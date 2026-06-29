# SDK

The Python SDK provides a developer-friendly path for declaring model
deployments and model-backed application endpoints. Each declared model
compiles into one `inference.inferops.dev/v1alpha1` `ModelDeployment`. The
output follows the contract in [crds.md](crds.md), defaults to an inactive
deployment, and uses the stable `/models/<deployment-name>` gateway route. An
app containing multiple models produces multiple `ModelDeployment` objects,
sorted by deployment name for deterministic GitOps-friendly YAML output.

## Two SDK Lanes

### Model lane

The model lane is the built-in inference API. InferOps manages routing,
rollout, scaling, auth, and streaming passthrough.

```python
import inferops

app = inferops.App("customer-support")

@app.model(name="qwen-chat", model="Qwen/Qwen2.5-7B-Instruct")
class QwenChat:
    pass
```

Users can call the built-in inference API through the Python client:

```python
client = inferops.Client(base_url="https://api.example.com", api_key="...")

response = client.responses.create(
    model="qwen-chat",
    input="Explain Kubernetes Services simply.",
)

stream = client.responses.stream(
    model="qwen-chat",
    input="Write a short poem.",
)
```

OpenAI-compatible chat completions are available through the same client and
the stable HTTP API. Streaming is enabled with `stream=True`.

```python
events = client.chat.completions.create(
    model="qwen-chat",
    messages=[{"role": "user", "content": "Say hello."}],
    stream=True,
)
```

### Custom endpoint lane

The custom endpoint lane is for app logic around a model such as
preprocessing, RAG, tools, guardrails, custom JSON routes, and SSE.

```python
import inferops

app = inferops.App("customer-support")

@app.model(name="qwen-chat", model="Qwen/Qwen2.5-7B-Instruct")
class QwenChat:
    @inferops.web_endpoint(method="POST", path="/chat")
    async def chat(self, request):
        return await self.generate(request["prompt"])

    @inferops.web_endpoint(method="POST", path="/chat/stream")
    async def stream_chat(self, request):
        async for chunk in self.generate_stream(request["prompt"]):
            yield chunk
```

Custom endpoint semantics are:

- `return ...` means a normal JSON or HTTP response.
- `yield ...` means a streaming response.
- Returning an async iterator is also supported for streaming, but declare the
  endpoint with `streaming=True` so the route contract is explicit.
- `self.generate(...)` delegates through the selected runtime abstraction.
- `self.generate_stream(...)` yields runtime-agnostic token or event chunks.

## Validation And Manifest Generation

The decorator's `engine` value becomes `spec.runtime.ref`; it is not limited to
the default runtime. Omitting `engine` selects `nano-vllm`.

The decorator defaults to one NVIDIA GPU for compatibility. Setting `gpu=None`
produces a CPU-only deployment. Supplying a positive integer requests that many
NVIDIA GPUs; supplying a non-empty string requests one NVIDIA GPU of that type.
The selected runtime image must support the requested compute mode.

The SDK validates decorator inputs eagerly so invalid model names, activation
policies, scaling bounds, route paths, cache settings, and GPU requests fail
before YAML is generated. Supported high-level deployment options include:

- `activation` and `when_full` for activation policy.
- `min_replicas` and `max_replicas` for explicit scaling bounds.
- `route_path`, `route_enabled`, and `openai_compatible` for gateway routing.
- `cache_enabled`, `cache_type`, `cache_size`, and `cache_path` for model cache settings.
- `runtime_image`, `dtype`, and `hugging_face_token_secret_name` for runtime overrides and source credentials.

The SDK talks to the user's Kubernetes API through the CLI or Kubernetes
client. It does not rely on a hosted InferOps control plane.
