# SDK Endpoints

This example covers two SDK surfaces that are easy to miss:

- custom model-backed methods declared with `@inferops.web_endpoint`;
- Python client calls for OpenAI-compatible chat completions.

## Custom Endpoints

Each runtime folder declares an `Assistant` model with `/chat` and
`/chat/stream` handlers.

These Python methods are served by `inferops serve`. The endpoint server runs
your Python methods, binds `self.generate()` and `self.generate_stream()` to a
live InferOps gateway, and forwards generation to the deployed model runtime.

## OrbStack Setup

The llama.cpp path assumes an OrbStack Kubernetes context and the `default`
namespace. Before deploying, confirm the context and namespace exist:

```bash
kubectl config current-context
kubectl --context orbstack config view --minify \
  -o 'jsonpath={..namespace}{"\n"}'
kubectl --context orbstack get namespace default
```

If InferOps is not installed in that namespace yet, install it with the homelab
profile. InferOps commands default to the `default` namespace, so these examples
only pass `--context orbstack`:

```bash
inferops install \
  --context orbstack \
  --profile homelab \
  --cache-path /var/lib/inferops/models
```

Generate the `ModelDeployment` manifest:

```bash
inferops generate examples/sdk-endpoints/vllm/app.py
inferops generate examples/sdk-endpoints/llama/app.py
```

Deploy the vLLM model lane to a GPU cluster:

```bash
inferops deploy examples/sdk-endpoints/vllm/app.py --context <cluster-context>
inferops activate assistant-vllm --context <cluster-context> --timeout 20m
inferops status assistant-vllm --context <cluster-context> --watch --timeout 20m
```

Deploy the llama.cpp model lane to a CPU cluster such as OrbStack:

```bash
inferops deploy examples/sdk-endpoints/llama/app.py --context orbstack
inferops activate assistant-llama --context orbstack --timeout 10m
inferops status assistant-llama --context orbstack --watch --timeout 10m
```

## Run Endpoints Locally

Start a gateway forward before running the SDK endpoint server:

```bash
inferops gateway forward --context orbstack
```

Serve the llama SDK endpoints against the live deployment:

```bash
inferops serve examples/sdk-endpoints/llama/app.py --port 9000
```

Call the custom `/chat` endpoint:

```bash
curl -X POST http://127.0.0.1:9000/chat \
  -H "Content-Type: application/json" \
  -d '{"prompt":"Explain Kubernetes Services simply in two short bullets.","max_tokens":96}'
```

Call the streaming `/chat/stream` endpoint:

```bash
curl -N -X POST http://127.0.0.1:9000/chat/stream \
  -H "Content-Type: application/json" \
  -d '{"prompt":"Write one short sentence about model serving.","max_tokens":48}'
```

## Observability

If the monitoring stack is not installed yet, follow
[`docs/observability.md`](../../docs/observability.md).

Enable Prometheus scraping and Grafana dashboards:

```bash
helm upgrade inferops-operator deploy/helm/inferops-operator \
  --kube-context orbstack \
  --namespace default \
  --reuse-values \
  --set serviceMonitor.enabled=true \
  --set dashboards.enabled=true

kubectl --context orbstack -n monitoring port-forward \
  svc/kube-prometheus-stack-grafana 3000:80
```

Open `http://127.0.0.1:3000`, then use `InferOps / Platform` and
`InferOps / llama.cpp Runtime`.

Generate a little traffic for the graphs:

```bash
for prompt in \
  "Explain Kubernetes Services simply in two short bullets." \
  "Give one practical reason to use a model cache." \
  "Write a concise status update for an active model endpoint."; do
  curl -s -X POST http://127.0.0.1:9000/chat \
    -H "Content-Type: application/json" \
    -d "{\"prompt\":\"$prompt\",\"max_tokens\":96}"
  echo
done
```

For vLLM, use the same pattern after forwarding the GPU cluster gateway. If the
gateway is not on `http://127.0.0.1:8080`, pass `--gateway-url`:

```bash
inferops serve examples/sdk-endpoints/vllm/app.py \
  --gateway-url http://127.0.0.1:8080 \
  --port 9000
```

## Deploy Endpoints In-Cluster

To run the custom Python endpoints inside Kubernetes, let InferOps build and
push an endpoint app image, then create a normal Kubernetes Deployment and
Service for that image. The app talks to the model runtime through the
in-cluster gateway URL.

```bash
inferops deploy-endpoints examples/sdk-endpoints/llama/app.py \
  --context orbstack \
  --image ghcr.io/brassinai/llama-sdk-endpoints:v0.1.0 \
  --build
```

The default endpoint app name is `llama-sdk-endpoints`, so you can test it with
a normal Kubernetes port-forward:

```bash
kubectl --context orbstack port-forward svc/llama-sdk-endpoints 9000:8080
```

Then call the endpoint:

```bash
curl -X POST http://127.0.0.1:9000/chat \
  -H "Content-Type: application/json" \
  -d '{"prompt":"Explain Kubernetes Services simply."}'
```

For vLLM, use the same Dockerfile with the vLLM app source:

```bash
inferops deploy-endpoints examples/sdk-endpoints/vllm/app.py \
  --context <cluster-context> \
  --image ghcr.io/brassinai/vllm-sdk-endpoints:v0.1.0 \
  --build
```

`--build` uses Docker locally and pushes the image by default. Use `--no-push`
only for local clusters that can already pull from your local Docker image
store. For GHCR, run `docker login ghcr.io` first with a token that can write
packages for the target user or organization.

## Client Calls

Each `client_usage.py` uses the real SDK HTTP transport and calls the forwarded
InferOps gateway. Keep the gateway forward running in another terminal.

```bash
python examples/sdk-endpoints/llama/client_usage.py
INFEROPS_BASE_URL=http://127.0.0.1:8080/models/assistant-vllm \
  python examples/sdk-endpoints/vllm/client_usage.py
```

For a live cluster test, start a gateway forward:

```bash
inferops gateway forward --context orbstack
```

The scripts default to the model route prefix:

```python
client = inferops.Client(
    base_url="http://127.0.0.1:8080/models/assistant-llama",
    api_key=None,
)
```

Then OpenAI-compatible calls go to:

```text
/models/assistant-llama/v1/chat/completions
```

The client examples use `client.chat.completions.create(...)`, which maps to
the standard OpenAI-compatible `/v1/chat/completions` path. Use
`client.responses.create(...)` only with a runtime image that explicitly
implements `/v1/responses`.

If you are running from a source checkout where the local SDK or CLI is not
installed, prefix Python and CLI commands with `PYTHONPATH=sdk/python:cli`.
