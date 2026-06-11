# YAML Deploy

Apply `modeldeployment.yaml` to declare and cache an inactive model using the
default nano-vLLM runtime. Set `spec.runtime.ref` to another registered runtime,
such as `vllm` or `sglang`, to select it. Activation is a separate explicit
operation.
