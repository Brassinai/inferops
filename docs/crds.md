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
  secrets:
    huggingFaceTokenSecretName: hf-token
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
| `cache.enabled` | `true` |
| `cache.type` | `nodeLocal` |
| `cache.size` | Operator `cache.defaultSize` (`100Gi` by default) |
| `cache.path` | Operator `cache.root` |

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

Replacement is never implicit. `ReplaceOldest` selects the active compatible
single-GPU deployment in the same namespace with the earliest `Ready`
transition.
`ReplaceLowestPriority` selects the lowest-priority compatible deployment only
when its priority is lower than the incoming deployment; age and name provide
deterministic tie breakers. Both policies require one replica requesting one
GPU. The incoming cache must be ready before the current runtime is drained.

Single-GPU replacement has unavoidable downtime: InferOps must release the
only GPU before it can start and verify the replacement runtime. If activation
fails or the runtime Deployment exceeds its explicit 10-minute progress
deadline, the operator attempts to restore the displaced runtime and reports
whether that rollback succeeded. `Queue` and `Reject` never replace a workload.

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

| Type | Meaning |
| --- | --- |
| `Ready` | Aggregate readiness of the deployment |
| `SpecValid` | Static and reconciliation-time validation passed |
| `RuntimeResolved` | Referenced `ModelRuntime` exists and produced an effective configuration |
| `SecretsReady` | Optional credential references are syntactically valid |
| `CacheReady` | Model cache is ready |
| `RoutingReady` | Gateway route is configured |
| `GPUAssigned` | Node-level whole-GPU capacity is reserved; this does not assert a physical GPU UUID |
| `RuntimeReady` | Managed runtime has its desired ready replicas |
| `ModelLoaded` | Runtime readiness indicates that the model can receive traffic |
| `Replacement` | Explicit replacement progress, success, or rollback outcome |

Stable `reason` values are machine-readable; `message` is for operators. `observedGeneration` must match `metadata.generation` for freshness.

`status.drainStartedAt` anchors the bounded drain grace period.
`status.replacement` records the replacement phase and UID-qualified target or
requester reference so a controller restart can safely resume the transaction.
Its `requestGeneration` prevents a destructive transaction from continuing
against a spec that changed after target selection.

### Validation reason codes

| Reason | Situation |
| --- | --- |
| `SpecInvalid` | Generic validation failure |
| `RuntimeNotFound` | `spec.runtime.ref` does not match an existing `ModelRuntime` |
| `SecretRequired` | A supplied Secret reference is not a valid Kubernetes name |
| `InvalidCachePath` | `spec.cache.path` is not under the operator's configured cache root |
| `InvalidDrainTimeout` | `spec.activation.drainTimeout` is not a positive duration |
| `CachePending` / `CacheDownloading` / `CacheVerified` | Cache lifecycle projection |
| `WaitingForGPU` | `Queue` is waiting for compatible whole-GPU capacity |
| `InsufficientGPUCapacity` | `Reject` found no compatible free slot |
| `InsufficientComputeCapacity` | The selected cache node cannot fit the aggregate runtime CPU or memory request in its allocatable capacity |
| `SchedulingConstraintsUnsatisfied` | The selected cache node does not match the requested labels or has an untolerated scheduling taint |
| `RuntimeCreating` / `RuntimeReady` | Runtime Deployment lifecycle |
| `DrainStarted` / `DeactivationStarted` / `DrainComplete` | Bounded drain and runtime removal |
| `ReplacementStarted` / `ReplacementActivating` / `ReplacementSucceeded` | Successful explicit replacement |
| `ReplacementCanceled` | A new spec generation withdrew or changed an in-progress replacement request |
| `ReplacementActivationFailed` / `ReplacementRollbackStarted` / `ReplacementRolledBack` / `ReplacementRollbackFailed` | Failed activation and rollback outcome |

Hugging Face credentials are optional. Public repositories work without a
Secret; private repositories reference `secrets.huggingFaceTokenSecretName`,
whose `token` key is consumed only by the downloader Job.

### Runtime resolution

The operator resolves `spec.runtime.ref` in the `ModelDeployment` namespace and
produces an effective runtime configuration:

- `spec.runtime.image` overrides `ModelRuntime.spec.defaultImage`.
- `port` defaults to `8000`.
- `healthPath` defaults to `/health`.
- `readinessPath` defaults to `healthPath`.
- `metricsPath` defaults to `/metrics`.

The resolved image must include a tag or digest. The mutable `:latest` tag is
rejected, and SHA-256 digests must contain the complete 64-character digest.
The current CRD requires `ModelRuntime.spec.port` and `healthPath`; resolver
fallbacks preserve compatibility if older objects omit them.

Runtime lookup failures caused by API availability, authorization, or request
cancellation are returned for retry. Only an actual Kubernetes `NotFound`
result produces the stable `RuntimeNotFound` condition.

### Runtime scheduling and disruption protection

`spec.scheduling` adds constraints to the managed runtime pods. InferOps keeps
the model cache's node affinity authoritative and rejects a contradictory
hostname selector instead of creating a pod that can never schedule:

```yaml
spec:
  scheduling:
    nodeSelector:
      inferops.dev/pool: inference
    tolerations:
      - key: dedicated
        operator: Equal
        value: inference
        effect: NoSchedule
    topologySpreadConstraints:
      - maxSkew: 1
        topologyKey: topology.kubernetes.io/zone
        whenUnsatisfiable: ScheduleAnyway
  availability:
    podDisruptionBudget:
      enabled: true
      minAvailable: 1
```

The operator supplies the topology-spread label selector; users cannot broaden
it to unrelated pods. Before creating a runtime Deployment, the controller
checks node selectors, `NoSchedule`/`NoExecute` taints, and whether the
aggregate replica request fits the selected node's total allocatable CPU and
memory. Kubernetes scheduling remains authoritative for live free capacity.
Node selectors and tolerations are also copied to the generated `ModelCache`
so its downloader can reach the same constrained node. With a single
node-local cache copy, required cache affinity can limit the useful spread;
prefer `ScheduleAnyway` until cache copies exist on multiple topology domains.

An active runtime gets a `PodDisruptionBudget` by default. With one replica,
the default `minAvailable: 1` deliberately prevents voluntary eviction that
would cause downtime. Set `enabled: false` or an explicit `minAvailable` only
when that availability tradeoff is understood.

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

`protocol` is projected to runtime pods as the
`inferops.dev/runtime-protocol` annotation. Health and readiness paths become
Kubernetes probes. The metrics path becomes a `prometheus.io/path` annotation
alongside scrape and port annotations. Runtime images must still implement
those endpoints; use the [runtime conformance suite](runtime-conformance.md)
before publishing an adapter.

The only currently supported protocol value is `openai`. Unsupported values
are rejected during admission and runtime resolution rather than being routed
with accidental OpenAI semantics. Additional protocols require explicit
gateway behavior before they can be accepted.

Phases: `Pending`, `Ready`, `Unavailable`, `Failed`. The controller publishes a
`Ready` condition with stable `RuntimeValidated` or `RuntimeInvalid` reasons.

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
    nodeSelector:
      inferops.dev/pool: inference
    tolerations:
      - key: dedicated
        operator: Equal
        value: inference
        effect: NoSchedule
    path: /var/lib/inferops/models/qwen-chat-cache
  secretRef: hf-token
```

Required: `modelRepo`, `storage.type`, `storage.size`, `storage.path`.
`spec.storage.nodeName` optionally pins placement; otherwise the controller
selects a node and records it in `status.nodeName` and `status.nodeUID`.
`spec.storage.nodeSelector` and `spec.storage.tolerations` constrain both
placement and the downloader Job. Generated caches inherit these fields from
their `ModelDeployment`.

Phases: `Pending`, `Downloading`, `Ready`, `Failed`.

For node-local caches, `status.size` and `status.reservedSize` report the
configured reservation. They are not a live `du` measurement; observed disk
usage and pressure reporting are part of MVP-503.

The current reconciled source is Hugging Face and the current destination is
node-local storage under the operator's configured cache root. S3-compatible
sources, existing-cache copies, PVC/local-PV destinations, checksum policy,
multi-node distribution, and eviction remain part of MVP-503 and are not
accepted by the v1alpha1 schema yet.

### Conditions

| Type | Meaning |
| --- | --- |
| `SpecValid` | Spec passed validation |
| `Placed` | A destination node or volume was selected |
| `Downloaded` | Download Job finished successfully |
| `Verified` | Integrity verification passed |
| `Ready` | Cache is ready to mount |

Stable reason codes: `SpecValidated`, `SpecInvalid`, `Placed`,
`NoEligibleNode`, `PinnedNodeUnavailable`, `DownloadRunning`,
`DownloadSucceeded`, `DownloadFailed`, `Verified`, `CacheReady`,
`CacheFailed`, `InsufficientCapacity`, `PathConflict`, `NodeLost`,
`CacheIdentityChanged`, `SecretNotFound`, and `SecretKeyMissing`.

To retry a failed download, set `inferops.dev/retry` to a new non-empty token.
The controller records the token on the Job, so the same value causes exactly
one attempt and is safe across reconciles. Retry tokens do not replace a Ready
cache.

Fallback polling intervals are explicit chart values:
`cache.downloadRequeueInterval` while a Job is active and
`cache.pendingRequeueInterval` while placement or a referenced Secret is
unavailable. Job and Node watches normally reconcile sooner.

### Deletion

Deleting a `ModelDeployment` garbage-collects its owned Service, ConfigMap,
runtime Deployment, and `PodDisruptionBudget`. Its deterministic `ModelCache`
is deliberately not owned by the deployment and remains available for reuse.
Deleting a `ModelCache`
object also does not remove on-disk files. Explicit, retry-safe cache cleanup
will be added in a future release.

`status.lastUsedTime` changes only when a runtime begins or ends a real
consumer transition. Routine cache and deployment reconciliation does not
refresh it.

## Stable names and routes

For a `ModelDeployment` named `<name>` in namespace `<namespace>`:

- Runtime Service: `<name>-runtime`
- ModelCache: a label-safe name derived from namespace, deployment name, model
  repository, and revision
- ModelCache path: `<cache-root>/<namespace>/<derived-cache-name>`
- Gateway route: `/models/<name>/v1/...`
- Gateway strips `/models/<name>` and forwards `/v1/...` to the runtime Service on port `8000`

## Compatibility

v1alpha1 is frozen for month one. Additive changes only. Renaming fields, phases, conditions, or routes requires review from all lane owners.
