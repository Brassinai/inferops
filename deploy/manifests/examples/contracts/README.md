# Shared Contract Fixtures

These manifests are dependency-free API fixtures for the control-plane,
runtime/gateway, and developer-experience lanes.

- `modelruntime-nano-vllm.yaml` defines the default packaged runtime.
- `modelruntime-vllm.yaml` and `modelruntime-sglang.yaml` demonstrate that the
  shared runtime contract is not tied to the default engine.
- `modelcache-ready.yaml` demonstrates a ready node-local cache.
- `modeldeployment-*.yaml` demonstrate inactive, waiting, active, and failed
  API responses.

Status-bearing fixtures represent objects returned by the Kubernetes API.
Tests may parse them directly without a live cluster. When applying examples to
a cluster, submit `metadata` and `spec`; controllers own `status`.
