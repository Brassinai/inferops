# InferOps

Kubernetes-native deployment and management for OpenAI-compatible inference
runtimes. InferOps is designed to support nano-vLLM, vLLM, SGLang, and
llama.cpp; `nano-vllm` is the default runtime and the first packaged
integration.

## Entry Points

InferOps supports multiple ways to deploy and manage inference workloads.

### Python SDK

Declare model metadata in Python and deploy it through the CLI. The decorated
class is a declaration marker; InferOps does not execute it or instantiate an
engine inside the runtime pod:

The optional `engine` field selects a `ModelRuntime`, such as `nano-vllm`,
`vllm`, `sglang`, or the CPU-friendly `llama-cpp`. Omitting it in the Python
SDK defaults to `nano-vllm`.
The `gpu` field defaults to one GPU for compatibility; set `gpu=None` for a
CPU-only deployment.

```python
import inferops

app = inferops.App("customer-support-llm")

@app.model(
    name="qwen-chat",
    model="Qwen/Qwen2.5-7B-Instruct",
    gpu="L4",
    min_replicas=0,
    max_replicas=4,
    max_model_len=4096,
)
class QwenChat:
    pass
```

Call the built-in inference API through the SDK client:

```python
client = inferops.Client(base_url="https://api.example.com", api_key="...")

response = client.responses.create(
    model="qwen-chat",
    input="Explain Kubernetes Services simply.",
)

stream = client.chat.completions.create(
    model="qwen-chat",
    messages=[{"role": "user", "content": "Write a short poem."}],
    stream=True,
)
```

Add custom model-backed endpoints when you need preprocessing, tools, or SSE:

```python
@app.model(name="assistant", model="Qwen/Qwen2.5-7B-Instruct")
class Assistant:
    @inferops.web_endpoint(method="POST", path="/chat")
    async def chat(self, request):
        return await self.generate(request["prompt"])

    @inferops.web_endpoint(method="POST", path="/chat/stream")
    async def stream_chat(self, request):
        async for chunk in self.generate_stream(request["prompt"]):
            yield chunk
```

If a handler returns an async iterator instead of yielding directly, declare it
with `streaming=True` so the endpoint contract stays explicit.

```bash
inferops deploy app.py
inferops generate app.py > modeldeployment.yaml
```

### CLI

Use the CLI for common deployment operations:

```bash
inferops deploy app.py
inferops generate app.py
inferops status support-bot
inferops logs support-bot
inferops delete support-bot
```

Deployments are designed to be idempotent. `inferops deploy` should write deployment metadata under `.inferops/` in the user's project, compare the current app spec with the last applied spec, and update the existing Kubernetes resources instead of creating duplicate instances.

### Kubernetes YAML

Use CRDs directly when you want a Kubernetes-native workflow:

```yaml
apiVersion: inference.inferops.dev/v1alpha1
kind: ModelDeployment
metadata:
  name: support-bot
spec:
  model:
    name: support-bot
    source: huggingface
    repo: Qwen/Qwen2.5-0.5B-Instruct
    revision: main
  runtime:
    ref: vllm
  resources:
    cpu: "8"
    memory: 32Gi
  activation:
    desiredState: Inactive
    whenFull: Queue
```

```bash
kubectl apply -f modeldeployment.yaml
```

## Documentation

- [Quickstart](docs/quickstart.md)
- [Homelab setup](docs/homelab.md)
- [CLI reference](docs/cli.md)
- [SDK reference](docs/sdk.md)
- [CRD reference](docs/crds.md)
- [Production notes](docs/production.md)

## Development

Install the required tool versions described in
[docs/development.md](docs/development.md), then run the same required
verification used by CI:

```bash
make verify
```

Useful focused commands:

```bash
make fmt
make fmt-check
make test
make vet
make python-check
make python-test
make helm-lint
make helm-template
make yaml-check
make schema-check
```

Verification requires no GPU or live Kubernetes cluster.
