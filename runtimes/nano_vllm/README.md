# nano-vLLM runtime image

This package builds the InferOps nano-vLLM runtime image. InferOps owns the
image, entrypoint, fake mode, tests, and Kubernetes runtime contract; the real
model engine implementation is installed from upstream nano-vLLM.

## HTTP contract

The image listens on port `8000` and serves:

- `GET /health` for process liveness.
- `GET /readiness` for model readiness and drain state.
- `GET /metrics` in Prometheus text format.
- `GET /v1/models` for model discovery.
- `POST /v1/completions` for text completions.
- `POST /v1/chat/completions` for basic role-formatted chat completions.

Set `stream: true` to receive OpenAI-style server-sent events. Each backend
chunk is sent as a separate ASGI response body with proxy buffering disabled.

## Images

Build the dependency-light fake image used by CI:

```bash
docker build \
  --target fake \
  --file runtimes/nano_vllm/Dockerfile \
  --tag inferops-runtime:fake .
```

Build the real GPU image:

```bash
docker build \
  --target real \
  --file runtimes/nano_vllm/Dockerfile \
  --tag inferops-runtime:nano-vllm .
```

## Testing

Run the runtime unit tests from the repository root:

```bash
uv run python -m unittest \
  tests.python.test_runtime_fake \
  tests.python.test_runtime_real \
  tests.python.test_runtime_server
```

Build both image targets:

```bash
docker build \
  --target fake \
  --file runtimes/nano_vllm/Dockerfile \
  --tag inferops-runtime:fake .

docker build \
  --target real \
  --file runtimes/nano_vllm/Dockerfile \
  --tag inferops-runtime:nano-vllm .
```

The real build should not need any additional build context. To confirm the
image is using the installed upstream package, inspect the import path:

```bash
docker run --rm \
  --entrypoint python \
  inferops-runtime:nano-vllm \
  -c "import nanovllm; print(nanovllm.__file__)"
```

The output should be under Python `site-packages`, not a copied source
directory.

### Test fake mode

Fake mode is the lightweight path used by CI. It requires no GPU or model
cache:

```bash
docker rm -f inferops-fake 2>/dev/null || true

docker run -d --rm \
  --name inferops-fake \
  -p 8000:8000 \
  inferops-runtime:fake
```

Check readiness, streaming, and metrics:

```bash
curl -i http://localhost:8000/readiness

curl -N http://localhost:8000/v1/completions \
  -H 'Content-Type: application/json' \
  -d '{"prompt":"Hello from InferOps","max_tokens":8,"stream":true}'

curl http://localhost:8000/metrics
```

The streaming response should emit server-sent-event chunks immediately and
end with:

```text
data: [DONE]
```

Stop the fake runtime:

```bash
docker stop inferops-fake
```

### Test real GPU mode

Real mode requires an NVIDIA GPU, the NVIDIA container runtime, and a prepared
model cache mounted into the container. The tested pretrained smoke model is
`Qwen/Qwen3-0.6B`.

Example host cache path used during validation:

```text
/home/dev/.cache/huggingface/inferops/Qwen3-0.6B
```

Start the real runtime:

```bash
docker rm -f inferops-real 2>/dev/null || true

docker run -d \
  --name inferops-real \
  --gpus all \
  --shm-size=2g \
  -p 8001:8000 \
  -v /home/dev/.cache/huggingface/inferops/Qwen3-0.6B:/models/qwen3:ro \
  -e MODEL_PATH=/models/qwen3 \
  -e MODEL_REPO=Qwen/Qwen3-0.6B \
  -e TENSOR_PARALLEL_SIZE=1 \
  -e ENFORCE_EAGER=true \
  -e MODEL_DTYPE=float16 \
  -e MAX_NUM_SEQS=16 \
  -e MAX_MODEL_LEN=1024 \
  -e INFEROPS_DRAIN_TIMEOUT=15s \
  inferops-runtime:nano-vllm
```

Follow startup logs until the model is loaded:

```bash
docker logs -f inferops-real
```

In another terminal, check readiness and model metadata:

```bash
curl -i http://localhost:8001/readiness
curl http://localhost:8001/v1/models
```

Run a genuine pretrained streaming inference:

```bash
curl -N http://localhost:8001/v1/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"Qwen/Qwen3-0.6B","prompt":"<|im_start|>user\nExplain Kubernetes in one sentence.<|im_end|>\n<|im_start|>assistant\n","max_tokens":64,"temperature":0.6,"stream":true}'
```

Check runtime metrics:

```bash
curl http://localhost:8001/metrics
```

Stop with a timeout longer than `INFEROPS_DRAIN_TIMEOUT` so in-flight work has
time to finish:

```bash
docker stop --timeout 20 inferops-real
```

To exercise bounded graceful drain, start a streaming request, then stop the
container from another terminal:

```bash
curl -N http://localhost:8001/v1/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"Qwen/Qwen3-0.6B","prompt":"Write a short paragraph about GPU scheduling.","max_tokens":256,"temperature":0.6,"stream":true}'

docker stop --timeout 20 inferops-real
```

Readiness should become false as shutdown starts, new requests should be
rejected, and active work should either finish or be bounded by the configured
drain timeout.

The real target installs nano-vLLM from GitHub at a pinned commit. Override
`NANOVLLM_COMMIT` only when deliberately moving InferOps to a newer upstream
runtime revision:

```bash
docker build \
  --target real \
  --build-arg NANOVLLM_COMMIT=<commit-sha> \
  --file runtimes/nano_vllm/Dockerfile \
  --tag inferops-runtime:nano-vllm .
```

The default real base image contains PyTorch 2.9.1 and CUDA 13.0, including
Blackwell `sm_120` support for RTX 50-series GPUs. Override `REAL_BASE_IMAGE`
when the cluster driver or upstream nano-vLLM dependency set requires another
compatible image. Runtime startup checks that PyTorch contains kernels for the
detected GPU before constructing `LLMEngine`.

CUDA 13.0 is required by the packaged FlashInfer JIT path for `sm_120` GPUs;
its architecture check rejects CUDA 12.8 for RTX 50-series execution even
though PyTorch's own CUDA 12.8 kernels support Blackwell.

The real image uses the official PyTorch CUDA 13.0 development base because
PyTorch Inductor, Triton, FlashInfer, and nano-vLLM kernels perform JIT
compilation during model warmup. Its matched `nvcc`, headers, and libraries are
exported through `CUDA_HOME`, `PATH`, and `LD_LIBRARY_PATH`. Mixing the
pip-installed CUDA 13.2 compiler with PyTorch's CUDA 13.0 headers is not
supported. Removing the compiler from the final image requires precompiling
and persisting every kernel variant needed by the target GPU architecture.
On WSL driver mounts that expose `libcuda.so.1` without the development
`libcuda.so` link, startup creates both names in the runtime user's writable
cache and configures Triton's supported `TRITON_LIBCUDA_PATH` override.

## Upstream nano-vLLM dependency

InferOps currently depends on:

- Repository: `https://github.com/Brassinai/nano-vllm`
- Branch: `runtime-support`
- Pinned commit: `a7ba517a4fc2921a6d0ba44763df087628b50363`

The Dockerfile installs the package with pip from that commit.

When nano-vLLM publishes an official release containing the InferOps runtime
support, replace the GitHub commit install with the release artifact.

The upstream package must remain installable through its `pyproject.toml` and
provide:

- `nanovllm.engine.llm_engine.LLMEngine(model_path, **config)`.
- `LLMEngine.stream_generate(prompt, SamplingParams)` yielding incremental
  strings or objects with a `text` attribute.
- `nanovllm.sampling_params.SamplingParams(max_tokens, temperature)`.
- `LLMEngine.exit()` for worker and accelerator cleanup.

## Model support

The authoritative nano-vLLM implementation provides real model execution for
its registered optimized architectures, including `Qwen3ForCausalLM` and its
compatible Qwen mapping. Its `HFAdapter` mapping for GPT-2 and DistilGPT2 is an
untrained embedding-and-linear-head smoke adapter: it validates engine and
streaming plumbing but deliberately does not load pretrained model weights.
InferOps emits a runtime warning when that adapter is selected. Do not use its
generated text to validate inference quality.

Use fake mode for dependency-free CI and a supported nano-vLLM architecture,
such as `Qwen/Qwen3-0.6B`, for end-to-end pretrained GPU inference.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `FAKE_MODE` | `true` in fake image, `false` in real image | Select dependency-free or nano-vLLM backend. |
| `MODEL_PATH` | required in real mode | Prepared model directory mounted read-only from the cache. |
| `MODEL_REPO` | empty | Model identifier reported by the OpenAI API. |
| `TENSOR_PARALLEL_SIZE` | `1` | Number of whole GPUs used by nano-vLLM. |
| `ENFORCE_EAGER` | `false` | Disable CUDA graph capture. |
| `MODEL_DTYPE` | `float16` | Model data type passed to nano-vLLM. |
| `MAX_NUM_SEQS` | `256` | Maximum concurrent engine sequences. |
| `MAX_MODEL_LEN` | `8192` | Maximum model sequence length. |
| `INFEROPS_DRAIN_TIMEOUT` | `5m` | Maximum shutdown drain interval. |
| `PORT` | `8000` | HTTP listen port. |

On `SIGTERM` or `SIGINT`, readiness becomes false immediately and new
generation requests receive `503`. The server waits for active generators for
at most `INFEROPS_DRAIN_TIMEOUT`; the Kubernetes termination grace period must
be longer than this timeout.
