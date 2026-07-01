# CRD Reference

API group: `inference.inferops.dev/v1alpha1`. All resources are namespaced.

## Quick reference

```yaml
apiVersion: inference.inferops.dev/v1alpha1
kind: ModelDeployment
metadata:
  name: qwen-chat
spec:
  model:
    name: qwen-chat
    source: huggingface
    repo: Qwen/Qwen2.5-7B-Instruct
    revision: main
  runtime:
    ref: nano-vllm
    maxModelLen: 4096
    tensorParallelSize: 1
    gpuMemoryUtilization: 0.85
  resources:
    cpu: "8"
    memory: 32Gi
    gpu:
      count: 1
      vendor: nvidia
  activation:
    desiredState: Inactive
    whenFull: Queue
    priority: 50
    drainTimeout: 5m
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
    size: 100Gi
    path: /var/lib/inferops/models
```

## ModelDeployment

### Required fields

| Field | Description |
| --- | --- |
| `spec.model.repo` | Model identifier (e.g. `Qwen/Qwen2.5-7B-Instruct`) |
| `spec.runtime.ref` | `ModelRuntime` name: `nano-vllm`, `vllm`, `sglang`, `llama-cpp` |

### Defaults

| Field | Default |
| --- | --- |
| `model.source` | `huggingface` |
| `model.revision` | `main` |
| `activation.desiredState` | `Inactive` |
| `activation.whenFull` | `Queue` |
| `activation.drainTimeout` | `5m` |
| `scaling.minReplicas` | `0` |
| `scaling.maxReplicas` | `1` |
| `routing.enabled` | `true` |
| `routing.openAICompatible` | `true` |

### GPU rules

- Include `resources.gpu` for GPU workloads; omit for CPU-only.
- `resources.gpu.count` >= 1 when present.
- `resources.gpu.vendor` defaults to `nvidia`.
- `runtime.tensorParallelSize` and `runtime.gpuMemoryUtilization` apply only to GPU workloads.
- CPU-only workloads must specify `resources.cpu` and `resources.memory`.

### Activation policies

| Policy | Behavior |
| --- | --- |
| `Queue` | Wait for a free GPU slot (default) |
| `Reject` | Fail immediately if no slot |
| `ReplaceOldest` | Evict oldest active model |
| `ReplaceLowestPriority` | Evict lowest-priority active model |

### Phases

| Phase | Meaning |
| --- | --- |
| `Pending` | Accepted, not yet reconciled |
| `Downloading` | Cache download in progress |
| `Cached` | Cache ready, inactive |
| `WaitingForCapacity` | Active desired, no CPU/memory capacity |
| `WaitingForGPU` | Active desired, no free GPU slot |
| `Activating` | Runtime starting |
| `Active` | Ready, routed |
| `Draining` | Stopping new traffic, finishing in-flight |
| `Deactivating` | Runtime stopping, releasing capacity |
| `Failed` | Unrecoverable error; check conditions and logs |

### Conditions

Standard types: `Ready`, `CacheReady`, `RuntimeReady`, `RoutingReady`, `Degraded`. GPU workloads also report `GPUAssigned`.

Stable `reason` values are machine-readable; `message` is for operators. `observedGeneration` must match `metadata.generation` for freshness.

## ModelRuntime

Reusable runtime definition.

```yaml
apiVersion: inference.inferops.dev/v1alpha1
kind: ModelRuntime
metadata:
  name: nano-vllm
spec:
  engine: nano-vllm
  protocol: openai
  defaultImage: ghcr.io/inferops/inferops-runtime:nano-vllm
  port: 8000
  healthPath: /health
  readinessPath: /health
  metricsPath: /metrics
```

Required: `engine`, `protocol`, `defaultImage`, `port`, `healthPath`. Optional: `readinessPath`, `metricsPath`, `command`, `args`, `env`. Secret values belong in referenced Secrets, never in `spec.env`.

Phases: `Pending`, `Ready`, `Unavailable`, `Failed`.

## ModelCache

Tracks one model revision at one location.

```yaml
apiVersion: inference.inferops.dev/v1alpha1
kind: ModelCache
metadata:
  name: qwen-chat-cache
spec:
  modelRepo: Qwen/Qwen2.5-7B-Instruct
  revision: main
  storage:
    type: nodeLocal
    size: 100Gi
    nodeName: homelab-server
    path: /var/lib/inferops/models/qwen-chat
  secretRef: hf-token
```

Required: `modelRepo`, `storage.type`, `storage.size`, `storage.path`. `nodeName` is selected by the controller.

Phases: `Pending`, `Downloading`, `Ready`, `Failed`.

## Stable names and routes

For a `ModelDeployment` named `<name>` in namespace `<namespace>`:

- Runtime Service: `<name>-runtime`
- Gateway route: `/models/<name>/v1/...`
- Gateway strips `/models/<name>` and forwards `/v1/...` to the runtime Service on port `8000`

## Compatibility

v1alpha1 is frozen for month one. Additive changes only. Renaming fields, phases, conditions, or routes requires review from all lane owners.
