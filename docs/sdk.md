# SDK

The Python SDK provides a developer-friendly path for declaring model
endpoints backed by nano-vLLM, vLLM, SGLang, or another registered
`ModelRuntime`. The default engine is `nano-vllm`.

The SDK compiles each declared model into one
`inference.inferops.dev/v1alpha1` `ModelDeployment`. Its output follows the
contract in [crds.md](crds.md), defaults to an inactive deployment, and uses
the stable `/models/<deployment-name>` gateway route. An app containing
multiple models produces multiple `ModelDeployment` objects.

The decorator's `engine` value becomes `spec.runtime.ref`; it is not limited to
the default runtime. Omitting `engine` selects `nano-vllm`.

The decorator defaults to one NVIDIA GPU for compatibility. Setting `gpu=None`
produces a CPU-only deployment. Supplying a positive integer requests that many
NVIDIA GPUs; supplying a non-empty string requests one NVIDIA GPU of that type.
The selected runtime image must support the requested compute mode.

The SDK talks to the user's Kubernetes API through the CLI or Kubernetes
client. It does not rely on a hosted InferOps control plane.
