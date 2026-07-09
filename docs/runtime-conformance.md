# Runtime conformance

Every InferOps runtime adapter must expose the same observable contract:

| Behavior | Contract |
| --- | --- |
| Protocol | OpenAI-compatible HTTP under `/v1` |
| Liveness | Configured `ModelRuntime.spec.healthPath` returns `2xx` |
| Readiness | Configured `readinessPath` returns `2xx` only when the model accepts traffic |
| Metrics | Configured `metricsPath` returns Prometheus text |
| Models | `GET /v1/models` advertises the configured model name |
| Inference | Non-streaming completions and incrementally flushed streaming chat completions |
| Errors | OpenAI `error` envelope for an unknown model |
| Lifecycle | The container entrypoint uses `exec`; the server handles `SIGTERM` and exits within the pod grace period |

The operator makes the declared contract visible on runtime pods through
`inferops.dev/runtime-protocol` and the standard Prometheus scrape
annotations. It uses the declared health and readiness paths for Kubernetes
probes.

## Running the suite

Run a runtime locally or port-forward its Service, then execute:

```bash
make runtime-conformance \
  RUNTIME_BASE_URL=http://127.0.0.1:8000 \
  RUNTIME_MODEL=<served-model-name>
```

Use `RUNTIME_CONFORMANCE_ARGS` when paths differ:

```bash
make runtime-conformance \
  RUNTIME_BASE_URL=http://127.0.0.1:8000 \
  RUNTIME_MODEL=<served-model-name> \
  RUNTIME_CONFORMANCE_ARGS="--readiness-path /health_generate"
```

The suite checks liveness, readiness, Prometheus output, model discovery,
non-streaming completions, and streaming SSE termination. It never prints
request bodies or credentials.

GPU runtimes must be exercised against their real server in a prepared GPU
environment. Their environment-to-CLI translation and signal forwarding are
covered separately by dependency-free unit tests. The custom FastAPI reference
runtime runs through the full suite in ordinary CPU-only CI.

Before publishing either GPU adapter, run both immutable release candidates on
prepared GPU nodes and execute the mandatory live matrix:

```bash
make runtime-conformance-matrix \
  VLLM_BASE_URL=http://<vllm-service>:8000 \
  VLLM_MODEL=<vllm-served-model-name> \
  SGLANG_BASE_URL=http://<sglang-service>:8000 \
  SGLANG_MODEL=<sglang-served-model-name>
```

The target fails when either runtime endpoint or model is omitted. CPU-only CI
does not pretend that mocked engine binaries validate the real GPU servers.

## Packaged runtime profiles

| Runtime | Health | Readiness | Metrics |
| --- | --- | --- | --- |
| nano-vLLM | `/health` | `/health` | `/metrics` |
| vLLM | `/health` | `/health` | `/metrics` |
| SGLang | `/health` | `/health_generate` | `/metrics` |
| llama.cpp | `/health` | `/health` | `/metrics` |
| custom FastAPI example | `/health` | `/ready` | `/metrics` |

Do not mark a new runtime image supported solely because its container starts.
Run this suite against the exact immutable image and model format that will be
published.
