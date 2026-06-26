# Architecture

InferOps is a self-hosted, Kubernetes-native orchestration platform for
OpenAI-compatible inference runtimes. The runtime abstraction targets
nano-vLLM, vLLM, and SGLang. `nano-vllm` is the default runtime and first
packaged integration, not a platform-wide restriction. Both the control plane
and data plane remain inside the user's cluster.

```txt
Python SDK / CLI / YAML
        -> Kubernetes API
        -> ModelDeployment CRD
        -> InferOps operator
        -> ModelCache + runtime workload + stable Service
        -> InferOps gateway
        -> selected runtime's OpenAI-compatible API
```

## Month-One Ownership

- `ModelDeployment` is the user-facing declaration for one model endpoint. The
  operator owns its cache preparation, activation lifecycle, workload, stable
  Service, and status.
- `ModelRuntime` is a reusable runtime definition. A deployment selects
  `nano-vllm`, `vllm`, `sglang`, or another conforming runtime by reference.
  InferOps defaults to `nano-vllm`.
- `ModelCache` records one persisted model revision and its node-local
  location. Deactivating a model does not delete its cache.
- The gateway owns the stable client-facing endpoint and sends traffic only to
  an `Active`, non-draining runtime.
- The SDK and CLI produce and consume the same CRD shapes documented in
  [crds.md](crds.md). They do not call a hosted InferOps API.

Deployment and activation are separate:

```txt
deploy     = declare model + prepare cache + create stable Service identity
activate   = acquire compute + start runtime + wait for readiness + route
deactivate = stop routing + drain + stop runtime + release compute + keep cache
```

Creating a `ModelDeployment` defaults to `Inactive` and must never evict an
active model. Replacement policies are explicit opt-ins.

CPU-only workloads omit `spec.resources.gpu` and run without a GPU
extended-resource request. GPU workloads explicitly include
`spec.resources.gpu`. The selected `ModelRuntime` is responsible for providing
an image and arguments compatible with the requested compute. CPU-only is a
compute selection, not a reduced feature tier: caching, activation,
deactivation, scaling, routing, health checks, metrics, draining, and stable
endpoint behavior remain the same. GPU scheduling, GPU assignment status, and
GPU-specific runtime settings apply only to GPU workloads.

## Stable Names And Routes

For a `ModelDeployment` named `<name>` in namespace `<namespace>`:

- The operator-managed runtime Service is always `<name>-runtime` in the same
  namespace. Rollouts and pod replacements must not change this name.
- The gateway route prefix defaults to `/models/<name>`.
- OpenAI-compatible requests use `/models/<name>/v1/...`; the gateway removes
  `/models/<name>` and forwards `/v1/...` to `<name>-runtime:8000`.
- `status.serviceName` and `status.endpoint` report the resolved names. The
  endpoint remains stable while the model is inactive, waiting, or failed,
  although requests may return an unavailable response.

`spec.routing.path` may override the route prefix, but the default convention
is the interoperability contract all lanes must support.

## Runtime Abstraction

The operator and gateway depend on the `ModelRuntime` contract rather than
hard-coding an engine. A conforming runtime declares its image, protocol, port,
health path, metrics path, command, arguments, and non-secret environment.
Runtime-specific adapters translate a `ModelDeployment` into that container
contract.

The first packaged adapter is nano-vLLM. vLLM and SGLang adapters must use the
same stable Service, gateway route, readiness, drain, status, and lifecycle
contracts while retaining engine-specific images, arguments, and environment.

## Default Nano-vLLM Contract

When `spec.runtime.ref` is `nano-vllm`, the operator starts the default
nano-vLLM container with:

| Contract | Value |
| --- | --- |
| Container name | `runtime` |
| HTTP port | `8000`, named `http` |
| Readiness | `GET /readiness` |
| Liveness | `GET /health` |
| Metrics | `GET /metrics` |
| OpenAI API | `/v1/chat/completions`, `/v1/completions` |

Required environment:

| Variable | Meaning |
| --- | --- |
| `MODEL_REPO` | Source repository identifier |
| `MODEL_REVISION` | Immutable or named source revision |
| `MODEL_PATH` | Prepared node-local model path |
| `MAX_MODEL_LEN` | Maximum model context length |
| `TENSOR_PARALLEL_SIZE` | Optional whole-GPU tensor parallel count; omitted for CPU-only workloads |
| `GPU_MEMORY_UTILIZATION` | Optional runtime GPU memory target; omitted for CPU-only workloads |
| `PORT` | HTTP listen port, always `8000` in month one |
| `INFEROPS_DRAIN_TIMEOUT` | Maximum time allowed for in-flight requests |

`HF_TOKEN` may be populated from the referenced Kubernetes Secret. It must
never be written into a CRD status, ConfigMap, log, or checked-in example.

`GET /health` reports process liveness. `GET /readiness` returns success only
after the model is loaded and while the runtime accepts new traffic. During
deactivation or replacement, the operator first marks the deployment
`Draining`; the gateway stops new requests; the runtime fails readiness,
handles `SIGTERM`, waits for in-flight requests up to
`activation.drainTimeout`, then exits. The pod termination grace period must
be longer than the configured drain timeout.

## Scheduling And Failure Rules

- GPU requests, when present, are whole devices. Requests and limits must be equal.
- CPU-only workloads rely on ordinary Kubernetes CPU and memory scheduling.
- `Queue` is the default full-capacity behavior. `ReplaceOldest` and
  `ReplaceLowestPriority` are explicit eviction permissions.
- Kubernetes and the vendor device plugin choose physical devices. InferOps
  records observed assignments but does not promise GPU UUID selection.
- A request for `Active` may remain `WaitingForCapacity` or `WaitingForGPU`.
- A runtime is routed only after readiness succeeds.
- Cache and activation failures are visible through phase and conditions.

## Explicit Non-Goals

MVP-001 does not define or promise GPU slicing, a hosted InferOps control
plane, automatic eviction without an explicit replacement policy, advanced
autoscaling, or a dashboard. These non-goals do not exclude vLLM or SGLang;
multi-runtime support is part of the platform direction, with nano-vLLM as the
initial packaged implementation.

## Contract Governance

The files under `operator/api/v1alpha1`, checked-in CRDs, contract examples,
and this documentation are shared contracts. Changes after MVP-001 require
review from all three lane owners: control plane, runtime/gateway, and developer
experience. `.github/CODEOWNERS` records the current owners; repository branch
protection must require approvals from each owner for enforcement.
