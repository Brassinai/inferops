# InferOps Runtime Chart

The default values render a GPU runtime with one `nvidia.com/gpu` request and
limit. To render a CPU-only runtime, use the checked-in CPU profile and select
a runtime image that supports CPU inference:

```bash
helm template inferops-runtime-cpu . \
  --values values-cpu.yaml \
  --set image.repository=example/cpu-runtime \
  --set image.tag=latest
```

The CPU profile retains the Service, probes, resource requests and limits,
drain timeout, and runtime lifecycle settings. It removes the GPU extended
resource and GPU-only environment variables. It enables fake mode by default
so the chart can be exercised without accelerator dependencies; choose a real
CPU-compatible runtime image and set `runtime.fakeMode=false` for inference.

The standalone chart mounts the prepared node-local cache from
`cache.hostPath` at `model.path`. The directory must already exist on the node,
and callers are responsible for constraining the pod to that node. The
operator will own that cache-placement decision when reconciliation is
implemented.

Readiness and startup probes use `/readiness`; liveness uses `/health`. Set
`metrics.prometheusAnnotations=true` to annotate the pod for scraping
`/metrics` on port `8000`.

The configured `runtime.terminationGracePeriodSeconds` must exceed
`runtime.drainTimeout`. The defaults provide a 30-second margin.
