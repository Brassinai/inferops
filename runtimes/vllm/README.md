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

Additional container arguments are forwarded to `vllm serve`.
