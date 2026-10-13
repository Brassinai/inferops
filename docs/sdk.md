# SDK Reference

The Python SDK turns decorated classes into `ModelDeployment` manifests. The
class is metadata only; the runtime image runs inference.

## Install

```bash
pip install -e sdk/python
```

## Built-in inference endpoint

```python
import inferops

app = inferops.App("my-app")

@app.model(name="qwen-chat", model="Qwen/Qwen2.5-7B-Instruct")
class QwenChat:
    pass
```

```bash
inferops generate app.py > manifests.yaml
inferops deploy app.py
inferops deploy app.py --activate
```

### Call the model

```python
client = inferops.Client(base_url="https://api.example.com", api_key="...")

resp = client.chat.completions.create(
    model="qwen-chat",
    messages=[{"role": "user", "content": "Hello"}],
)

stream = client.chat.completions.create(
    model="qwen-chat",
    messages=[{"role": "user", "content": "Hello"}],
    stream=True,
)
for event in stream:
    print(event)
```

Any OpenAI client works against `/models/<name>/v1/...`.

## Custom endpoints

Add app logic around the model:

```python
@app.model(name="qwen-chat", model="Qwen/Qwen2.5-7B-Instruct")
class Assistant:
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
- `self.generate(...)` → runtime-agnostic inference
- `self.generate_stream(...)` → runtime-agnostic stream

Deploy the model runtime first, then serve endpoints:

```bash
inferops deploy app.py
inferops activate qwen-chat
inferops gateway forward
inferops serve app.py --gateway-url http://127.0.0.1:8080 --port 9000
```

Call it:

```bash
curl -X POST http://127.0.0.1:9000/chat \
  -H "Content-Type: application/json" \
  -d '{"prompt":"Hello"}'
```

Deploy endpoints into the cluster:

```bash
inferops deploy-endpoints app.py \
  --image ghcr.io/brassinai/my-endpoint-app:v0.1.0 \
  --build
```

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

`inferops generate` sorts manifests and renders stable YAML for GitOps diffs.
