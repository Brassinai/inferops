# Self-Hosted Dashboard

InferOps includes a self-hosted dashboard for operators who want an in-cluster
view of active model deployments, GPU allocation, caches, endpoints, recent
Events, log selectors, activation state, and scaling state.

The dashboard is deliberately not an InferOps-hosted control plane. It runs in
the user's cluster, talks to the Kubernetes API with its own ServiceAccount, and
is read-only in this MVP.

## Install

Install the operator and gateway first, then install or upgrade the dashboard in
the same namespace:

```bash
helm upgrade --install inferops-dashboard deploy/helm/inferops-dashboard \
  --namespace inferops-system \
  --set-string dashboard.gatewayBaseURL=https://models.example.com \
  --set-string dashboard.prometheusURL=https://prometheus.example.com
```

For local access, port-forward the Service:

```bash
kubectl -n inferops-system port-forward svc/inferops-dashboard 8088:8080
```

Then open `http://127.0.0.1:8088`.

## Security Model

The dashboard chart grants:

- namespaced `get` and `list` on `ModelDeployment`, `ModelCache`,
  `ModelRuntime`, Pods, Services, and Events;
- cluster-scoped `get` and `list` on Nodes, only to report GPU capacity.

It does not grant Secret reads, status writes, or custom-resource mutation. The
generated YAML endpoint removes `spec.secrets`, status, live annotations, UIDs,
and resource versions before returning manifests.

The Service is `ClusterIP` by default. Put authentication in front of the
dashboard before exposing it outside a trusted network, and narrow
`networkPolicy.ingressFrom` and `networkPolicy.apiServerCIDRs` for the target
cluster.

## API

- `GET /api/snapshot` returns deployments, caches, runtimes, GPU summaries,
  endpoint summaries, recent Events, log selectors, and Prometheus query hints.
- `GET /api/generated-yaml` returns sanitized `ModelDeployment` manifests for
  currently visible deployments. This endpoint is kept for API consumers and is
  not shown in the default dashboard UI.
- `GET /healthz` and `GET /readyz` support Kubernetes probes.

## Metrics And Grafana

When `serviceMonitor.enabled=true` and `dashboards.enabled=true` are set on the
operator chart, Grafana sidecar installations can discover:

- `InferOps / Platform` for runtime-independent cache, activation, GPU, and
  gateway signals.
- `InferOps / vLLM Runtime` for vLLM-native request, token, TTFT, and KV-cache
  metrics.
- `InferOps / llama.cpp Runtime` for llama.cpp-native token and request metrics.

See `docs/observability.md` for the metric naming, label-cardinality, and
runtime-dashboard standards.

## Limitations

- The dashboard is read-only; activation and scaling changes remain explicit
  `kubectl`, CLI, SDK, or GitOps updates.
- Grafana and Prometheus remain the source of truth for time-series data.
- GPU request totals are computed from pods visible in the dashboard namespace;
  Node capacity is cluster-visible through the minimal Node read permission.
