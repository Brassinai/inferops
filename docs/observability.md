# Observability Standard

Dashboards have two layers:

- **Platform dashboards** use `inferops_` metrics and work for every runtime.
- **Runtime dashboards** use engine-native metrics (`vllm:*`, `llamacpp:*`).

## Metric contract

InferOps metrics must:

- use the `inferops_` prefix;
- keep labels bounded and stable;
- exclude object names, repos, namespaces, UIDs, request IDs, and user input;
- name counters with `_total`, histograms with `_seconds` or `_bytes`;
- use stable reason labels for failures.

## Setup

```bash
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update

helm upgrade --install kube-prometheus-stack prometheus-community/kube-prometheus-stack \
  --namespace monitoring \
  --create-namespace \
  --set grafana.sidecar.dashboards.enabled=true \
  --set grafana.sidecar.dashboards.searchNamespace=ALL

helm upgrade inferops-operator deploy/helm/inferops-operator \
  --namespace default \
  --reuse-values \
  --set serviceMonitor.enabled=true \
  --set dashboards.enabled=true
```

Open Grafana:

```bash
kubectl -n monitoring port-forward svc/kube-prometheus-stack-grafana 3000:80
```

Username is `admin`. Get the password from the Grafana Secret.

## Platform metrics

| Metric | Meaning |
| --- | --- |
| `inferops_modelcache_phase_count` | Caches by phase |
| `inferops_cache_download_duration_seconds` | Cache download duration |
| `inferops_cache_download_failures_total` | Terminal cache failures |
| `inferops_model_activation_duration_seconds` | Activation duration |
| `inferops_activation_queue_depth` | Deployments waiting for GPU |
| `inferops_gpu_slots_*` | GPU slot availability |
| `inferops_gateway_requests_total` | Gateway request rate |
| `inferops_gateway_request_duration_seconds` | Gateway latency |
| `inferops_gateway_active_requests` | In-flight requests |
| `inferops_gateway_upstream_errors_total` | Upstream failures |

## Runtime dashboard checklist

When adding a dashboard for a new runtime:

1. Keep platform signals in `InferOps / Platform`.
2. Name it `InferOps / <runtime> Runtime`.
3. Use only bounded, reliably exported labels.
4. Include throughput, queue pressure, and memory/cache pressure when available.
5. Add text panels for missing metrics; do not graph approximations as exact.
6. Render the Helm chart and parse dashboard JSON before shipping.
