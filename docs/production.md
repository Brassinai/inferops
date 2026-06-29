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

- Read/write `ModelDeployment`, `ModelRuntime`, `ModelCache`
- Create/read/update `Deployment`, `Service`, `ConfigMap`, `Job`, `Secret` (reference only), `PersistentVolumeClaim`
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
| Cache download duration | `ModelCache` conditions |
| Request latency / errors | Gateway metrics |
| Runtime readiness | Engine `/health` |

## Upgrades

- Helm upgrade the operator and gateway independently.
- CRD changes require applying new manifests before upgrading the operator.
- Runtime image updates are triggered by changing `spec.runtime.image` or the `ModelRuntime` default image.
- Activation is not automatic on image change; re-activate explicitly.

## Known limitations

- GPU slicing is not supported.
- No hosted InferOps control plane; all components run in-cluster.
- No automatic eviction without an explicit replacement policy.
- Advanced autoscaling and dashboard are not in month one.
