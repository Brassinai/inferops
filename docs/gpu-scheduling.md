# GPU Scheduling

InferOps reserves whole Kubernetes extended resources. Kubernetes and the
vendor device plugin assign physical devices.

## Eligibility

A node must be:

- `Ready` and schedulable;
- matching `scheduling.gpuNodeSelector`;
- advertising enough allocatable vendor GPUs;
- matching `resources.gpu.type` through `scheduling.gpuTypeLabel` when typed;
- holding the deployment's ready node-local cache.

Reservations include `Activating` and `Active` workloads. The controller also
reads managed runtime Deployments so a missed status write cannot hide a
running workload.

Candidates are ordered by cache locality, unreserved capacity, then node name.

## Full-capacity behavior

| Policy | Behavior |
| --- | --- |
| `Queue` | Wait for a slot (default) |
| `Reject` | Fail with `InsufficientGPUCapacity` |
| `ReplaceOldest` | Evict oldest compatible single-GPU deployment |
| `ReplaceLowestPriority` | Evict lowest-priority compatible single-GPU deployment |

Single-GPU replacement has downtime. On failure, the operator tries to restore
the previous runtime.

## Helm configuration

```yaml
scheduling:
  gpuNodeSelector:
    inferops.dev/gpu-node: "true"
  gpuTypeLabel: inferops.dev/gpu-type
```

Label nodes:

```bash
kubectl label node gpu-node-1 inferops.dev/gpu-node=true
kubectl label node gpu-node-1 inferops.dev/gpu-type=a100
```

Do not enable device-plugin sharing or time slicing.

## Rollout capacity

`Rolling`, `BlueGreen`, and `Canary` rollouts are blocked unless the cache node
has enough compatible slots for old plus surge replicas.
