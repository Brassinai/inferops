# Build and Publish Images

Publish InferOps images to a registry your cluster can pull from. Defaults use
`ghcr.io/brassinai`.

## Image overview

| Component | Default image | Makefile target |
| --- | --- | --- |
| Operator | `ghcr.io/brassinai/inferops-operator:<tag>` | `make operator-image` |
| Gateway | `ghcr.io/brassinai/inferops-gateway:<tag>` | `make gateway-image` |
| Downloader | `ghcr.io/brassinai/model-downloader:<tag>` | `make model-downloader-build` |
| Dashboard | `ghcr.io/brassinai/inferops-dashboard:<tag>` | `make dashboard-image` |
| llama.cpp | `ghcr.io/brassinai/llama-cpp:<tag>` | `runtimes/llama_cpp/Dockerfile` |
| vLLM | `ghcr.io/brassinai/vllm:<tag>` | `runtimes/vllm/Dockerfile` |
| SGLang | `ghcr.io/brassinai/sglang:<tag>` | `runtimes/sglang/Dockerfile` |

## Set namespace and tag

```bash
export IMAGE_NAMESPACE=ghcr.io/brassinai
export IMAGE_TAG=v0.1.0
export IMAGE_PLATFORMS=linux/amd64,linux/arm64
```

Use a namespace you control if you are not publishing to `brassinai`.

## Create a GHCR token

Create a classic GitHub personal access token with:

- `write:packages`
- `read:packages` (if also pulling private images)

Log in:

```bash
echo "$GHCR_TOKEN" | docker login ghcr.io \
  --username "$GHCR_USER" \
  --password-stdin
```

## Build locally

```bash
make control-plane-images IMAGE_TAG="$IMAGE_TAG"
make model-downloader-build IMAGE_TAG="$IMAGE_TAG"
make dashboard-image IMAGE_TAG="$IMAGE_TAG"

docker build -f runtimes/llama_cpp/Dockerfile -t inferops/llama-cpp:"$IMAGE_TAG" .
docker build -f runtimes/vllm/Dockerfile -t inferops/vllm:"$IMAGE_TAG" .
docker build -f runtimes/sglang/Dockerfile -t inferops/sglang:"$IMAGE_TAG" .
```

## Publish multi-platform images

```bash
make control-plane-images-push
make model-downloader-push
make dashboard-image-push
```

Runtime images:

```bash
# Portable CPU runtime
docker buildx build \
  --platform "$IMAGE_PLATFORMS" \
  -f runtimes/llama_cpp/Dockerfile \
  -t "$IMAGE_NAMESPACE/llama-cpp:$IMAGE_TAG" \
  --push .

# GPU runtimes (usually amd64 only)
docker buildx build \
  --platform linux/amd64 \
  -f runtimes/vllm/Dockerfile \
  -t "$IMAGE_NAMESPACE/vllm:$IMAGE_TAG" \
  --push .

docker buildx build \
  --platform linux/amd64 \
  -f runtimes/sglang/Dockerfile \
  -t "$IMAGE_NAMESPACE/sglang:$IMAGE_TAG" \
  --push .
```

Inspect manifests:

```bash
docker buildx imagetools inspect "$IMAGE_NAMESPACE/inferops-operator:$IMAGE_TAG"
```

## Install with published images

If you published to the default namespace and tag:

```bash
inferops install --profile homelab \
  --cache-path /var/lib/inferops/models \
  --cache-capacity 500Gi
```

If you published elsewhere, override with Helm:

```bash
helm upgrade --install inferops-operator deploy/helm/inferops-operator \
  --namespace default \
  --set-string image.repository="$IMAGE_NAMESPACE/inferops-operator" \
  --set-string image.tag="$IMAGE_TAG" \
  --set-string cache.downloaderImage="$IMAGE_NAMESPACE/model-downloader:$IMAGE_TAG"

helm upgrade --install inferops-gateway deploy/helm/inferops-gateway \
  --namespace default \
  --set-string image.repository="$IMAGE_NAMESPACE/inferops-gateway" \
  --set-string image.tag="$IMAGE_TAG"
```

## OrbStack CPU smoke test

```bash
orb start k8s
kubectl config use-context orbstack

inferops install --context orbstack --profile homelab \
  --cache-path /var/lib/inferops/models \
  --cache-capacity 2Gi

kubectl --context orbstack apply -f examples/yaml-deploy/modeldeployment-cpu.yaml
inferops activate cpu-smollm --context orbstack --timeout 10m
inferops gateway forward --context orbstack

curl -X POST http://127.0.0.1:8080/models/cpu-smollm/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"cpu-smollm","messages":[{"role":"user","content":"Say hello."}]}'
```

## Private packages

Create an image pull secret:

```bash
kubectl create secret docker-registry ghcr-pull \
  --namespace default \
  --docker-server=ghcr.io \
  --docker-username="$GHCR_USER" \
  --docker-password="$GHCR_TOKEN"
```

Attach it to the chart:

```bash
helm upgrade --install inferops-operator deploy/helm/inferops-operator \
  --namespace default \
  --set imagePullSecrets[0].name=ghcr-pull
```

For operator-generated downloader Jobs and runtime pods, patch the namespace
ServiceAccount:

```bash
kubectl patch serviceaccount default \
  --namespace default \
  --type merge \
  --patch '{"imagePullSecrets":[{"name":"ghcr-pull"}]}'
```
