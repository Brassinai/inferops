# CRD Contracts

The month-one API group is `inference.inferops.dev/v1alpha1`. All three
resources are namespaced. Specs express desired state; controllers alone write
status. Unknown fields are rejected by the checked-in schemas.

## Common Status Rules

Every status includes `observedGeneration`, `phase`, and Kubernetes-style
`conditions`. Conditions use:

```yaml
- type: Ready
  status: "True" # True, False, or Unknown
  observedGeneration: 3
  lastTransitionTime: "2026-06-11T09:00:00Z"
  reason: RuntimeReady
  message: Runtime is accepting traffic.
```

Controllers preserve the last transition time while a condition's status is
unchanged. `reason` is a stable machine-readable value; `message` is for
operators. A stale status is identifiable when `observedGeneration` is behind
`metadata.generation`.

## ModelDeployment

`ModelDeployment` declares exactly one model endpoint.

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
    image: ghcr.io/your-org/inferops-runtime:nano-vllm
    dtype: bfloat16
    maxModelLen: 4096
    tensorParallelSize: 1
    gpuMemoryUtilization: 0.85
  resources:
    cpu: "8"
    memory: 32Gi
    gpu:
      count: 1
      vendor: nvidia
      type: ""
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
  secrets:
    huggingFaceTokenSecretName: hf-token
```

Required month-one fields are `spec.model.repo` and `spec.runtime.ref`.
Defaults are `model.source=huggingface`, `model.revision=main`,
`activation.desiredState=Inactive`, `activation.whenFull=Queue`,
`activation.drainTimeout=5m`, `scaling.minReplicas=0`,
`scaling.maxReplicas=1`, `routing.enabled=true`, and
`routing.openAICompatible=true`.

`resources.cpu` and `resources.memory` describe ordinary Kubernetes compute
requirements. `resources.gpu` is optional; omitting it creates a CPU-only
runtime workload with no GPU extended-resource request. CPU-only deployments
must specify both `resources.cpu` and `resources.memory`, and must omit
`runtime.tensorParallelSize` and `runtime.gpuMemoryUtilization`. When present,
`resources.gpu.count` is required and must be at least one, and
`resources.gpu.vendor` defaults to `nvidia`. Runtime compatibility with CPU or
the requested GPU vendor is determined by the selected `ModelRuntime`.
Existing GPU manifests should include the GPU block explicitly rather than
relying on admission defaults.

`spec.runtime.ref` references a `ModelRuntime`; it is not restricted to one
engine. The standard runtime names are `nano-vllm`, `vllm`, `sglang`, and
`llama-cpp`. The Python SDK defaults the reference to `nano-vllm`; direct YAML
keeps it explicit.

`activation.desiredState` is `Inactive` or `Active`.
`activation.whenFull` is `Queue`, `Reject`, `ReplaceOldest`, or
`ReplaceLowestPriority`. Replacement modes are the only policies that permit
eviction. Month-one scaling is limited to explicit replica bounds; advanced
autoscaling behavior is not part of this contract.

Observed phases:

| Phase | Meaning |
| --- | --- |
| `Pending` | Accepted but not yet reconciled |
| `Downloading` | Model cache is being prepared |
| `Cached` | Cache is ready and desired state is inactive |
| `WaitingForCapacity` | Active is desired but compatible non-GPU compute capacity is unavailable |
| `WaitingForGPU` | Active is desired but compatible capacity is unavailable |
| `Activating` | Runtime is starting or loading |
| `Active` | Runtime is ready and gateway routing is enabled |
| `Draining` | New traffic is stopped while in-flight requests finish |
| `Deactivating` | Runtime is stopping and releasing capacity |
| `Failed` | Reconciliation cannot currently make progress |

Standard condition types are `Ready`, `CacheReady`, `RuntimeReady`,
`RoutingReady`, and `Degraded`. GPU deployments also report `GPUAssigned`.

Status also reports `endpoint`, `serviceName`, `assignedNode`,
`assignedGPUs`, cache summary, desired/ready replicas, and loaded model state.

## ModelRuntime

`ModelRuntime` freezes a reusable runtime protocol and container defaults.
InferOps is designed for nano-vLLM, vLLM, SGLang, and llama.cpp, and permits
additional conforming runtimes. The default runtime object is:

```yaml
apiVersion: inference.inferops.dev/v1alpha1
kind: ModelRuntime
metadata:
  name: nano-vllm
spec:
  engine: nano-vllm
  protocol: openai
  defaultImage: ghcr.io/your-org/inferops-runtime:nano-vllm
  port: 8000
  healthPath: /health
  readinessPath: /health
  metricsPath: /metrics
```

Required fields are `engine`, `protocol`, `defaultImage`, `port`, and
`healthPath`. Optional `readinessPath`, `metricsPath`, `command`, `args`, and
non-secret `env` support custom runtimes. When `readinessPath` is omitted, the
operator falls back to `healthPath` for compatibility. The packaged nano-vLLM
and vLLM runtimes use `/health`; SGLang uses `/health_generate` for readiness.
`defaultImage` identifies a released runtime image that already contains the
engine server plus any thin InferOps CLI adapter; the operator does not build
engine images.
Drain state remains owned by the operator and gateway rather than an
InferOps-managed engine server. `engine` is intentionally open-ended.
`protocol` describes the gateway-facing protocol and is `openai` for
nano-vLLM, vLLM, SGLang, and llama.cpp.
Secret values belong in referenced Secrets, never `spec.env`.

Observed phases are `Pending`, `Ready`, `Unavailable`, and `Failed`. Standard
condition types are `Ready` and `Valid`.

The checked-in contract fixtures include `nano-vllm`, `vllm`, `sglang`, and
`llama-cpp` runtime objects. A `Ready` fixture proves shape compatibility for
lane tests; runtime conformance and image release validation remain
implementation work.

## ModelCache

`ModelCache` tracks one model revision at one persisted location:

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

Required fields are `modelRepo`, `storage.type`, `storage.size`, and
`storage.path`. `nodeLocal` caches are not portable; `storage.nodeName` is
selected or confirmed by the cache controller.

Observed phases are `Pending`, `Downloading`, `Ready`, and `Failed`. Standard
condition types are `Ready`, `Downloaded`, `Verified`, and `Degraded`. Status
reports the resolved revision, checksum, node, path, size, last-used time, and
failure details through conditions.

## Fixtures

`deploy/manifests/examples/contracts/` contains dependency-free examples for
the shared runtime and cache plus inactive, waiting, active, and failed
`ModelDeployment` states. They are contract fixtures for operator tests,
gateway route tests, and SDK/CLI parsing tests. Status-bearing fixtures model
API responses; clients creating objects should submit only `metadata` and
`spec`.

## Compatibility

The v1alpha1 shapes are frozen for month one. Changes should be additive where
possible. Renaming/removing fields, changing defaults, phases, conditions,
Service naming, routes, or runtime behavior requires review from every lane
owner and synchronized updates to schemas, fixtures, docs, SDK, and CLI.
