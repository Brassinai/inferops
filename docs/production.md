# Production Notes

## Cluster requirements

- Kubernetes 1.28+
- NVIDIA device plugin for GPU workloads
- Container runtime with NVIDIA support for GPU workloads
- Fast storage for node-local cache

## Capacity planning

| Workload | Rule |
| --- | --- |
| GPU | Equal requests and limits for `nvidia.com/gpu` |
| CPU-only | Explicit `cpu` and `memory` requests/limits |
| Cache | Size to largest model; plan 2x for extraction workspace |

## RBAC

**Operator needs:**

- Read/write `ModelDeployment` status
- Read/write `ModelRuntime` status
- Read/write `ModelCache` and status
- CRUD `Deployment`, `Service`, `ConfigMap`, `Job`, `PodDisruptionBudget`
- `get` referenced Secrets
- Read Nodes
- Emit Events

**Gateway needs:**

- Read `ModelDeployment` and `Service`
- Receive auth Secret through projected volume

Neither needs cluster-admin.

## Secrets

- Runtime pods get inference secrets, not download credentials.
- Cache Jobs get download credentials, not runtime secrets.
- Never log Secret contents, kubeconfigs, or tokens.

## Images

Pin operator, gateway, downloader, and runtime images by immutable digest or
non-`latest` tag. Build the downloader locally with `make model-downloader-build`.

## Network

- Gateway exposes the OpenAI-compatible endpoint.
- NetworkPolicies restrict runtime ingress to the same-namespace gateway and
  deny runtime egress by default.
- Do not expose runtime pods directly.
- Use Tailscale or an ingress controller for external access.

## Monitoring

Key metrics:

| Metric | Source |
| --- | --- |
| Deployment phase | `ModelDeployment` status |
| GPU slots | `inferops_gpu_slots_*` |
| Activation queue | `inferops_activation_queue_depth` |
| Activation duration | `inferops_model_activation_duration_seconds` |
| Cache failures | `inferops_cache_download_failures_total` |
| Gateway requests | `inferops_gateway_requests_total` |
| Gateway latency | `inferops_gateway_request_duration_seconds` |
| Runtime metrics | Engine `/metrics` |

Enable Prometheus scraping:

```bash
helm upgrade --install inferops-operator deploy/helm/inferops-operator \
  --set serviceMonitor.enabled=true \
  --set dashboards.enabled=true

helm upgrade --install inferops-gateway deploy/helm/inferops-gateway \
  --set serviceMonitor.enabled=true
```

## Dashboard

Optional read-only UI:

```bash
helm upgrade --install inferops-dashboard deploy/helm/inferops-dashboard \
  --namespace inferops-system \
  --set-string dashboard.gatewayBaseURL=https://models.example.com \
  --set-string dashboard.prometheusURL=https://prometheus.example.com
```

Keep it behind authentication.

## Upgrades

1. Back up custom resources and Secrets. See [Disaster recovery](disaster-recovery.md).
2. Run `make verify`.
3. Apply CRDs before upgrading the operator (Helm does not upgrade `crds/`):

```bash
kubectl apply --server-side -f deploy/manifests/crds
```

4. Upgrade operator and gateway independently:

```bash
helm upgrade --install inferops-operator deploy/helm/inferops-operator \
  --namespace inferops-system --atomic --wait

helm upgrade --install inferops-gateway deploy/helm/inferops-gateway \
  --namespace inferops-system --atomic --wait
```

5. Runtime image updates require re-activation.

## Known limitations

- No GPU slicing.
- No hosted InferOps control plane.
- Single-GPU replacement has downtime.
- Rolling/blue-green/canary rollouts need spare GPU capacity.
- Dashboard is read-only.
