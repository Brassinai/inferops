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

The CPU profile retains the Service, probes, resource requests and limits, and
runtime lifecycle settings. It removes the GPU extended resource and GPU-only
environment variables. The selected runtime image must support CPU inference.
The packaged `llama-cpp` adapter is the simplest CPU runtime for local
contract testing and consumes a GGUF file from the mounted model directory.
The vLLM CPU adapter is also available, but its upstream images are
architecture-specific and substantially larger. The regular vLLM and SGLang
images remain GPU images; SGLang's documented CPU backend requires supported
Intel Xeon AMX hardware.

The standalone chart mounts the prepared node-local cache from
`cache.hostPath` at `model.path`. The directory must already exist on the node,
and callers are responsible for constraining the pod to that node. The
operator will own that cache-placement decision when reconciliation is
implemented.

The generic chart defaults startup, readiness, and liveness to `/health`.
Set `runtime.readinessPath=/health_generate` for SGLang's stronger readiness
check. Set
`metrics.prometheusAnnotations=true` to annotate the pod for scraping
`runtime.metricsPath` on port `8000`.

If Prometheus Operator is installed, enable a standalone runtime
`ServiceMonitor` without relying on pod annotations:

```bash
helm template inferops-runtime . \
  --set metrics.serviceMonitor.enabled=true
```

The runtime chart keeps observability runtime-neutral: the scrape port is the
runtime Service's `http` port, and the scrape path is `runtime.metricsPath`.

`runtime.terminationGracePeriodSeconds` bounds the engine's native graceful
shutdown after the operator and gateway have stopped routing new requests.
