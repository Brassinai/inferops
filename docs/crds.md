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
  resources:
    cpu: "8"
    memory: 32Gi
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
    size: 100Gi
    path: /var/lib/inferops/models
```

## Required fields

| Field | Description |
| --- | --- |
| `spec.model.repo` | Model identifier, e.g. `Qwen/Qwen2.5-7B-Instruct` |
| `spec.runtime.ref` | `ModelRuntime` name: `nano-vllm`, `vllm`, `sglang`, `llama-cpp` |

## Defaults

| Field | Default |
| --- | --- |
| `model.source` | `huggingface` |
| `model.revision` | `main` |
| `activation.desiredState` | `Inactive` |
| `activation.whenFull` | `Queue` |
| `activation.drainTimeout` | `5m` |
| `scaling.minReplicas` | `0` |
| `scaling.maxReplicas` | `1` |
| `rollout.strategy` | `Recreate` |
| `routing.enabled` | `true` |
| `routing.openAICompatible` | `true` |
| `cache.enabled` | `true` |
| `cache.type` | `nodeLocal` |

`cache.enabled` must remain `true` in the current release.

## GPU rules

- Include `resources.gpu` for GPU workloads; omit for CPU-only.
- `resources.gpu.count` >= 1 when present.
- `resources.gpu.vendor` defaults to `nvidia`.
- `runtime.tensorParallelSize` and `runtime.gpuMemoryUtilization` apply only to
  GPU workloads.

## Activation policies

| Policy | Behavior |
| --- | --- |
| `Queue` | Wait for capacity (default) |
| `Reject` | Fail immediately |
| `ReplaceOldest` | Evict oldest active model |
| `ReplaceLowestPriority` | Evict lowest-priority active model |

## Phases

| Phase | Meaning |
| --- | --- |
| `Pending` | Accepted, not reconciled |
| `Downloading` | Cache download in progress |
| `Cached` | Cache ready, inactive |
| `WaitingForCapacity` | No CPU/memory capacity |
| `WaitingForGPU` | No free GPU slot |
| `Activating` | Runtime starting |
| `Active` | Ready, routed |
| `Draining` | Stopping new traffic |
| `Deactivating` | Runtime stopping |
| `Failed` | Unrecoverable error |

## Conditions

| Type | Meaning |
| --- | --- |
| `Ready` | Aggregate readiness |
| `SpecValid` | Validation passed |
| `RuntimeResolved` | Runtime exists and is valid |
| `CacheReady` | Cache is ready |
| `RoutingReady` | Gateway route configured |
| `GPUAssigned` | GPU capacity reserved |
| `RuntimeReady` | Runtime has desired ready replicas |
| `ModelLoaded` | Model can receive traffic |

## ModelRuntime

Reusable runtime definition:

```yaml
apiVersion: inference.inferops.dev/v1alpha1
kind: ModelRuntime
metadata:
  name: nano-vllm
spec:
  engine: nano-vllm
  protocol: openai
  defaultImage: ghcr.io/brassinai/inferops-runtime:nano-vllm
  port: 8000
  healthPath: /health
  readinessPath: /health
  metricsPath: /metrics
```

Required: `engine`, `protocol`, `defaultImage`, `port`, `healthPath`.

## ModelCache

Tracks one model artifact at one node location.

```yaml
apiVersion: inference.inferops.dev/v1alpha1
kind: ModelCache
metadata:
  name: model-<hash>-cache
spec:
  modelRepo: Qwen/Qwen2.5-7B-Instruct
  revision: main
  storage:
    type: nodeLocal
    size: 100Gi
    path: /var/lib/inferops/models/default/artifacts/<hash>
```

Phases: `Pending`, `Downloading`, `Ready`, `Failed`.

## Stable names

For `ModelDeployment` `<name>` in namespace `<namespace>`:

| Resource | Name/Path |
| --- | --- |
| Runtime Service | `<name>-runtime` |
| ModelCache | `model-<artifact-hash>-cache` |
| Cache path | `<cache-root>/<namespace>/artifacts/<artifact-hash>` |
| Gateway route | `/models/<name>/v1/...` |
