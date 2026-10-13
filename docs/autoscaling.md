# Autoscaling

InferOps scales `ModelDeployment` replicas conservatively without
overcommitting GPU capacity.

## Supported fields

```yaml
spec:
  scaling:
    minReplicas: 0
    maxReplicas: 1
    targetPendingRequests: 4
    idleTimeout: 10m
```

| Field | Behavior |
| --- | --- |
| `minReplicas` / `maxReplicas` | Hard bounds |
| `targetPendingRequests` | Scale up when pending requests exceed threshold |
| `idleTimeout` | Scale active deployment to zero after idle period |

## Signals

For vLLM-compatible runtimes the operator reads
`vllm:num_requests_waiting` and `vllm:num_requests_running` from the runtime
metrics endpoint. If metrics are unavailable, it keeps the safe floor and
sets `ScalingMetricsUnavailable`.

## GPU capacity cap

GPU workloads cannot exceed compatible free whole-GPU slots on the cache node.
When capped, `status.scaling.capacityLimited` is `true`.

## External integrations

HPA and KEDA are optional but must respect the same capacity checks.
Scale-from-zero requires an external activation signal because a zero-replica
runtime has no metrics endpoint.
