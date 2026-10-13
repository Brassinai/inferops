# Runtime Conformance

Every InferOps runtime adapter must expose the same contract.

## Contract

| Behavior | Requirement |
| --- | --- |
| Protocol | OpenAI-compatible HTTP under `/v1` |
| Liveness | `ModelRuntime.spec.healthPath` returns `2xx` |
| Readiness | `readinessPath` returns `2xx` only when ready for traffic |
| Metrics | `metricsPath` returns Prometheus text |
| Models | `GET /v1/models` advertises the configured model name |
| Inference | Non-streaming and streaming chat completions |
| Errors | OpenAI `error` envelope for unknown model |
| Lifecycle | Entrypoint uses `exec`; handles `SIGTERM` within grace period |

## Run the suite

```bash
make runtime-conformance \
  RUNTIME_BASE_URL=http://127.0.0.1:8000 \
  RUNTIME_MODEL=<served-model-name>
```

Override paths:

```bash
make runtime-conformance \
  RUNTIME_BASE_URL=http://127.0.0.1:8000 \
  RUNTIME_MODEL=<name> \
  RUNTIME_CONFORMANCE_ARGS="--readiness-path /health_generate"
```

## GPU runtime matrix

Before publishing a GPU adapter, run it on real GPU hardware:

```bash
make runtime-conformance-matrix \
  VLLM_BASE_URL=http://<vllm>:8000 \
  VLLM_MODEL=<name> \
  SGLANG_BASE_URL=http://<sglang>:8000 \
  SGLANG_MODEL=<name>
```

## Packaged runtime profiles

| Runtime | Health | Readiness | Metrics |
| --- | --- | --- | --- |
| nano-vLLM | `/health` | `/health` | `/metrics` |
| vLLM | `/health` | `/health` | `/metrics` |
| SGLang | `/health` | `/health_generate` | `/metrics` |
| llama.cpp | `/health` | `/health` | `/metrics` |
