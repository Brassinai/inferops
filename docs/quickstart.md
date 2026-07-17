# Quickstart

Deploy an OpenAI-compatible model on a k3s homelab using either an NVIDIA GPU
or the portable llama.cpp CPU path.

## Prerequisites

- Linux node; an NVIDIA GPU is required only for nano-vLLM, vLLM, or SGLang
- [k3s with NVIDIA container runtime](https://docs.k3s.io/advanced#nvidia-container-runtime-support)
  and the [NVIDIA Device Plugin](https://github.com/NVIDIA/k8s-device-plugin)
  for GPU deployments
- `kubectl` and `helm` 3.15+
- Hugging Face token as a Kubernetes Secret (if pulling gated models)

## Install InferOps

The homelab profile installs or upgrades both charts, the CRDs, and the
packaged `nano-vllm`, `vllm`, `sglang`, and `llama-cpp` runtime definitions:

```bash
inferops install --profile homelab \
  --cache-path /var/lib/inferops/models \
  --cache-capacity 500Gi
```

The default compute profile is CPU-safe for the llama.cpp path. For NVIDIA GPU
runtime deployments, install with:

```bash
inferops install --profile homelab \
  --compute-profile nvidia-gpu \
  --cache-path /var/lib/inferops/models \
  --cache-capacity 500Gi
```

The equivalent lower-level commands are:

```bash
kubectl apply --server-side -f ./deploy/manifests/crds

helm upgrade --install inferops-operator ./deploy/helm/inferops-operator \
  --namespace default \
  --values ./deploy/helm/inferops-operator/values-homelab.yaml

helm upgrade --install inferops-gateway ./deploy/helm/inferops-gateway \
  --namespace default \
  --values ./deploy/helm/inferops-gateway/values-homelab.yaml
```

Verify:

```bash
kubectl get pods
kubectl get modelruntime
inferops doctor
inferops gpu list
```

`inferops doctor` may create temporary read-only cache probe Jobs. It removes
them after collecting disk-space results; active deadlines and TTL cleanup
cover interrupted runs. Probe Jobs never create or modify the host cache
directory.

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

### CPU-only option

Select the installed `llama-cpp` runtime and omit the GPU request:

```python
# cpu_app.py
import inferops

app = inferops.App("cpu-quickstart")

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

The equivalent Kubernetes manifest is
[`examples/yaml-deploy/modeldeployment-cpu.yaml`](../examples/yaml-deploy/modeldeployment-cpu.yaml).

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

For the full single-node GPU acceptance workflow with recorded timings and
defects, run
[`scripts/homelab_acceptance.py`](../scripts/homelab_acceptance.py) as described
in [Homelab setup details](homelab.md).

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
