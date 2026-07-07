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
- Read referenced `Secret` for auth tokens

Neither component needs cluster-admin. Keep roles namespace-scoped where possible.

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
- Use `NetworkPolicy` to restrict runtime pods to gateway traffic only.
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
```

The packaged dashboard is vLLM-first, but runtime scraping is label-based and
works for any runtime Service that follows the InferOps runtime contract and
serves Prometheus metrics at its `ModelRuntime.spec.metricsPath`.
OpenTelemetry is intentionally not an MVP dependency.

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
- Replacement and rollback are not implemented until MVP-108; replacement
  policy values fail safely instead of evicting a model.
- Advanced autoscaling and OpenTelemetry export are not in month one.
