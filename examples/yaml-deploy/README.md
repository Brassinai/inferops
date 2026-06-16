# YAML Deploy

Apply `modeldeployment.yaml` to declare and cache an inactive model using the
default nano-vLLM runtime. Set `spec.runtime.ref` to another registered runtime,
such as `vllm` or `sglang`, to select it. Activation is a separate explicit
operation.

`modeldeployment-cpu.yaml` shows a CPU-only vLLM deployment. CPU-only
deployments omit `spec.resources.gpu` and GPU-only runtime tuning, while
retaining activation, scaling, routing, caching, and lifecycle behavior. They
must specify CPU and memory, and the referenced runtime image must include CPU
support.
