# Routing

The gateway is the stable OpenAI-compatible endpoint. For a deployment named
`qwen-chat`, it accepts `/models/qwen-chat/v1/...`, strips the prefix, and
proxies `/v1/...` to the `qwen-chat-runtime` Service on port `8000`.

## Route states

The gateway routes only when the deployment is `Active`, readiness passes,
and ready endpoints exist. Other states return lifecycle responses.

| State | HTTP status | Error code |
| --- | ---: | --- |
| Unknown route | `404` | `model_not_found` |
| Cached / inactive | `409` | `model_inactive` |
| Pending / downloading / waiting | `503` | `model_activating` |
| Activating | `503` | `model_activating` |
| Draining | `503` | `model_draining` |
| Failed / unready | `503` | `model_unavailable` |
| Runtime connection failure | `502` | `upstream_error` |

`503` responses include `Retry-After`.

## Routing policy

Ready deployments sharing the same `spec.routing.path` are selected by:

| Strategy | Behavior |
| --- | --- |
| `LeastLoaded` | Send to backend with fewest in-flight requests (default) |
| `Weighted` | Smooth weighted round-robin |

Weight `0` keeps a backend discoverable but excludes it from new traffic. If
all ready candidates have weight `0`, the route returns `503`.

Rate limits are per-gateway-process token buckets. Enforce global quotas at
your upstream ingress.

Request logging is sanitized: model, namespace, route, method, path, status,
duration. Bodies and credentials are never logged.

## Authentication

Create a Secret and enable auth:

```bash
kubectl create secret generic inferops-gateway-token \
  --from-literal=token='<random token>' \
  --namespace inferops-system

helm upgrade --install inferops-gateway ./deploy/helm/inferops-gateway \
  --namespace inferops-system \
  --set auth.enabled=true \
  --set auth.secretName=inferops-gateway-token
```

Clients send `Authorization: Bearer <token>`. Missing or invalid credentials
get a `401`. The Secret reloads every second; no gateway restart needed.

## Metrics

`GET /metrics` exposes:

- `inferops_gateway_requests_total`
- `inferops_gateway_request_duration_seconds`
- `inferops_gateway_active_requests`
- `inferops_gateway_upstream_errors_total`

Labels are limited to method, status code, and stable error reasons.

## Gateway configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `INFEROPS_GATEWAY_ADDRESS` | `:8080` | Listen address |
| `INFEROPS_GATEWAY_SYNC_INTERVAL` | `5s` | Registry refresh interval |
| `INFEROPS_GATEWAY_RATE_LIMIT_RPM` | `0` | Default per-backend RPM |
| `INFEROPS_GATEWAY_REQUEST_LOGGING` | `false` | Default request logging |
| `INFEROPS_GATEWAY_AUTH_TOKEN_FILE` | empty | Token file for auth |
| `POD_NAMESPACE` | required | Namespace for discovery |

The gateway ServiceAccount needs namespace-scoped `list` on `ModelDeployment`,
`Service`, and `EndpointSlice`.
