# SGLang runtime image

This image adds a small environment-to-CLI adapter to the upstream
`lmsysorg/sglang:v0.5.14` image, pinned by digest. SGLang owns its OpenAI
server, request handling, metrics, and process lifecycle.

The pinned upstream server provides:

- `GET /health` for process liveness;
- `GET /health_generate` for model readiness;
- `GET /metrics` in Prometheus format when `--enable-metrics` is set;
- `GET /v1/models`;
- `POST /v1/completions`; and
- `POST /v1/chat/completions`.

The adapter always enables metrics because `/metrics` is part of the InferOps
runtime contract.

## Build

```bash
docker build \
  --file runtimes/sglang/Dockerfile \
  --tag inferops-runtime:sglang .
```

Override `SGLANG_IMAGE` only with a tested immutable version or digest.

After starting the image on a GPU node, run the shared contract with SGLang's
stronger readiness endpoint:

```bash
make runtime-conformance \
  RUNTIME_BASE_URL=http://127.0.0.1:8000 \
  RUNTIME_MODEL=<served-model-name> \
  RUNTIME_CONFORMANCE_ARGS="--readiness-path /health_generate"
```

## CPU support

There is intentionally no generic SGLang CPU image in this repository.
SGLang's documented CPU backend is optimized for Intel AMX instructions on
4th-generation or newer Intel Xeon Scalable processors. Upstream documents
building `docker/xeon.Dockerfile`; it does not publish a portable CPU server
image equivalent to vLLM's Linux `arm64` image.

An Apple Silicon Mac running Linux containers exposes an ARM64 CPU, not Intel
Xeon AMX. Removing the GPU flags from the regular SGLang image does not turn
it into a working CPU runtime. Use the llama.cpp or vLLM CPU adapter for local
Mac testing. A future SGLang CPU variant should be an explicit Xeon-specific
image and node profile, tested on supported hardware.

## Model cache

InferOps mounts one prepared model directory at `MODEL_PATH`. The entrypoint
requires that directory to exist and never falls back to downloading
`MODEL_REPO`. The image enables Hugging Face and Transformers offline modes so
model acquisition remains the `ModelCache` controller's responsibility.

`MODEL_REPO` is optional metadata passed as SGLang's served model name.

## Environment

| Variable | Default in image | CLI mapping |
| --- | --- | --- |
| `MODEL_PATH` | `/models/model` | `--model-path` |
| `MODEL_REPO` | empty | `--served-model-name` |
| `HOST` | `0.0.0.0` | `--host` |
| `PORT` | `8000` | `--port` |
| `TENSOR_PARALLEL_SIZE` | engine default | `--tp-size` |
| `MODEL_DTYPE` | engine default | `--dtype` |
| `MAX_MODEL_LEN` | engine default | `--context-length` |
| `GPU_MEMORY_UTILIZATION` | engine default | `--mem-fraction-static` |
| `MAX_NUM_SEQS` | engine default | `--max-running-requests` |
| `ENFORCE_EAGER` | `false` | disables prefill and decode CUDA graphs |

Additional container arguments are forwarded to `sglang.launch_server`.
