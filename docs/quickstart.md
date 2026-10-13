# Quickstart

Deploy an OpenAI-compatible model on a single k3s node. Use a GPU runtime
(`nano-vllm`, `vllM`, `sglang`) or the CPU-only `llama-cpp` path.

## What you need

- Linux node. GPU only required for GPU runtimes.
- `kubectl` and Helm 3.15+.
- k3s with NVIDIA container runtime and device plugin (GPU only).
- Hugging Face token as a Kubernetes Secret for gated models.

## Install InferOps

```bash
# GPU homelab
inferops install --profile homelab \
  --compute-profile nvidia-gpu \
  --cache-path /var/lib/inferops/models \
  --cache-capacity 500Gi

# CPU-only homelab
inferops install --profile homelab \
  --cache-path /var/lib/inferops/models \
  --cache-capacity 500Gi
```

Verify:

```bash
kubectl get pods
kubectl get modelruntime
inferops doctor
```

## Deploy a model

**Option A: YAML**

```yaml
# qwen.yaml
apiVersion: inference.inferops.dev/v1alpha1
kind: ModelDeployment
metadata:
  name: qwen-chat
spec:
  model:
    name: qwen-chat
    source: huggingface
    repo: Qwen/Qwen2.5-0.5B-Instruct
    revision: main
  runtime:
    ref: nano-vllm
    maxModelLen: 4096
  resources:
    cpu: "4"
    memory: 16Gi
    gpu:
      count: 1
      vendor: nvidia
  activation:
    desiredState: Inactive
    whenFull: Queue
  scaling:
    minReplicas: 0
    maxReplicas: 1
  routing:
    enabled: true
  cache:
    enabled: true
    type: nodeLocal
    size: 50Gi
    path: /var/lib/inferops/models
```

```bash
kubectl apply -f qwen.yaml
```

**Option B: Python SDK**

```python
import inferops

app = inferops.App("quickstart")

@app.model(name="qwen-chat", model="Qwen/Qwen2.5-0.5B-Instruct")
class QwenChat:
    pass
```

```bash
inferops deploy app.py
```

**CPU-only**

```python
@app.model(
    name="cpu-smollm",
    engine="llama-cpp",
    model="jc-builds/SmolLM2-135M-Instruct-Q4_K_M-GGUF",
    gpu=None,
    cpu="4",
    memory="2Gi",
    max_model_len=512,
)
class CPUSmolLM:
    pass
```

```bash
inferops deploy cpu_app.py
```

## Activate and watch

```bash
inferops activate qwen-chat
inferops status qwen-chat --watch
```

| Phase | Meaning |
| --- | --- |
| `Cached` | Ready to activate, no GPU used |
| `WaitingForGPU` | No free GPU; queued |
| `Activating` | Runtime pod starting |
| `Active` | Ready for traffic |
| `Failed` | Check `inferops logs qwen-chat` |

## Call the model

```bash
curl -X POST http://<gateway-host>/models/qwen-chat/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen-chat","messages":[{"role":"user","content":"Hello"}]}'
```

Or with the Python client:

```python
client = inferops.Client(base_url="http://<gateway-host>")
resp = client.chat.completions.create(
    model="qwen-chat",
    messages=[{"role": "user", "content": "Hello"}],
)
```

## Deactivate or delete

```bash
# Stop runtime, release GPU, keep cache
inferops deactivate qwen-chat

# Remove deployment
inferops delete qwen-chat

# Remove cache files when no deployment uses them
inferops cache list
inferops cache delete <cache-name>
```

## Next steps

- [Homelab setup details](homelab.md)
- [CLI reference](cli.md)
- [SDK reference](sdk.md)
- [CRD fields](crds.md)
