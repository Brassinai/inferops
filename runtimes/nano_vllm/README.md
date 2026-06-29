# nano-vLLM runtime image

This image is a thin InferOps adapter around an upstream nano-vLLM engine
image. InferOps does not import `LLMEngine`, implement an HTTP server, format
OpenAI responses, or own engine shutdown.

The upstream image is expected to provide:

- a `nanovllm serve <model-path>` command;
- OpenAI-compatible `/v1/models`, `/v1/completions`, and
  `/v1/chat/completions` endpoints;
- `GET /health` for health and readiness;
- `GET /metrics` in Prometheus format; and
- native signal handling and graceful shutdown.

That server contract belongs in the nano-vLLM codebase. The InferOps
entrypoint only translates the common runtime environment to CLI arguments.

## Build

Until the upstream nano-vLLM image is published, pass a locally built engine
image explicitly:

```bash
docker build \
  --build-arg NANOVLLM_IMAGE=example/nano-vllm:<immutable-tag> \
  --file runtimes/nano_vllm/Dockerfile \
  --tag inferops-runtime:nano-vllm .
```

Release builds should use an immutable upstream tag or digest.

## Model cache

InferOps mounts one prepared model directory at `MODEL_PATH`. The entrypoint
requires that directory to exist and never falls back to downloading
`MODEL_REPO`. The image enables Hugging Face and Transformers offline modes so
model acquisition remains the `ModelCache` controller's responsibility.

`MODEL_REPO` is optional metadata used as the model name returned by the
OpenAI API.

## Environment

| Variable | Default in image | CLI mapping |
| --- | --- | --- |
| `MODEL_PATH` | `/models/model` | positional model path |
| `MODEL_REPO` | empty | `--served-model-name` |
| `HOST` | `0.0.0.0` | `--host` |
| `PORT` | `8000` | `--port` |
| `TENSOR_PARALLEL_SIZE` | engine default | `--tensor-parallel-size` |
| `MODEL_DTYPE` | engine default | `--dtype` |
| `MAX_MODEL_LEN` | engine default | `--max-model-len` |
| `GPU_MEMORY_UTILIZATION` | engine default | `--gpu-memory-utilization` |
| `MAX_NUM_SEQS` | engine default | `--max-num-seqs` |
| `ENFORCE_EAGER` | `false` | `--enforce-eager` |

Additional container arguments are forwarded to `nanovllm serve`.
