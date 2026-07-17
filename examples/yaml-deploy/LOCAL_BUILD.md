# Build and Run Locally

Use this guide when you are forking InferOps, changing source code, and need a
local Kubernetes cluster to run images built from your checkout.

For the normal YAML deployment flow with packaged images, use
[YAML Deploy](README.md).

## What Needs an Image

InferOps is made of separate images. Build or publish the ones you changed:

| Component | Local image used in this guide | Required when |
| --- | --- | --- |
| Operator | `inferops-operator:dev` | You changed `operator/` or CRD/controller behavior |
| Gateway | `inferops-gateway:dev` | You changed `gateway/` or routing behavior |
| Model downloader | `inferops/model-downloader:dev` | You changed cache download behavior or need cache Jobs to use local code |
| llama.cpp runtime | `inferops/llama-cpp:dev` | You are testing the CPU llama.cpp example locally |
| vLLM runtime | `inferops/vllm:dev` | You are testing the GPU vLLM example with a local runtime image |
| Dashboard | `inferops-dashboard:dev` | You changed `dashboard/` or want the dashboard from source |

The Python CLI does not need a Go build. Install it with `uv` from the checkout.

## Prepare the CLI

From the repository root:

```bash
uv venv
. .venv/bin/activate
uv pip install -e sdk/python -e cli
inferops --help
```

Keep the virtual environment activated while running the commands below.

## Prepare OrbStack

Start Kubernetes and select the local context:

```bash
orb start k8s
kubectl config use-context orbstack
kubectl --context orbstack get nodes
```

This local build path uses raw Helm commands instead of `inferops install`, so
advertise cache capacity on the OrbStack node manually before installing:

```bash
kubectl --context orbstack annotate node orbstack \
  inferops.dev/cache-capacity=2Gi \
  --overwrite
```

Create the node-local cache directory once:

```bash
kubectl --context orbstack apply -f - <<'YAML'
apiVersion: v1
kind: Pod
metadata:
  name: inferops-cache-path-init
spec:
  restartPolicy: Never
  nodeName: orbstack
  containers:
    - name: init
      image: busybox:1.36.1
      command:
        - sh
        - -c
        - |
          mkdir -p /host/var/lib/inferops/models
          chown 65532:65532 /host/var/lib/inferops/models
          chmod 0750 /host/var/lib/inferops/models
      volumeMounts:
        - name: host-root
          mountPath: /host
  volumes:
    - name: host-root
      hostPath:
        path: /
        type: Directory
YAML

kubectl --context orbstack wait \
  --for=jsonpath='{.status.phase}'=Succeeded \
  pod/inferops-cache-path-init \
  --timeout=60s
kubectl --context orbstack delete pod inferops-cache-path-init
```

## Build Images

Build the control plane images:

```bash
make control-plane-images IMAGE_TAG=dev
```

Build the model downloader image:

```bash
make model-downloader-build IMAGE_TAG=dev
```

Build the CPU llama.cpp runtime image used by
`modeldeployment-cpu.yaml`:

```bash
docker build -f runtimes/llama_cpp/Dockerfile -t inferops/llama-cpp:dev .
```

Build the GPU vLLM runtime image used by
`modeldeployment-vllm-gpu.yaml`:

```bash
docker build -f runtimes/vllm/Dockerfile -t inferops/vllm:dev .
```

Build the dashboard image if you want the browser UI from source:

```bash
docker build -f dashboard/Dockerfile -t inferops-dashboard:dev .
```

OrbStack and Docker Desktop Kubernetes usually share the local Docker-compatible
image store. If your cluster does not, load the images into the cluster or push
them to a registry the cluster can pull from.

For kind:

```bash
kind load docker-image inferops-operator:dev
kind load docker-image inferops-gateway:dev
kind load docker-image inferops/model-downloader:dev
kind load docker-image inferops/llama-cpp:dev
kind load docker-image inferops/vllm:dev
kind load docker-image inferops-dashboard:dev
```

For minikube:

```bash
minikube image load inferops-operator:dev
minikube image load inferops-gateway:dev
minikube image load inferops/model-downloader:dev
minikube image load inferops/llama-cpp:dev
minikube image load inferops/vllm:dev
minikube image load inferops-dashboard:dev
```

For k3d:

```bash
k3d image import inferops-operator:dev
k3d image import inferops-gateway:dev
k3d image import inferops/model-downloader:dev
k3d image import inferops/llama-cpp:dev
k3d image import inferops/vllm:dev
k3d image import inferops-dashboard:dev
```

If you push to a registry instead, tag and push every local image that the
cluster will run, then replace the Helm image repositories and tags below with
that registry reference:

```bash
docker tag inferops-operator:dev registry.example.com/inferops/operator:dev
docker tag inferops-gateway:dev registry.example.com/inferops/gateway:dev
docker tag inferops/model-downloader:dev registry.example.com/inferops/model-downloader:dev
docker tag inferops/llama-cpp:dev registry.example.com/inferops/llama-cpp:dev
docker tag inferops/vllm:dev registry.example.com/inferops/vllm:dev
docker push registry.example.com/inferops/operator:dev
docker push registry.example.com/inferops/gateway:dev
docker push registry.example.com/inferops/model-downloader:dev
docker push registry.example.com/inferops/llama-cpp:dev
docker push registry.example.com/inferops/vllm:dev
```

## Install Local Images

Apply CRDs from the checkout, then install the operator and gateway with Helm
image overrides:

```bash
kubectl --context orbstack apply --server-side -f deploy/manifests/crds

helm upgrade --install inferops-operator deploy/helm/inferops-operator \
  --kube-context orbstack \
  --namespace default \
  --create-namespace \
  --wait \
  --timeout 5m \
  --values deploy/helm/inferops-operator/values-homelab.yaml \
  --set-string image.repository=inferops-operator \
  --set-string image.tag=dev \
  --set-string cache.downloaderImage=inferops/model-downloader:dev \
  --set-json 'cache.requiredNodeResources=[]' \
  --set-string gpu.required=false \
  --set-string modelRuntimes.llama-cpp.image.repository=inferops/llama-cpp \
  --set-string modelRuntimes.llama-cpp.image.tag=dev \
  --set-string modelRuntimes.vllm.image.repository=inferops/vllm \
  --set-string modelRuntimes.vllm.image.tag=dev \
  --set-string cache.root=/var/lib/inferops/models \
  --set-string profile=homelab

helm upgrade --install inferops-gateway deploy/helm/inferops-gateway \
  --kube-context orbstack \
  --namespace default \
  --create-namespace \
  --wait \
  --timeout 5m \
  --values deploy/helm/inferops-gateway/values-homelab.yaml \
  --set-string image.repository=inferops-gateway \
  --set-string image.tag=dev
```

The quoted `--set-json 'cache.requiredNodeResources=[]'` is intentional for
zsh. Without quotes, zsh treats `[]` as a filename pattern.

Check that Kubernetes is running your local images:

```bash
kubectl --context orbstack get deploy inferops-operator \
  -o jsonpath='operator image={.spec.template.spec.containers[0].image}{"\n"}'
kubectl --context orbstack get deploy inferops-gateway \
  -o jsonpath='gateway image={.spec.template.spec.containers[0].image}{"\n"}'
```

Check that the registered runtimes now point at your local runtime images:

```bash
kubectl --context orbstack get modelruntime llama-cpp \
  -o jsonpath='llama-cpp image={.spec.defaultImage}{"\n"}'
kubectl --context orbstack get modelruntime vllm \
  -o jsonpath='vllm image={.spec.defaultImage}{"\n"}'
```

The Helm overrides above are the easiest path because the example manifests can
stay unchanged. For one-off testing, you can also set the image directly on a
single deployment:

```yaml
spec:
  runtime:
    ref: llama-cpp
    image: inferops/llama-cpp:dev
```

```yaml
spec:
  runtime:
    ref: vllm
    image: inferops/vllm:dev
```

## Install the Local Dashboard

The dashboard is optional and read-only:

```bash
helm upgrade --install inferops-dashboard deploy/helm/inferops-dashboard \
  --kube-context orbstack \
  --namespace default \
  --create-namespace \
  --wait \
  --timeout 5m \
  --set-string image.repository=inferops-dashboard \
  --set-string image.tag=dev \
  --set-string dashboard.gatewayBaseURL=http://127.0.0.1:8080

kubectl --context orbstack port-forward svc/inferops-dashboard 8088:8080
```

Open `http://127.0.0.1:8088`.

## Run the CPU Example

Apply and activate the CPU llama.cpp deployment:

```bash
kubectl --context orbstack apply -f examples/yaml-deploy/modeldeployment-cpu.yaml
inferops status cpu-smollm --context orbstack
inferops activate cpu-smollm --context orbstack --timeout 10m
inferops status cpu-smollm --context orbstack --watch --timeout 10m
```

The status output should include a `RuntimeResolved` condition that references
`inferops/llama-cpp:dev`.

When the deployment is `Active`, call it through the gateway:

```bash
inferops gateway forward --context orbstack

curl -X POST http://127.0.0.1:8080/models/cpu-smollm/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"cpu-smollm","messages":[{"role":"user","content":"Say hello from local InferOps."}]}'
```

## Run the GPU vLLM Example

The GPU vLLM image wraps the upstream CUDA vLLM server. Run this path on a
Linux GPU node with NVIDIA drivers, a container runtime that can expose GPUs,
and the NVIDIA Kubernetes device plugin installed. OrbStack on macOS is useful
for CPU smoke tests, but it is not a CUDA GPU cluster.

Make sure the GPU node has enough advertised cache capacity for the example
model:

```bash
kubectl --context <gpu-context> annotate node <gpu-node-name> \
  inferops.dev/cache-capacity=80Gi \
  --overwrite
```

Ensure `/var/lib/inferops/models` exists on that node, then apply CRDs and
install or upgrade the operator in that GPU cluster with the vLLM image
override:

```bash
kubectl --context <gpu-context> apply --server-side -f deploy/manifests/crds

helm upgrade --install inferops-operator deploy/helm/inferops-operator \
  --kube-context <gpu-context> \
  --namespace default \
  --create-namespace \
  --wait \
  --timeout 5m \
  --values deploy/helm/inferops-operator/values-homelab.yaml \
  --set-string image.repository=inferops-operator \
  --set-string image.tag=dev \
  --set-string cache.downloaderImage=inferops/model-downloader:dev \
  --set-json 'cache.requiredNodeResources=["nvidia.com/gpu"]' \
  --set-string gpu.required=true \
  --set-string modelRuntimes.vllm.image.repository=inferops/vllm \
  --set-string modelRuntimes.vllm.image.tag=dev \
  --set-string cache.root=/var/lib/inferops/models \
  --set-string profile=homelab
```

Install or upgrade the gateway in the same cluster:

```bash
helm upgrade --install inferops-gateway deploy/helm/inferops-gateway \
  --kube-context <gpu-context> \
  --namespace default \
  --create-namespace \
  --wait \
  --timeout 5m \
  --values deploy/helm/inferops-gateway/values-homelab.yaml \
  --set-string image.repository=inferops-gateway \
  --set-string image.tag=dev
```

If the GPU cluster cannot see your local Docker images, push them to a registry
and use those registry repositories in the Helm overrides.

Apply and activate the GPU deployment:

```bash
inferops doctor --context <gpu-context> --check kubernetes-api --check device-plugin --check gpu-capacity
inferops gpu list --context <gpu-context>
kubectl --context <gpu-context> apply -f examples/yaml-deploy/modeldeployment-vllm-gpu.yaml
inferops activate gpu-vllm-qwen --context <gpu-context> --timeout 20m
inferops status gpu-vllm-qwen --context <gpu-context> --watch --timeout 20m
```

The status output should include a `RuntimeResolved` condition that references
`inferops/vllm:dev`.

Call it through the gateway:

```bash
inferops gateway forward --context <gpu-context>

curl -X POST http://127.0.0.1:8080/models/gpu-vllm-qwen/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"gpu-vllm-qwen","messages":[{"role":"user","content":"Give me one Kubernetes GPU scheduling tip."}]}'
```

## Rebuild After Code Changes

After changing Go code, rebuild the affected image with a new tag:

```bash
make control-plane-images IMAGE_TAG=runtime-ready-fix-1
```

Then upgrade only the changed component:

```bash
helm upgrade --install inferops-operator deploy/helm/inferops-operator \
  --kube-context orbstack \
  --namespace default \
  --wait \
  --timeout 5m \
  --values deploy/helm/inferops-operator/values-homelab.yaml \
  --set-string image.repository=inferops-operator \
  --set-string image.tag=runtime-ready-fix-1 \
  --set-string cache.downloaderImage=inferops/model-downloader:dev \
  --set-json 'cache.requiredNodeResources=[]' \
  --set-string gpu.required=false \
  --set-string modelRuntimes.llama-cpp.image.repository=inferops/llama-cpp \
  --set-string modelRuntimes.llama-cpp.image.tag=dev \
  --set-string modelRuntimes.vllm.image.repository=inferops/vllm \
  --set-string modelRuntimes.vllm.image.tag=dev \
  --set-string cache.root=/var/lib/inferops/models \
  --set-string profile=homelab
```

Use non-`latest` tags for local iteration so Kubernetes pulls or restarts the
image you intended.

## Troubleshooting

If a pod reports `ImagePullBackOff`, the cluster cannot see the image. Load it
into the cluster image store or push it to a registry and update the Helm image
override.

If a `ModelCache` remains `Pending` with `NoEligibleNode`, check the node cache
annotation:

```bash
kubectl --context orbstack get node orbstack \
  -o jsonpath='{.metadata.annotations.inferops\.dev/cache-capacity}{"\n"}'
```

If a cache Job cannot mount the cache path, recreate the cache directory with
the `inferops-cache-path-init` pod above.

If a deployment is `Active` but calls fail, inspect the gateway and runtime:

```bash
inferops status cpu-smollm --context orbstack
inferops logs cpu-smollm --context orbstack --tail 100
kubectl --context orbstack logs deploy/inferops-gateway
kubectl --context orbstack get pods,svc,endpointslices
```
