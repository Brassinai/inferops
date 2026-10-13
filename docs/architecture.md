# Architecture

InferOps is a self-hosted, Kubernetes-native platform for OpenAI-compatible
inference runtimes. Control plane and data plane stay inside your cluster.

## Data flow

```
Python SDK / CLI / YAML
        |
        v
Kubernetes API  ->  ModelDeployment CRD
        |
        v
InferOps operator  ->  ModelCache + runtime workload + Service
        |
        v
InferOps gateway  ->  runtime OpenAI API
```

## Core objects

| Object | Purpose |
| --- | --- |
| `ModelDeployment` | Your model endpoint declaration |
| `ModelRuntime` | Reusable runtime definition (nano-vLLM, vLLM, SGLang, llama.cpp) |
| `ModelCache` | Persisted model revision on a node |
| Gateway | Stable client-facing route |

## Lifecycle

```
deploy     -> declare model + prepare cache + create Service
activate   -> acquire compute + start runtime + route traffic
deactivate -> drain traffic + stop runtime + release compute + keep cache
```

## Stable names

For a `ModelDeployment` named `<name>` in namespace `<namespace>`:

| Resource | Name |
| --- | --- |
| Runtime Service | `<name>-runtime` |
| Gateway route | `/models/<name>/v1/...` |

The gateway strips `/models/<name>` and forwards `/v1/...` to the runtime
Service on port `8000`.

## Runtime contract

A conforming runtime declares:

- Image
- Port (default `8000`)
- Health path
- Readiness path
- Metrics path
- Command, args, and non-secret env

The operator translates `ModelDeployment` into that container contract.

## nano-vLLM defaults

| Contract | Value |
| --- | --- |
| Container name | `runtime` |
| Port | `8000` |
| Readiness | `GET /health` |
| Liveness | `GET /health` |
| Metrics | `GET /metrics` |

Environment variables passed to the pod:

| Variable | Meaning |
| --- | --- |
| `MODEL_REPO` | Model name for the OpenAI API |
| `MODEL_PATH` | Node-local prepared model path |
| `MAX_MODEL_LEN` | Max context length |
| `TENSOR_PARALLEL_SIZE` | Whole-GPU tensor parallel count (GPU only) |
| `GPU_MEMORY_UTILIZATION` | GPU memory target (GPU only) |
| `PORT` | Listen port, always `8000` |

## Scheduling rules

- GPU requests are whole devices. Requests equal limits.
- Activation scheduler reads node allocatable GPUs and reservations from
  `Activating`/`Active` workloads.
- Node-local cache affinity is a hard constraint.
- CPU-only workloads use normal Kubernetes CPU/memory scheduling.

## Full-capacity behavior

| Policy | Behavior |
| --- | --- |
| `Queue` | Wait for a free slot (default) |
| `Reject` | Fail immediately |
| `ReplaceOldest` | Evict oldest active model |
| `ReplaceLowestPriority` | Evict lowest-priority active model |

Single-GPU replacement has downtime. On failure, the operator tries to
restore the previous runtime.

## Non-goals

MVP-001 does not include GPU slicing, a hosted control plane, automatic
eviction, advanced autoscaling, or OpenTelemetry export.
