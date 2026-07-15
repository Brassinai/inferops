# Build and Publish Images

Use this guide when you need to publish InferOps images to a registry that a
Kubernetes cluster can pull from. The chart defaults use GitHub Container
Registry (GHCR) under the `ghcr.io/brassinai` namespace.

This guide covers the core images needed before a cluster can run a packaged
InferOps install:

| Component | Default image | Local build or publish target |
| --- | --- | --- |
| Operator | `ghcr.io/brassinai/inferops-operator:<tag>` | `make operator-image` / `make operator-image-push` |
| Gateway | `ghcr.io/brassinai/inferops-gateway:<tag>` | `make gateway-image` / `make gateway-image-push` |
| Model downloader | `ghcr.io/brassinai/model-downloader:<tag>` | `make model-downloader-build` / `make model-downloader-push` |
| Dashboard | `ghcr.io/brassinai/inferops-dashboard:<tag>` | `make dashboard-image` / `make dashboard-image-push` |
| llama.cpp runtime | `ghcr.io/brassinai/llama-cpp:<tag>` | `runtimes/llama_cpp/Dockerfile` |
| vLLM runtime | `ghcr.io/brassinai/vllm:<tag>` | `runtimes/vllm/Dockerfile` |
| SGLang runtime | `ghcr.io/brassinai/sglang:<tag>` | `runtimes/sglang/Dockerfile` |

The `nano-vllm` runtime image name is also present in the chart defaults, but
its Dockerfile requires an upstream nano-vLLM base image. Publish only the
runtime images your deployment manifests reference.

## Choose a Namespace and Tag

Set the registry namespace and image tag once:

```bash
export IMAGE_NAMESPACE=ghcr.io/brassinai
export IMAGE_TAG=v0.1.0
export IMAGE_PLATFORMS=linux/amd64,linux/arm64
```

Use `ghcr.io/brassinai` for Brassin AI releases if your GitHub user can
publish packages into the `brassinai` organization. Otherwise use a namespace
you control, such as:

```bash
export IMAGE_NAMESPACE=ghcr.io/<github-user-or-org>
export IMAGE_TAG=v0.1.0
export IMAGE_PLATFORMS=linux/amd64,linux/arm64
```

If you publish under a different namespace, install InferOps with Helm image
overrides or update the chart values used by your release.

## Create a GHCR Token

For command-line publishing to GHCR, create a personal access token from a
GitHub user account. It is not generated as an organization token.

Use a classic personal access token with at least:

- `write:packages` to push images.
- `read:packages` if the same token will also pull private images.

GitHub's GHCR documentation:

- [Working with the Container registry](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)
- [About permissions for GitHub Packages](https://docs.github.com/en/packages/learn-github-packages/about-permissions-for-github-packages)
- [Create a classic token with `write:packages`](https://github.com/settings/tokens/new?scopes=write:packages)

Organization ownership still matters. To push to `ghcr.io/brassinai/...`, the
user that owns the token must have write access to packages in the `brassinai`
organization. If the organization requires SAML SSO, authorize the token for
that organization after creating it.

For GitHub Actions publishing from this repository, prefer `GITHUB_TOKEN`
instead of a long-lived personal token. A package published by a workflow can be
associated with the workflow repository, and repository/package permissions can
then be managed in GitHub.

Log in locally:

```bash
export GHCR_USER=<github-user>
export GHCR_TOKEN=<classic-token-with-write-packages>

echo "$GHCR_TOKEN" | docker login ghcr.io \
  --username "$GHCR_USER" \
  --password-stdin
```

## Build Local Images

Use local builds for development, smoke tests, or kind/OrbStack clusters that
run the same architecture as your Docker host. These commands produce
single-platform images. On Apple Silicon, that usually means Linux `arm64`;
on a typical Linux CI runner, that usually means Linux `amd64`.

Build the control-plane images and model downloader:

```bash
make control-plane-images IMAGE_TAG="$IMAGE_TAG"
make model-downloader-build IMAGE_TAG="$IMAGE_TAG"
```

Build the dashboard image:

```bash
make dashboard-image IMAGE_TAG="$IMAGE_TAG"
```

Build the llama.cpp runtime image:

```bash
docker build \
  --file runtimes/llama_cpp/Dockerfile \
  --tag inferops/llama-cpp:"$IMAGE_TAG" \
  .
```

Build the vLLM runtime image when publishing the GPU vLLM runtime:

```bash
docker build \
  --file runtimes/vllm/Dockerfile \
  --tag inferops/vllm:"$IMAGE_TAG" \
  .
```

Build the SGLang runtime image when publishing the SGLang runtime:

```bash
docker build \
  --file runtimes/sglang/Dockerfile \
  --tag inferops/sglang:"$IMAGE_TAG" \
  .
```

Do not use these local images as the default release path unless you
intentionally want to publish only your Docker host architecture. For portable
GHCR releases, use the multi-platform publish targets below.

## Publish Multi-Platform Core Images

Use the publish targets for GHCR release images. They build and push a Docker
manifest list for `IMAGE_PLATFORMS`, so Linux `amd64` and Linux `arm64` users
can pull the same tag and receive the matching image for their node.

```bash
make control-plane-images-push
make model-downloader-push
make dashboard-image-push
```

If you did not export the variables above, pass literal values instead of
empty shell references:

```bash
make control-plane-images-push \
  IMAGE_NAMESPACE=ghcr.io/brassinai \
  IMAGE_TAG=v0.1.0 \
  IMAGE_PLATFORMS=linux/amd64,linux/arm64
```

Check the pushed manifests before installing:

```bash
docker buildx imagetools inspect "$IMAGE_NAMESPACE/inferops-operator:$IMAGE_TAG"
docker buildx imagetools inspect "$IMAGE_NAMESPACE/inferops-gateway:$IMAGE_TAG"
docker buildx imagetools inspect "$IMAGE_NAMESPACE/model-downloader:$IMAGE_TAG"
docker buildx imagetools inspect "$IMAGE_NAMESPACE/inferops-dashboard:$IMAGE_TAG"
```

Each manifest should include the platforms you published, for example
`linux/amd64` and `linux/arm64`.

## Publish Runtime Images

Runtime images inherit stronger architecture constraints from their upstream
engine images. Build and push only the platform variants that the upstream base
image supports and that your deployment needs.

For portable runtimes whose base image supports both platforms, publish with
`buildx`:

```bash
docker buildx build \
  --platform "$IMAGE_PLATFORMS" \
  --file runtimes/llama_cpp/Dockerfile \
  --tag "$IMAGE_NAMESPACE/llama-cpp:$IMAGE_TAG" \
  --push \
  .
```

For GPU runtimes, most clusters are Linux `amd64`; publish that platform unless
you have tested an ARM GPU base image:

```bash
docker buildx build \
  --platform linux/amd64 \
  --file runtimes/vllm/Dockerfile \
  --tag "$IMAGE_NAMESPACE/vllm:$IMAGE_TAG" \
  --push \
  .

docker buildx build \
  --platform linux/amd64 \
  --file runtimes/sglang/Dockerfile \
  --tag "$IMAGE_NAMESPACE/sglang:$IMAGE_TAG" \
  --push \
  .
```

After the first push, set the GHCR package visibility and access policy in
GitHub. Public packages can be pulled by clusters without authentication.
Private packages require a Kubernetes image pull secret.

## Install with Published Images

If you published to the default namespace and tag used by the chart, the normal
install path can use the defaults. This is CPU-safe and works for the
llama.cpp path:

```bash
inferops install \
  --context <cluster-context> \
  --profile homelab \
  --cache-path /var/lib/inferops/models
```

For an NVIDIA GPU cluster, make the GPU assumption explicit:

```bash
inferops install \
  --context <cluster-context> \
  --profile homelab \
  --compute-profile nvidia-gpu \
  --cache-path /var/lib/inferops/models
```

The compute profile controls install-time operator assumptions. It does not
choose the runtime for every model. Each `ModelDeployment` still selects its
runtime with `spec.runtime.ref`, such as `llama-cpp`, `vllm`, or `sglang`.

If you published to a different namespace or tag, pass explicit Helm values:

```bash
helm upgrade --install inferops-operator deploy/helm/inferops-operator \
  --kube-context <cluster-context> \
  --namespace default \
  --create-namespace \
  --wait \
  --timeout 5m \
  --set-string image.repository="$IMAGE_NAMESPACE/inferops-operator" \
  --set-string image.tag="$IMAGE_TAG" \
  --set-string cache.downloaderImage="$IMAGE_NAMESPACE/model-downloader:$IMAGE_TAG" \
  --set-string modelRuntimes.llama-cpp.image.repository="$IMAGE_NAMESPACE/llama-cpp" \
  --set-string modelRuntimes.llama-cpp.image.tag="$IMAGE_TAG" \
  --set-string modelRuntimes.vllm.image.repository="$IMAGE_NAMESPACE/vllm" \
  --set-string modelRuntimes.vllm.image.tag="$IMAGE_TAG" \
  --set-string modelRuntimes.sglang.image.repository="$IMAGE_NAMESPACE/sglang" \
  --set-string modelRuntimes.sglang.image.tag="$IMAGE_TAG"

helm upgrade --install inferops-gateway deploy/helm/inferops-gateway \
  --kube-context <cluster-context> \
  --namespace default \
  --create-namespace \
  --wait \
  --timeout 5m \
  --set-string image.repository="$IMAGE_NAMESPACE/inferops-gateway" \
  --set-string image.tag="$IMAGE_TAG"
```

Install the dashboard only when you want the browser UI:

```bash
helm upgrade --install inferops-dashboard deploy/helm/inferops-dashboard \
  --kube-context <cluster-context> \
  --namespace default \
  --create-namespace \
  --wait \
  --timeout 5m \
  --set-string image.repository="$IMAGE_NAMESPACE/inferops-dashboard" \
  --set-string image.tag="$IMAGE_TAG"
```

## OrbStack CPU Smoke Test

Use this path to verify a source build or published image set on a local
OrbStack cluster with the CPU llama.cpp example:

```bash
orb start k8s
kubectl config use-context orbstack

kubectl --context orbstack annotate node orbstack \
  inferops.dev/cache-capacity=2Gi \
  --overwrite
```

Create `/var/lib/inferops/models` on the OrbStack node. The helper Pod in
[`examples/yaml-deploy/LOCAL_BUILD.md`](../examples/yaml-deploy/LOCAL_BUILD.md)
does this for local clusters.

Install InferOps:

```bash
inferops install \
  --context orbstack \
  --profile homelab \
  --cache-path /var/lib/inferops/models
```

Apply and activate the CPU example:

```bash
kubectl --context orbstack apply \
  -f examples/yaml-deploy/modeldeployment-cpu.yaml

inferops status cpu-smollm --context orbstack
inferops activate cpu-smollm --context orbstack --timeout 10m
inferops status cpu-smollm --context orbstack --watch --timeout 10m
```

Call the model through the gateway:

```bash
inferops gateway forward --context orbstack

curl -X POST http://127.0.0.1:8080/models/cpu-smollm/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"cpu-smollm","messages":[{"role":"user","content":"Say hello from CPU llama.cpp."}]}'
```

If the deployment waits on cache placement, inspect the cache directly:

```bash
kubectl --context orbstack get modelcache
kubectl --context orbstack describe modelcache <cache-name>
```

`NoEligibleNode` usually means the node is missing
`inferops.dev/cache-capacity`, the node is not Ready/schedulable, or the
install was done with `--compute-profile nvidia-gpu` on a CPU-only cluster.
If a cluster was installed with the wrong compute profile, rerun
`inferops install` with the intended profile, then annotate the pending
`ModelCache` with `inferops.dev/retry="$(date +%s)"`.

## Private Packages

If the GHCR packages are private, create an image pull secret in the namespace
where the operator, gateway, dashboard, cache Jobs, and runtime pods run:

```bash
kubectl --context <cluster-context> create secret docker-registry ghcr-pull \
  --namespace default \
  --docker-server=ghcr.io \
  --docker-username="$GHCR_USER" \
  --docker-password="$GHCR_TOKEN"
```

Then pass the chart values that attach the secret to pods:

```bash
helm upgrade --install inferops-operator deploy/helm/inferops-operator \
  --kube-context <cluster-context> \
  --namespace default \
  --set imagePullSecrets[0].name=ghcr-pull

helm upgrade --install inferops-gateway deploy/helm/inferops-gateway \
  --kube-context <cluster-context> \
  --namespace default \
  --set imagePullSecrets[0].name=ghcr-pull

helm upgrade --install inferops-dashboard deploy/helm/inferops-dashboard \
  --kube-context <cluster-context> \
  --namespace default \
  --set imagePullSecrets[0].name=ghcr-pull
```

Those chart values cover the chart-managed operator, gateway, and dashboard
pods. The model downloader Jobs and generated runtime Deployments are created
later by the operator. Until those generated resources have a dedicated
InferOps pull-secret field, use one of these options for private downloader or
runtime images:

- make the runtime and downloader packages public;
- preload the images into the cluster node image store for local clusters;
- configure node-level registry credentials;
- attach the pull secret to the namespace's default ServiceAccount:

```bash
kubectl --context <cluster-context> patch serviceaccount default \
  --namespace default \
  --type merge \
  --patch '{"imagePullSecrets":[{"name":"ghcr-pull"}]}'
```

If model deployments run outside the install namespace, create the secret and
patch the default ServiceAccount in each workload namespace that needs to pull
private runtime images.

## Verify Pullability

From a machine that is not using your local Docker image cache, confirm the
registry has the pushed images:

```bash
docker pull "$IMAGE_NAMESPACE/inferops-operator:$IMAGE_TAG"
docker pull "$IMAGE_NAMESPACE/inferops-gateway:$IMAGE_TAG"
docker pull "$IMAGE_NAMESPACE/model-downloader:$IMAGE_TAG"
docker pull "$IMAGE_NAMESPACE/inferops-dashboard:$IMAGE_TAG"
docker pull "$IMAGE_NAMESPACE/llama-cpp:$IMAGE_TAG"
docker pull "$IMAGE_NAMESPACE/vllm:$IMAGE_TAG"
docker pull "$IMAGE_NAMESPACE/sglang:$IMAGE_TAG"
```

In Kubernetes, check the actual images on installed resources:

```bash
kubectl --context <cluster-context> get deploy inferops-operator \
  -o jsonpath='operator image={.spec.template.spec.containers[0].image}{"\n"}'

kubectl --context <cluster-context> get deploy inferops-gateway \
  -o jsonpath='gateway image={.spec.template.spec.containers[0].image}{"\n"}'

kubectl --context <cluster-context> get modelruntime llama-cpp \
  -o jsonpath='llama-cpp image={.spec.defaultImage}{"\n"}'

kubectl --context <cluster-context> get modelruntime vllm \
  -o jsonpath='vllm image={.spec.defaultImage}{"\n"}'

kubectl --context <cluster-context> get modelruntime sglang \
  -o jsonpath='sglang image={.spec.defaultImage}{"\n"}'
```
