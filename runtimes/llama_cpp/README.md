# llama.cpp runtime image

This CPU-first runtime adds a small InferOps adapter to the official
`ghcr.io/ggml-org/llama.cpp:server` image. The upstream image is pinned by
digest and provides Linux `amd64`, `arm64`, and `s390x` variants. Docker
Desktop or OrbStack on an Apple Silicon Mac selects the Linux `arm64` image;
an Intel Mac selects `amd64`.

llama.cpp owns its server, OpenAI-compatible request handling, health,
metrics, streaming, and process lifecycle. InferOps only supplies the prepared
model-cache path and common runtime settings.

## Model cache

llama.cpp consumes GGUF model files. `MODEL_PATH` must be a mounted directory
prepared by ModelCache.

- If the directory contains exactly one top-level `.gguf` file, the adapter
  selects it automatically.
- Set `MODEL_FILE` to a relative filename when the directory contains multiple
  GGUF files or a split model. For split models, select the first shard.
- `MODEL_REPO` is optional API metadata and maps to llama.cpp's `--alias`.

The adapter always passes a local file to `llama-server`; it never asks the
engine to download a model.

## Build on macOS

Docker runs this as a Linux container:

```bash
docker build \
  --file runtimes/llama_cpp/Dockerfile \
  --tag inferops-runtime:llama-cpp .
```

The CPU image does not use macOS Metal because the engine runs inside a Linux
VM. It is intended as a portable contract test, not the fastest native Mac
configuration.

## Local smoke test

Download a small public GGUF model (approximately 100 MB) into a directory
that represents a ready ModelCache:

```bash
mkdir -p .inferops/models/smollm2-135m
curl --fail --location \
  --output .inferops/models/smollm2-135m/model.gguf \
  https://huggingface.co/QuantFactory/SmolLM2-135M-Instruct-GGUF/resolve/main/SmolLM2-135M-Instruct.Q4_K_M.gguf
```

Run the Linux runtime:

```bash
docker run --rm \
  --publish 8000:8000 \
  --volume "$PWD/.inferops/models/smollm2-135m:/models/model:ro" \
  --env MODEL_REPO=smollm2-135m \
  --env MAX_MODEL_LEN=512 \
  inferops-runtime:llama-cpp
```

Verify the shared runtime contract:

```bash
curl --fail http://localhost:8000/health
curl --fail http://localhost:8000/v1/models
curl --fail http://localhost:8000/metrics
curl --fail http://localhost:8000/v1/chat/completions \
  --header 'content-type: application/json' \
  --data '{"model":"smollm2-135m","messages":[{"role":"user","content":"Reply with one short sentence."}],"max_tokens":32}'
```

## Environment

| Variable | Default in image | CLI mapping |
| --- | --- | --- |
| `MODEL_PATH` | `/models/model` | directory containing GGUF files |
| `MODEL_FILE` | auto-detected | `--model` |
| `MODEL_REPO` | empty | `--alias` |
| `HOST` | `0.0.0.0` | `--host` |
| `PORT` | `8000` | `--port` |
| `MAX_MODEL_LEN` | engine/model default | `--ctx-size` |
| `MAX_NUM_SEQS` | engine default | `--parallel` |
| `CPU_THREADS` | engine default | `--threads` |
| `CPU_THREADS_BATCH` | engine default | `--threads-batch` |

Additional container arguments are forwarded to `llama-server`.
