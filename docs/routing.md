# Routing

The InferOps gateway is the stable OpenAI-compatible endpoint. For a
`ModelDeployment` named `qwen-chat`, it accepts
`/models/qwen-chat/v1/...`, removes `/models/qwen-chat`, and proxies `/v1/...`
to the `qwen-chat-runtime` Service on port `8000`.

The gateway routes only when the deployment phase is `Active` and runtime
readiness is true. It stops admitting new requests before a deployment enters
drain. Inactive, waiting, activating, draining, and failed models remain
addressable at their stable route but receive an explicit unavailable response.

Custom `spec.routing.path` values are allowed, but every lane must support the
default `/models/<deployment-name>` convention. Streaming responses must not be
buffered.
