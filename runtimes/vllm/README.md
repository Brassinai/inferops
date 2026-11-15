# vLLM runtime images

These images add a small environment-to-CLI adapter to upstream vLLM images
pinned by digest. vLLM owns its OpenAI server, request handling, metrics, and
process lifecycle.

The upstream server provides:

- `GET /health` for health and readiness;
- `GET /metrics` in Prometheus format;
- `GET /v1/models`;
- `POST /v1/completions`; and
- `POST /v1/chat/completions`.

InferOps packages Prometheus Operator scraping and a vLLM-first Grafana
dashboard in the Helm charts. The dashboard uses vLLM's Prometheus metrics for
TTFT, generation tokens/sec, waiting requests, requested-token approximation,
and KV cache pressure. Raw GPU utilization should come from DCGM exporter or an
equivalent node GPU exporter; OpenTelemetry is a later integration and is not
required for the MVP runtime path.

## GPU build

```bash
docker build \
  --file runtimes/vllm/Dockerfile \
  --tag inferops-runtime:vllm .
```

Override `VLLM_IMAGE` only with a tested immutable version or digest.

## CPU build on macOS

vLLM publishes separate CPU images for Linux `arm64` and `x86_64`.
`Dockerfile.cpu` defaults to the v0.23.0 Linux `arm64` image used by Docker
Desktop or OrbStack on an Apple Silicon Mac:

```bash
docker build \
  --file runtimes/vllm/Dockerfile.cpu \
  --tag inferops-runtime:vllm-cpu .
```

The default is intentionally architecture-specific. On an `x86_64` Linux
host, select the matching pinned image explicitly:

```bash
docker build \
  --file runtimes/vllm/Dockerfile.cpu \
  --build-arg 'VLLM_CPU_IMAGE=vllm/vllm-openai-cpu:v0.23.0-x86_64@sha256:6240a6bba604e607300e47490e3477211f968bdf125211bb877d19a70b8fe844' \
  --tag inferops-runtime:vllm-cpu .
```

This is a Linux CPU container. It does not use Apple Metal.
The vLLM image is substantially larger than the llama.cpp image, so ensure
the Docker or OrbStack Linux VM has several GiB of free disk space before
building it.

### Local CPU smoke test

Prepare a small Transformers model as the local ModelCache contents. The
following command uses the Hugging Face CLI without installing it into this
repository:

```bash
mkdir -p .inferops/models/smollm2-135m-vllm
uvx --from huggingface-hub hf download \
  HuggingFaceTB/SmolLM2-135M-Instruct \
  --local-dir .inferops/models/smollm2-135m-vllm
```

Run the image with conservative settings for a laptop:

```bash
docker run --rm \
  --publish 8000:8000 \
  --volume "$PWD/.inferops/models/smollm2-135m-vllm:/models/model:ro" \
  --security-opt seccomp=unconfined \
  --cap-add SYS_NICE \
  --shm-size 2g \
  --env MODEL_REPO=smollm2-135m \
  --env MODEL_DTYPE=float32 \
  --env MAX_MODEL_LEN=512 \
  --env MAX_NUM_SEQS=1 \
  --env VLLM_CPU_KVCACHE_SPACE=1 \
  --env VLLM_CPU_OMP_THREADS_BIND=auto \
  inferops-runtime:vllm-cpu
```

`VLLM_CPU_KVCACHE_SPACE` is measured in GiB. Increase it only when the
container has enough memory.

Verify the same runtime contract used by the GPU image:

```bash
curl --fail http://localhost:8000/health
curl --fail http://localhost:8000/v1/models
curl --fail http://localhost:8000/metrics
curl --fail http://localhost:8000/v1/chat/completions \
  --header 'content-type: application/json' \
  --data '{"model":"smollm2-135m","messages":[{"role":"user","content":"Reply with one short sentence."}],"max_tokens":32}'
```

Run the automated shared contract:

```bash
make runtime-conformance \
  RUNTIME_BASE_URL=http://127.0.0.1:8000 \
  RUNTIME_MODEL=smollm2-135m
```

## Model cache

InferOps mounts one prepared model directory at `MODEL_PATH`. The entrypoint
requires that directory to exist and never falls back to downloading
`MODEL_REPO`. The image enables Hugging Face and Transformers offline modes so
model acquisition remains the `ModelCache` controller's responsibility.

`MODEL_REPO` is optional metadata passed as vLLM's served model name.

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
| `TOOL_CALLING_ENABLED` | unset | `true` adds `--enable-auto-tool-choice`; accepts `true`/`false` or `1`/`0` |
| `TOOL_CALL_PARSER` | unset | required with tooling enabled; maps to `--tool-call-parser` |
| `CHAT_TEMPLATE_FILE` | model metadata | `--chat-template` |

Additional container arguments are forwarded to `vllm serve`.

## Tool calling

Select a parser and chat template compatible with the exact model and vLLM
version. For example, a Hermes-compatible model can use
`ModelRuntime.spec.args: ["--enable-auto-tool-choice", "--tool-call-parser", "hermes"]`.
Do not select `hermes` for an arbitrary model. The environment mappings above
are available in newly built adapter images; existing images can forward the
equivalent engine arguments. Enabling tooling through the environment without a
parser fails startup explicitly. False adds no flags and does not override
explicit engine arguments.

Per-runtime `command`, `args` and `env` are configurable through the operator
chart's `modelRuntimes.<name>` values and the existing ModelRuntime CRD. Use
dedicated runtimes for differing model/parser combinations. A running server
does not establish tool-call correctness: validate streamed function arguments
and a follow-up tool result, then retain evidence for InferCloud's operator
verification workflow. These checks are separate from consumer login and the
default cloud-connect workflow.

## Vision models

Use a compatible vision-language model and include its processor/tokenizer assets
in ModelCache. Existing ModelRuntime `spec.args` can forward
`--limit-mm-per-prompt '{"image":4}'` and model-specific processor options. There
is no new InferOps deployment field or gateway upload API. The gateway preserves
OpenAI-compatible image messages and records runtime `prompt_tokens` without
trying to tokenize base64 data itself.

The count limit alone does not bound image tokens. Configure and verify the
processor's resolution/tiling limits for the selected model and pinned runtime.
InferCloud vision publishing also requires an attestation that standard runtime
usage includes visual tokens and fits its bounded image-input profile. Do not
infer vision capability from the engine name. See
[upstream multimodal input](https://docs.vllm.ai/en/stable/features/multimodal_inputs/).
