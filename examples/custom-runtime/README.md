# Custom FastAPI Runtime

This reference runtime demonstrates the complete `ModelRuntime` contract
without requiring a GPU or downloading model weights. It is an API fixture,
not an inference engine: responses are deterministic and intended for
development and conformance testing.

The image owns:

- OpenAI-compatible models, completions, chat completions, and streaming APIs;
- separate liveness (`/health`) and readiness (`/ready`) endpoints;
- Prometheus text metrics at `/metrics`;
- `MODEL_PATH`, `MODEL_REPO`, `HOST`, and `PORT` runtime settings; and
- graceful `SIGTERM` handling through Uvicorn.

Build and run it:

```bash
docker build -f examples/custom-runtime/Dockerfile \
  -t inferops/custom-fastapi:0.0.0 .
mkdir -p /tmp/inferops-custom-model
docker run --rm -p 8000:8000 \
  -v /tmp/inferops-custom-model:/models/model:ro \
  -e MODEL_REPO=custom-fastapi \
  inferops/custom-fastapi:0.0.0
```

Run the shared conformance suite:

```bash
python3 scripts/runtime_conformance.py \
  --base-url http://127.0.0.1:8000 \
  --model custom-fastapi \
  --readiness-path /ready
```

`modelruntime.yaml` registers the adapter in an InferOps namespace. Replace its
development image reference with the immutable image produced by your build
pipeline before using it in production.
