# SDK

The Python SDK provides a developer-friendly path for declaring model
endpoints backed by nano-vLLM, vLLM, SGLang, or another registered
`ModelRuntime`. The default engine is `nano-vllm`.

The SDK compiles each declared model into one
`inference.inferops.dev/v1alpha1` `ModelDeployment`. Its output follows the
contract in [crds.md](crds.md), defaults to an inactive deployment, and uses
the stable `/models/<deployment-name>` gateway route. An app containing
multiple models produces multiple `ModelDeployment` objects, sorted by
deployment name for deterministic GitOps-friendly YAML output.

The decorator's `engine` value becomes `spec.runtime.ref`; it is not limited to
the default runtime. Omitting `engine` selects `nano-vllm`.

The decorator defaults to one NVIDIA GPU for compatibility. Setting `gpu=None`
produces a CPU-only deployment. Supplying a positive integer requests that many
NVIDIA GPUs; supplying a non-empty string requests one NVIDIA GPU of that type.
The selected runtime image must support the requested compute mode.

The SDK validates decorator inputs eagerly so invalid model names, activation
policies, scaling bounds, route paths, cache settings, and GPU requests fail
before YAML is generated. Supported high-level deployment options include:

- `activation` and `when_full` for activation policy.
- `min_replicas` and `max_replicas` for explicit scaling bounds.
- `route_path`, `route_enabled`, and `openai_compatible` for gateway routing.
- `cache_enabled`, `cache_type`, `cache_size`, and `cache_path` for model cache settings.
- `runtime_image`, `dtype`, and `hugging_face_token_secret_name` for runtime overrides and source credentials.

The SDK talks to the user's Kubernetes API through the CLI or Kubernetes
client. It does not rely on a hosted InferOps control plane.
