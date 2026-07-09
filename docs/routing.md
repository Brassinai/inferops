# Routing

The InferOps gateway is the stable OpenAI-compatible endpoint. For a
`ModelDeployment` named `qwen-chat`, it accepts
`/models/qwen-chat/v1/...`, removes `/models/qwen-chat`, and proxies `/v1/...`
to the `qwen-chat-runtime` Service on port `8000`.

The gateway routes only when the deployment phase is `Active` and runtime
readiness is true for the current `ModelDeployment` generation, ready replicas
exist, the operator-owned runtime Service matches the expected selector, and
an EndpointSlice contains a ready, non-terminating endpoint. It stops admitting
new requests as soon as discovery observes `Draining`. Inactive, waiting,
activating, draining, and failed models remain addressable at their stable route
but receive an explicit lifecycle response.

At drain start the operator also removes the stable runtime Service selector.
This retains the Service and ClusterIP while EndpointSlice reconciliation
withdraws the runtime endpoints, providing a second fail-closed barrier if a
gateway instance is briefly serving an older discovery snapshot. The selector
is restored before reactivation.

Keep `spec.activation.drainTimeout` longer than the configured gateway
`discovery.syncInterval`. The defaults are five minutes and five seconds,
respectively, leaving ample time for every healthy gateway replica to observe
`Draining` before the in-flight grace period ends.

Custom `spec.routing.path` values are allowed, but every lane must support the
default `/models/<deployment-name>` convention. Streaming responses must not be
buffered.

North-south exposure does not alter these paths. The gateway chart supports
standard Ingress, Gateway API HTTPRoute, LoadBalancer Service, and Tailscale
front doors while preserving `/models/<name>/v1/...`; see
[Cluster and ingress support](cluster-ingress.md).

The namespace-scoped registry is refreshed from `ModelDeployment`, Service, and
EndpointSlice objects every five seconds by default. `discovery.syncInterval`
configures this bound and must be at least one second. A failed Kubernetes read
keeps the last complete registry snapshot and is retried with exponential
delay. If discovery cannot refresh for three configured intervals, readiness
fails and every previously ready backend is marked unavailable until a complete
query succeeds. Readiness also remains false until the first complete snapshot.

`GET /drainz?namespace=<namespace>&model=<name>` returns one gateway process's
observed drain state for the matching backend, including active request count
and `drainComplete`. When gateway authentication is enabled, `/drainz` requires
the same bearer token as model traffic. The operator chart does not trust a
single load-balanced `/drainz` response by default; it lists the gateway
Service's ready EndpointSlice addresses and requires every ready gateway pod to
report `drainComplete=true` before finishing deactivation or replacement
drains. `spec.activation.drainTimeout` remains the fallback when gateway status
is unavailable or requests keep streaming.

Gateway errors use the OpenAI `{"error": ...}` envelope:

| Model lifecycle state | HTTP status | Error code |
| --- | ---: | --- |
| Unknown route | `404` | `model_not_found` |
| Cached / inactive | `409` | `model_inactive` |
| Pending, downloading, or waiting for capacity/GPU | `503` | `model_activating` |
| Activating | `503` | `model_activating` |
| Draining | `503` | `model_draining` |
| Failed, stale, or unready | `503` | `model_unavailable` |
| Runtime connection failure | `502` | `upstream_error` |

`503` lifecycle responses include `Retry-After`. Request bodies, headers,
streaming responses, query parameters, and client cancellation are passed
through to the selected runtime. Only paths below the selected model's `/v1`
prefix are proxied. Custom route prefixes must be canonical paths: traversal,
duplicate separators, URL escapes, backslashes, query or fragment delimiters,
and the reserved `/healthz`, `/readyz`, and `/metrics` trees are rejected
during admission.

## Authentication

Gateway bearer authentication is optional and fails closed when enabled. Create
a Secret whose selected key contains one token per line, then configure the
chart:

```bash
kubectl create secret generic inferops-gateway-token \
  --from-literal=token='<random bearer token>' \
  --namespace inferops-system

helm upgrade --install inferops-gateway ./deploy/helm/inferops-gateway \
  --namespace inferops-system \
  --set auth.enabled=true \
  --set auth.secretName=inferops-gateway-token
```

Clients send `Authorization: Bearer <token>`. Missing or invalid credentials
receive an OpenAI-shaped `401` response. The Secret is mounted read-only and
reloaded on a bounded one-second interval, so projected Secret updates are
accepted without a gateway restart or per-request filesystem I/O. An unreadable
or empty token file returns `503`, fails gateway readiness, and does not forward
traffic. Gateway credentials are removed before a request reaches the runtime.

The gateway does not log authorization headers or request bodies.

## Metrics

`GET /metrics` exposes:

- `inferops_gateway_requests_total`
- `inferops_gateway_request_duration_seconds`
- `inferops_gateway_active_requests`
- `inferops_gateway_upstream_errors_total`

Labels are limited to normalized HTTP methods, status codes, and stable error
reasons. Model names, repositories, route paths, credentials, and object UIDs
are intentionally excluded.

## Gateway configuration

The Helm chart supplies these settings to the gateway:

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `INFEROPS_GATEWAY_ADDRESS` | `:8080` | HTTP listen address |
| `INFEROPS_GATEWAY_REGISTRY` | `kubernetes` | Registry mode; `fake` starts with an empty in-memory registry for package-level development |
| `INFEROPS_GATEWAY_SYNC_INTERVAL` | `5s` | Kubernetes query and successful refresh interval |
| `INFEROPS_GATEWAY_AUTH_TOKEN_FILE` | empty | Enables auth using the mounted newline-delimited token file |
| `POD_NAMESPACE` | none | Required namespace for Kubernetes discovery; the chart injects it from pod metadata |

The gateway ServiceAccount has namespace-scoped `list` access only to
`ModelDeployment`, Service, and EndpointSlice objects. Kubernetes mounts the
referenced authentication Secret, so the gateway process does not need Secret
API permissions. Set
`serviceAccount.create` and `rbac.create` to `false` only when supplying
equivalent identities and permissions externally.
