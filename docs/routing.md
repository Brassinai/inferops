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

Custom `spec.routing.path` values are allowed, but every lane must support the
default `/models/<deployment-name>` convention. Streaming responses must not be
buffered.

The namespace-scoped registry is refreshed from `ModelDeployment`, Service, and
EndpointSlice objects every five seconds by default. `discovery.syncInterval`
configures this bound and must be at least one second. A failed Kubernetes read
keeps the last complete registry snapshot and is retried with exponential
delay. If discovery cannot refresh for three configured intervals, readiness
fails and every previously ready backend is marked unavailable until a complete
query succeeds. Readiness also remains false until the first complete snapshot.

Gateway errors use the OpenAI `{"error": ...}` envelope:

| Model state | HTTP status | Error code |
| --- | ---: | --- |
| Unknown route | `404` | `model_not_found` |
| Inactive | `409` | `model_inactive` |
| Activating | `503` | `model_activating` |
| Draining | `503` | `model_draining` |
| Failed, stale, or unready | `503` | `model_unavailable` |
| Runtime connection failure | `502` | `upstream_error` |

`503` lifecycle responses include `Retry-After`. Request bodies, headers,
streaming responses, query parameters, and client cancellation are passed
through to the selected runtime. Only paths below the selected model's `/v1`
prefix are proxied. Custom route prefixes must be canonical paths: traversal,
duplicate separators, URL escapes, backslashes, query or fragment delimiters,
and the reserved `/healthz` and `/readyz` trees are rejected during admission.

## Gateway configuration

The Helm chart supplies these settings to the gateway:

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `INFEROPS_GATEWAY_ADDRESS` | `:8080` | HTTP listen address |
| `INFEROPS_GATEWAY_REGISTRY` | `kubernetes` | Registry mode; `fake` starts with an empty in-memory registry for package-level development |
| `INFEROPS_GATEWAY_SYNC_INTERVAL` | `5s` | Kubernetes query and successful refresh interval |
| `POD_NAMESPACE` | none | Required namespace for Kubernetes discovery; the chart injects it from pod metadata |

The gateway ServiceAccount has namespace-scoped `list` access only to
`ModelDeployment`, Service, and EndpointSlice objects. Set
`serviceAccount.create` and `rbac.create` to `false` only when supplying
equivalent identities and permissions externally.
