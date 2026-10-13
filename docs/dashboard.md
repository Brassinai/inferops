# Self-Hosted Dashboard

Read-only, in-cluster web UI for deployments, GPUs, caches, endpoints, Events,
and log selectors.

## Install

Install operator and gateway first, then:

```bash
helm upgrade --install inferops-dashboard deploy/helm/inferops-dashboard \
  --namespace inferops-system \
  --set-string dashboard.gatewayBaseURL=https://models.example.com \
  --set-string dashboard.prometheusURL=https://prometheus.example.com
```

Local access:

```bash
kubectl -n inferops-system port-forward svc/inferops-dashboard 8088:8080
```

Open `http://127.0.0.1:8088`.

## Security

The dashboard ServiceAccount can:

- `get`/`list` `ModelDeployment`, `ModelCache`, `ModelRuntime`, Pods,
  Services, Events in its namespace;
- `get`/`list` Nodes cluster-wide for GPU capacity.

It cannot read Secrets, write status, or mutate custom resources. Put
authentication in front of the Service before exposing it externally.

## API

| Endpoint | Purpose |
| --- | --- |
| `GET /api/snapshot` | Deployments, caches, GPUs, endpoints, Events |
| `GET /api/generated-yaml` | Sanitized `ModelDeployment` manifests |
| `GET /healthz` | Liveness |
| `GET /readyz` | Readiness |

## Grafana dashboards

Enable packaged Grafana dashboards on the operator chart:

```bash
helm upgrade --install inferops-operator deploy/helm/inferops-operator \
  --set serviceMonitor.enabled=true \
  --set dashboards.enabled=true
```

- `InferOps / Platform` — cache, GPU, gateway, activation signals.
- `InferOps / vLLM Runtime` — vLLM request, token, KV-cache metrics.
- `InferOps / llama.cpp Runtime` — llama.cpp token and request metrics.

## Limitations

- Read-only in this MVP.
- Prometheus/Grafana remain the source for time-series data.
