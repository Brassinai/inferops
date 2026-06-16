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
resource and GPU-only environment variables.
