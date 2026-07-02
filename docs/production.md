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
- Create/read/update/delete `Deployment`, `Service`, `ConfigMap`, and `Job`
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
| Request latency / errors | Gateway metrics |
| Runtime readiness | Engine `/health` |

## Upgrades

- Helm upgrade the operator and gateway independently.
- The operator uses namespace-scoped Lease leader election. Set
  `replicaCount` above one for control-plane failover; only the elected replica
  runs reconcilers.
- CRD changes require applying new manifests before upgrading the operator.
- Runtime image updates are triggered by changing `spec.runtime.image` or the `ModelRuntime` default image.
- Activation is not automatic on image change; re-activate explicitly.

## Known limitations

- GPU slicing is not supported.
- No hosted InferOps control plane; all components run in-cluster.
- Replacement and rollback are not implemented until MVP-108; replacement
  policy values fail safely instead of evicting a model.
- Advanced autoscaling and dashboard are not in month one.
