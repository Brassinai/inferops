# Production Notes

## Required cluster components

- Kubernetes 1.28+
- NVIDIA device plugin (GPU workloads)
- Container runtime with NVIDIA support (GPU workloads)
- `local` or `hostPath` storage for node-local cache, or a fast shared filesystem

## Capacity assumptions

- GPU workloads: equal requests and limits for `nvidia.com/gpu`
- CPU-only: explicit `cpu` and `memory` requests/limits
- Cache: sized to the largest model; plan 2x model size for extraction workspace

## RBAC

The operator needs:

- Read `ModelDeployment` and write its status
- Read `ModelRuntime` and write its status
- Create/read/update `ModelCache` and write its status
- Create/read/update/delete `Deployment`, `Service`, `ConfigMap`, `Job`, and
  `PodDisruptionBudget`
- Get referenced `Secret` objects only
- Read `Node` (scheduling decisions)
- Emit `Events`

The gateway needs:

- Read `ModelDeployment` and `Service`
- Receive the referenced auth `Secret` through a read-only projected volume

Neither component needs cluster-admin. Keep roles namespace-scoped where possible.
For per-team RoleBindings, quotas, and the supported shared-operator layout,
see [Namespace tenancy](tenancy.md).

## Secrets

- Runtime pods receive inference secrets (API keys), not model-download credentials.
- `ModelCache` download jobs receive registry credentials, not runtime inference secrets.
- Never log Secret contents, kubeconfigs, or tokens.

## Image immutability

Release installations should pin `cache.downloaderImage`, operator, gateway,
and runtime images by immutable digest. Non-`latest` tags are accepted for
development builds, but operators should not treat a mutable registry tag as a
reproducible production release. Build the downloader locally with
`make model-downloader-build` and run its focused tests with
`make model-downloader-test`.

## Network

- Gateway exposes the OpenAI-compatible endpoint.
- The Helm charts enable NetworkPolicies that restrict runtime ingress to the
  same-namespace gateway and deny runtime egress by default.
- Set the exact Kubernetes API Service IP and narrow gateway ingress peers for
  the target cluster as described in [Namespace tenancy](tenancy.md).
- Use Tailscale or an ingress controller for external access; do not expose runtime pods directly.

## Monitoring

Watch these:

| Metric | Source |
| --- | --- |
| Deployment phase | `ModelDeployment` status |
| GPU slots used/available | Node allocatable + pod requests |
| GPU slots | `inferops_gpu_slots_total`, `inferops_gpu_slots_occupied`, `inferops_gpu_slots_available` |
| Activation queue | `inferops_activation_queue_depth` |
| Activation duration | `inferops_model_activation_duration_seconds` |
| Cache download duration | `inferops_cache_download_duration_seconds` |
| Lifecycle failures | `inferops_controller_failures_total` |
| Activation failures | `inferops_activation_failures_total` |
| Cache failures | `inferops_cache_download_failures_total` |
| Gateway process metrics | Gateway `/metrics` |
| Gateway request count | `inferops_gateway_requests_total` |
| Gateway request latency | `inferops_gateway_request_duration_seconds` |
| Gateway active requests | `inferops_gateway_active_requests` |
| Gateway upstream failures | `inferops_gateway_upstream_errors_total` |
| Runtime TTFT | vLLM `vllm:time_to_first_token_seconds` |
| Runtime tokens/sec | vLLM `vllm:generation_tokens_total` rate |
| Pending runtime work | vLLM `vllm:num_requests_waiting`; pending-token panels approximate token debt from recent request sizes |
| Runtime KV cache pressure | vLLM `vllm:kv_cache_usage_perc` |
| GPU utilization | DCGM exporter or equivalent node GPU metrics |
| Model load / switch time | `inferops_model_activation_duration_seconds` until a dedicated replacement workflow metric exists |
| Runtime readiness | Engine `/health` |

Enable Prometheus Operator integration with Helm values:

```bash
helm upgrade --install inferops-operator deploy/helm/inferops-operator \
  --set serviceMonitor.enabled=true \
  --set dashboards.enabled=true

helm upgrade --install inferops-gateway deploy/helm/inferops-gateway \
  --set serviceMonitor.enabled=true

helm upgrade --install inferops-runtime deploy/helm/inferops-runtime \
  --set metrics.serviceMonitor.enabled=true
```

The packaged Grafana dashboard is vLLM-first, but runtime scraping is
label-based and works for runtime Services that follow the InferOps runtime
contract and serve Prometheus metrics at the configured
`serviceMonitor.runtimes.path`. Keep that chart value aligned with the deployed
`ModelRuntime.spec.metricsPath`; use a dedicated ServiceMonitor when one
namespace mixes runtimes with different metrics paths.
OpenTelemetry is intentionally not an MVP dependency.

## Self-hosted dashboard

Install the optional read-only dashboard when operators need an in-cluster view
of deployments, GPUs, caches, endpoints, Events, log selectors, metrics query
hints, activation state, scaling state, and sanitized generated YAML:

```bash
helm upgrade --install inferops-dashboard deploy/helm/inferops-dashboard \
  --namespace inferops-system \
  --set-string dashboard.gatewayBaseURL=https://models.example.com \
  --set-string dashboard.prometheusURL=https://prometheus.example.com
```

The dashboard Service is `ClusterIP` by default and should stay behind
port-forwarding, a private network, or an authenticated internal ingress. Its
RBAC is read-only and intentionally excludes Secret reads and custom-resource
mutation. See [Self-hosted dashboard](dashboard.md).

## Upgrades

- Back up the InferOps custom resources and referenced Secrets before changing
  CRDs or controller images. See [Backup and disaster recovery](disaster-recovery.md).
- Run `make verify`, then server-side dry-run the new CRDs against the target
  cluster.
- Apply CRDs before upgrading the operator. Helm does not upgrade files from a
  chart's `crds/` directory:

  ```bash
  kubectl apply --server-side --dry-run=server -f deploy/manifests/crds
  kubectl apply --server-side -f deploy/manifests/crds
  ```

  `inferops install` performs this CRD apply automatically before its Helm
  upgrades.

- Helm upgrade the operator and gateway independently with `--atomic --wait`.
- The operator uses namespace-scoped Lease leader election. Set
  `replicaCount` above one for control-plane failover; only the elected replica
  runs reconcilers.
- Runtime image updates are triggered by changing `spec.runtime.image` or the `ModelRuntime` default image.
- Activation is not automatic on image change; re-activate explicitly.

The validating admission configuration uses `failurePolicy: Fail`: malformed
InferOps resources are rejected before reconciliation, and writes are also
rejected while every webhook endpoint is unavailable. Existing runtime traffic
does not depend on the webhook. The chart creates a self-signed serving
certificate and preserves it through Helm upgrades with `lookup`; deleting the
`*-webhook-certs` Secret intentionally rotates the CA and serving certificate
on the next upgrade. GitOps renderers that cannot use cluster-side `lookup`
should provide a stable TLS Secret and PEM CA through
`webhook.tls.existingSecret` and `webhook.tls.caBundle`; the Secret must contain
`tls.crt` and `tls.key`.

The operator and gateway charts create `PodDisruptionBudget` objects by
default. A single-replica component therefore blocks voluntary eviction.
Increase replicas before planned node maintenance, or explicitly adjust the
chart's `podDisruptionBudget` values after accepting the availability impact.

## Known limitations

- GPU slicing is not supported.
- No hosted InferOps control plane; all components run in-cluster.
- Single-GPU replacement has unavoidable downtime while the current runtime
  releases the GPU and the replacement becomes ready. Activation failures
  trigger a best-effort rollback whose outcome is reported in status and
  Events; operators must intervene when rollback also fails.
- Advanced `Rolling`, `BlueGreen`, and `Canary` rollouts require spare
  compatible GPU capacity on the cache-local node. Simulated multi-GPU tests
  protect the scheduler contract, but real hardware release drills must still
  cover vendor device-plugin failures, physical GPU health faults, and topology
  constraints.
- The self-hosted dashboard is read-only. Activation, scaling, and rollout
  changes remain explicit CLI, SDK, YAML, or GitOps operations.
