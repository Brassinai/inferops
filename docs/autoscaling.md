# Autoscaling

Autoscaling design should account for inference-specific signals, requested
CPU, memory, optional GPU capacity, queue depth, request concurrency, and safe
rollout behavior.

InferOps supports conservative controller-owned scaling on
`ModelDeployment.spec.scaling`:

- `minReplicas` and `maxReplicas` are hard bounds.
- `targetPendingRequests` uses runtime Prometheus metrics to scale up when
  pending requests are observed.
- `idleTimeout` can scale active deployments with `minReplicas: 0` down to zero
  after the runtime reports no pending or running requests for the timeout.

For vLLM-compatible runtimes the operator reads
`vllm:num_requests_waiting` and `vllm:num_requests_running` from the runtime
Service's configured metrics path. If metrics cannot be scraped or parsed, the
operator keeps the safe replica floor and records
`ScalingMetricsUnavailable` in `status.scaling.reason`.

GPU workloads are capped by compatible free whole-GPU slots before the runtime
Deployment is created or patched. The cap is visible through
`status.scaling.capacityLimited` and `status.scaling.reason`; the operator does
not overbook GPUs to chase queue depth.

When the operator chart NetworkPolicy is enabled, runtime metric scraping is
limited to `networkPolicy.runtimeMetricsPorts` and InferOps-managed runtime pod
labels. Add any custom `ModelRuntime.spec.port` used for metrics to that list.

Possible Kubernetes integrations include HPA and KEDA, but they must remain
optional until they can respect the same capacity checks. Scale-from-zero also
requires an external activation signal or spec update because a zero-replica
runtime has no metrics endpoint to scrape.
