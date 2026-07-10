# GPU Scheduling

InferOps reserves whole Kubernetes extended resources before creating a runtime
Deployment. Kubernetes and the vendor device plugin still perform the final
physical device assignment.

For a request with `vendor: nvidia`, the scheduler uses `nvidia.com/gpu`.
Other vendors map to `<vendor>.com/gpu`. A runtime pod receives equal requests
and limits for that resource.

## Eligibility and ordering

A candidate node must:

- be `Ready` and schedulable;
- match `scheduling.gpuNodeSelector`;
- advertise enough allocatable vendor GPU resources;
- match `resources.gpu.type` through `scheduling.gpuTypeLabel` when a type is
  requested; and
- contain the deployment's verified node-local cache.

Reservations include `Activating` and `Active` ModelDeployments. The controller
also inspects managed runtime Deployments so a status-write interruption cannot
make an existing GPU workload invisible. Reconciliation is serialized to keep
the read/reserve/create/status sequence from oversubscribing slots.

Candidates are ordered by ready cache locality, unreserved capacity, then node
name. With node-local caches, locality is a hard constraint because mounting a
path from another node would expose unrelated or incomplete data.

## Full-capacity behavior

- `Queue` sets `WaitingForGPU` and retries through controller backoff and Node
  watches. It does not create an unschedulable runtime Deployment.
- `Reject` sets `Failed` with reason `InsufficientGPUCapacity`.
- `ReplaceOldest` and `ReplaceLowestPriority` opt into deterministic
  single-GPU replacement. The incoming cache is prepared first, the selected
  runtime is drained, and traffic switches only after the incoming runtime is
  ready. A failed activation triggers a visible attempt to restore the prior
  runtime.

`ReplaceLowestPriority` only displaces a workload with a strictly lower
priority. Replacement candidates are namespace-scoped so one tenant cannot
evict another tenant's workload. Both workloads must use one replica and one
GPU of the same vendor resource on the cache node. Because the old runtime
must release the only GPU before the new runtime can start, single-GPU
replacement has unavoidable downtime. `Queue` and `Reject` never enter this
workflow.
While the old runtime drains, the incoming deployment's `assignedNode` is a
logical handoff reservation and `GPUAssigned` remains `Unknown`; this prevents
another queued workload from taking the slot without claiming that Kubernetes
has already assigned a physical device. The same reservation remains in force
during rollback and is consumable only by the displaced deployment being
restored.

InferOps records the selected node in `status.assignedNode`. It does not invent
physical GPU UUIDs; `status.assignedGPUs` remains empty unless a trusted
runtime/device-plugin integration reports actual assignments.

## Helm configuration

```yaml
scheduling:
  gpuNodeSelector:
    inferops.dev/gpu-node: "true"
  gpuTypeLabel: inferops.dev/gpu-type
metrics:
  port: 8080
```

Label typed nodes explicitly:

```bash
kubectl label node gpu-node-1 inferops.dev/gpu-node=true
kubectl label node gpu-node-1 inferops.dev/gpu-type=a100
```

Do not enable device-plugin sharing or time slicing when relying on the
exclusive whole-slot model.

## Rollout capacity validation

`Rolling`, `BlueGreen`, and `Canary` runtime rollouts are blocked unless the
cache-local node has enough compatible slots for the old runtime plus the
configured rollout surge. `Queue` leaves the current runtime serving and
reports `RolloutWaitingForCapacity`; `Reject` leaves it serving and reports
`RolloutRejected`.

Single-GPU replacement is not an implicit fallback for advanced rollouts. Use
`activation.whenFull: ReplaceOldest` or `ReplaceLowestPriority` on an explicit
replacement deployment when downtime-with-rollback is acceptable.

The automated acceptance tests simulate multi-GPU capacity with Kubernetes
Node allocatable resources. Hardware-only behavior that still needs release
qualification includes vendor device-plugin failures, physical GPU health
faults, and real multi-GPU topology or NUMA constraints.
