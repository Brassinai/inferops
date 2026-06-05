# inferops

Easy deployment and management of inference engines in your Kubernetes cluster.

## Entry Points

InferOps supports multiple ways to deploy and manage inference workloads.

### Python SDK

Declare an app in Python and deploy it through the CLI:

The `engine` field selects a managed runtime, such as `nano-vllm`, `vllm`, or `sglang`.

```python
import inferops

app = inferops.App("customer-support-llm")

@app.model(
    name="qwen-chat",
    engine="nano-vllm",
    model="Qwen/Qwen2.5-7B-Instruct",
    gpu="L4",
    min_replicas=0,
    max_replicas=4,
    max_model_len=4096,
)
class QwenChat:
    def __init__(self):
        from nanovllm import LLM
        self.llm = LLM("/models/qwen", tensor_parallel_size=1)

    @inferops.web_endpoint(method="POST", path="/chat")
    def chat(self, request):
        return self.llm.generate([request["prompt"]])
```

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
  model: Qwen/Qwen2.5-0.5B-Instruct
  runtimeRef: nano-vllm
```

```bash
kubectl apply -f modeldeployment.yaml
```

## Development

Useful commands:

```bash
make fmt
make test
make vet
make helm-lint
make python-check
make verify
```
