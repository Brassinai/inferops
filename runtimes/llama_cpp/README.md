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
| `TOOL_CALLING_ENABLED` | unset | `true` adds `--jinja`; accepts `true`/`false` or `1`/`0` |
| `CHAT_TEMPLATE_FILE` | model metadata | `--chat-template-file` |

Additional container arguments are forwarded to `llama-server`.

## Tool calling

Use a tool-compatible model and template with `ModelRuntime.spec.args: ["--jinja"]`.
An optional `--chat-template-file` selects a template already mounted in the pod.
The environment mappings above are available in newly built adapter images;
existing images can forward the equivalent engine arguments directly.
`TOOL_CALLING_ENABLED=false` adds no flags; it does not override explicit
`spec.args` or upstream engine environment settings. Preserve vision/projector
arguments when adding tooling to a multimodal runtime.

The operator chart accepts per-runtime `command`, `args` and `env` under
`modelRuntimes.<name>`, using the existing ModelRuntime CRD fields. Create a
dedicated runtime when models need different templates. The cloud's read-only
agent reports configured tooling from ready runtime pods; configuration alone
does not certify model behavior. Validate a streamed tool call and tool-result
round trip before recording cloud capability evidence. See InferCloud's
`docs/model-metadata.md` operator workflow.

## Vision models

Image understanding needs a compatible language-model GGUF and its matching
multimodal projector, both prepared in ModelCache. When both are top-level GGUF
files, set `MODEL_FILE=model.gguf` explicitly and forward
`--mmproj /models/model/mmproj.gguf` through ModelRuntime `spec.args` (adjust the
filenames to the cache). ModelRuntime `spec.env` supplies MODEL_FILE. The existing
adapter forwards these arguments; no new operator field or upload endpoint is
required. A projector must match the model and the pinned llama-server version.

The gateway forwards OpenAI-compatible `image_url` content parts unchanged.
Image count, processor token bounds and standard usage reporting still need to
be tested against the actual deployment before InferCloud attests vision support.
See [upstream multimodal setup](https://github.com/ggml-org/llama.cpp/blob/master/docs/multimodal.md).
