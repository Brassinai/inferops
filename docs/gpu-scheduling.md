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
- `ReplaceOldest` and `ReplaceLowestPriority` set
  `ReplacementNotImplemented` until the explicit drain and rollback workflow is
  installed. No implicit eviction occurs.

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
