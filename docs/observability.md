# Observability Standard

InferOps dashboards are split into two layers:

- **Platform dashboards** use `inferops_` metrics exported by the operator and
  gateway. These panels must work for every runtime.
- **Runtime dashboards** use engine-native metrics such as `vllm:*` or
  `llamacpp:*`. These panels may be missing when a runtime does not export the
  underlying signal, and the dashboard must say so clearly.

## Metric Contract

InferOps-owned metrics must:

- use the `inferops_` prefix;
- keep labels bounded and stable;
- avoid object names, model repositories, namespaces, UIDs, request IDs, and
  user input in labels;
- expose counters with `_total`, histograms with `_seconds` or `_bytes`
  suffixes as appropriate, and gauges without `_total`;
- prefer stable reason labels for terminal failures.

Runtime-native metrics may preserve upstream names and labels, but dashboards
must aggregate them before display when the upstream label set is noisy.

## Prometheus And Grafana Setup

Install kube-prometheus-stack, then enable InferOps ServiceMonitors and
dashboard ConfigMaps:

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

Open Grafana locally:

```bash
kubectl -n monitoring port-forward svc/kube-prometheus-stack-grafana 3000:80
kubectl -n monitoring get secret kube-prometheus-stack-grafana \
  -o jsonpath="{.data.admin-password}" | base64 -d; echo
```

Log in at `http://127.0.0.1:3000` with username `admin`.

## Platform Metrics

The operator and gateway provide the baseline signals used by
`InferOps / Platform`:

- `inferops_modelcache_phase_count{phase}`: ModelCache availability by phase.
- `inferops_modelcache_reserved_bytes{phase}`: reserved cache storage by phase.
- `inferops_cache_download_duration_seconds`: cache download duration.
- `inferops_cache_download_failures_total{reason}`: terminal cache failures.
- `inferops_model_activation_duration_seconds`: activation-to-ready duration.
- `inferops_activation_queue_depth`: deployments waiting for GPU capacity.
- `inferops_gpu_slots_total{resource}`: scheduler-visible GPU slots.
- `inferops_gpu_slots_occupied{resource}`: occupied InferOps GPU slots.
- `inferops_gpu_slots_available{resource}`: available InferOps GPU slots.
- `inferops_gateway_requests_total{method,status_code}`: gateway request rate.
- `inferops_gateway_request_duration_seconds{method}`: gateway latency.
- `inferops_gateway_active_requests`: in-flight gateway requests.
- `inferops_gateway_upstream_errors_total{reason}`: upstream runtime failures.

## Runtime Dashboard Checklist

When adding a dashboard for a new runtime:

1. Keep the platform signals in `InferOps / Platform`; do not duplicate them
   unless the runtime view needs nearby context.
2. Use a dedicated dashboard title: `InferOps / <runtime> Runtime`.
3. Add variables only for bounded labels that the runtime reliably exports.
4. Include token throughput, request queue/active request pressure, and cache or
   memory pressure when those metrics exist.
5. Do not graph approximations as exact signals. If a runtime lacks TTFT,
   switch time, or GPU utilization, add a text panel that says the metric is not
   exported yet.
6. Render the Helm chart and parse the dashboard JSON before shipping.
