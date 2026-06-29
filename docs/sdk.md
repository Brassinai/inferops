# SDK Reference

The Python SDK compiles decorated classes into `ModelDeployment` manifests. The decorated class is metadata only; the runtime image owns inference.

## Install

```bash
pip install -e sdk/python
```

## Model lane

Built-in inference API managed by InferOps.

```python
import inferops

app = inferops.App("my-app")

@app.model(name="qwen-chat", model="Qwen/Qwen2.5-7B-Instruct")
class QwenChat:
    pass
```

Generate or deploy:

```bash
inferops generate app.py > manifests.yaml
inferops deploy app.py
inferops deploy app.py --activate
```

### Call the model

Via the Python client:

```python
client = inferops.Client(base_url="https://api.example.com", api_key="...")

# Non-streaming
resp = client.chat.completions.create(
    model="qwen-chat",
    messages=[{"role": "user", "content": "Hello"}],
)

# Streaming
stream = client.chat.completions.create(
    model="qwen-chat",
    messages=[{"role": "user", "content": "Hello"}],
    stream=True,
)
for event in stream:
    print(event)
```

Or use any OpenAI-compatible HTTP client against:

```
/models/qwen-chat/v1/chat/completions
```

## Custom endpoint lane

Add app logic (preprocessing, RAG, guardrails) around the model.

```python
import inferops

app = inferops.App("my-app")

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

Endpoint semantics:

- `return` → JSON response
- `yield` or async iterator → streaming response
- `self.generate(...)` → runtime-agnostic inference call
- `self.generate_stream(...)` → runtime-agnostic stream

## Decorator options

```python
@app.model(
    name="qwen-chat",
    engine="nano-vllm",          # ModelRuntime ref; default: nano-vllm
    model="Qwen/Qwen2.5-7B-Instruct",
    gpu=1,                        # int = count; str = type; None = CPU-only
    gpu_vendor="nvidia",
    cpu="8",
    memory="32Gi",
    activation="Inactive",        # Inactive | Active
    when_full="Queue",            # Queue | Reject | ReplaceOldest | ReplaceLowestPriority
    priority=50,
    drain_timeout="5m",
    min_replicas=0,
    max_replicas=1,
    max_model_len=4096,
    route_path="/models/qwen-chat",
    openai_compatible=True,
    cache_enabled=True,
    cache_type="nodeLocal",
    cache_size="100Gi",
    cache_path="/var/lib/inferops/models",
    hugging_face_token_secret_name="hf-token",
)
class QwenChat:
    pass
```

## Validation

Invalid inputs fail before YAML is generated:

```python
@app.model(name="", model="Qwen/Qwen2.5-7B-Instruct")
class Bad:
    pass
# ValueError: name is required
```

## Deterministic output

`inferops generate` sorts manifests by name and renders stable YAML for GitOps diffs.
