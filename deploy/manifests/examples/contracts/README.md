# Shared Contract Fixtures

These manifests are dependency-free API fixtures for the control-plane,
runtime/gateway, and developer-experience lanes.

- `modelruntime-nano-vllm.yaml` defines the default packaged runtime.
- `modelruntime-vllm.yaml`, `modelruntime-sglang.yaml`, and
  `modelruntime-llama-cpp.yaml` demonstrate that the shared runtime contract is
  not tied to the default engine. llama.cpp provides the CPU-friendly fixture.
- `modelcache-ready.yaml` demonstrates a ready node-local cache.
- `modeldeployment-*.yaml` demonstrate inactive, waiting, active, and failed
  API responses.

Status-bearing fixtures represent objects returned by the Kubernetes API.
Tests may parse them directly without a live cluster. When applying examples to
a cluster, submit `metadata` and `spec`; controllers own `status`.

The fixture image references demonstrate API shape and are not evidence that a
tag has been published. A deployable `defaultImage` must be produced by the
engine's release pipeline and include the corresponding thin InferOps adapter.
