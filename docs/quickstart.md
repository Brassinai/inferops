# Quickstart

Deploy an OpenAI-compatible model on a single-GPU k3s homelab.

## Prerequisites

- Linux node with one NVIDIA GPU (8GB+ VRAM for the 0.5B example)
- [k3s with NVIDIA container runtime](https://docs.k3s.io/advanced#nvidia-container-runtime-support)
- [NVIDIA Device Plugin](https://github.com/NVIDIA/k8s-device-plugin)
- `kubectl` and `helm` 3.15+
- Hugging Face token as a Kubernetes Secret (if pulling gated models)

## Install InferOps

```bash
helm install inferops-operator ./deploy/helm/inferops-operator \
  --namespace inferops-system --create-namespace

helm install inferops-gateway ./deploy/helm/inferops-gateway \
  --namespace inferops-system
```

Verify:

```bash
kubectl get pods -n inferops-system
```

## Deploy a model

### Option A: raw YAML

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
    path: /models/qwen-chat
    openAICompatible: true
  cache:
    enabled: true
    type: nodeLocal
    size: 50Gi
    path: /var/lib/inferops/models
```

```bash
kubectl apply -f qwen.yaml
```

### Option B: Python SDK

```python
# app.py
import inferops

app = inferops.App("quickstart")

@app.model(name="qwen-chat", model="Qwen/Qwen2.5-0.5B-Instruct")
class QwenChat:
    pass
```

```bash
inferops deploy app.py
```

## Activate

```bash
inferops activate qwen-chat
```

Watch:

```bash
inferops status qwen-chat --watch
```

Phases:

| Phase | Meaning |
| --- | --- |
| `Cached` | Ready to activate, no GPU used |
| `WaitingForGPU` | No free GPU slot; stays queued |
| `Activating` | Runtime pod starting |
| `Active` | Ready for traffic |
| `Failed` | Check `inferops logs qwen-chat` |

## Call the model

Via gateway:

```bash
curl -X POST http://<gateway-host>/models/qwen-chat/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen-chat","messages":[{"role":"user","content":"Hello"}]}'
```

Via Python SDK client:

```python
client = inferops.Client(base_url="http://<gateway-host>")
resp = client.chat.completions.create(
    model="qwen-chat",
    messages=[{"role": "user", "content": "Hello"}],
)
```

## Deactivate

```bash
inferops deactivate qwen-chat
```

GPU is released. Cache is preserved. Re-activation skips re-download.

## Delete

```bash
inferops delete qwen-chat
```

Delete removes the deployment and Service. Cache remains until you run:

```bash
inferops cache delete qwen-chat
```

## Next steps

- [Homelab setup details](homelab.md)
- [CLI reference](cli.md)
- [SDK reference](sdk.md)
- [CRD field reference](crds.md)
