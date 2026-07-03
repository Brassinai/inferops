# Manually Deploying a nano-vLLM Model on Kubernetes

This note is written as a practical deployment story for InferOps.

Forget the SDK for now. Imagine we already have three things:

1. A Linux GPU node.
2. A model already present on disk.
3. A nano-vLLM container image that knows how to serve an OpenAI-compatible API.

The thing we want to understand is the Kubernetes workflow that InferOps is
trying to automate.

```txt
GPU host
  -> k3s
  -> NVIDIA container runtime
  -> NVIDIA device plugin
  -> InferOps CRDs
  -> ModelRuntime + ModelCache + ModelDeployment
  -> runtime Deployment + Service
  -> gateway or port-forward
  -> OpenAI-compatible request
```

I kept it honest about the current repo state: the CRDs, charts, and gateway
routing exist, but some operator automation is still early, so the tutorial
shows both the manual Kubernetes shape and what the operator owns.

That honesty is part of the point of the tutorial. It should help a reader or
contributor understand the landscape of the project before touching code. If
you know what Kubernetes objects are needed by hand, you can contribute to the
operator, gateway, Helm charts, docs, tests, or examples without guessing where
your change belongs.

## Current Project Landscape

InferOps is not only one binary. It is a set of contracts and components that
should eventually work together.

```txt
CRDs
  -> define the public InferOps API

ModelRuntime
  -> describes a reusable serving engine such as nano-vLLM

ModelCache
  -> describes where model files live and whether they are ready

ModelDeployment
  -> describes the endpoint a user wants

Operator
  -> should reconcile ModelDeployment into Deployments, Services, cache checks,
     placement decisions, and status conditions

Gateway
  -> should route /models/<name>/v1/... traffic to the correct ready runtime
     Service

Helm charts
  -> package the operator, gateway, and runtime resources for repeatable install
```

The repository already has the API shapes and early packaging:

- CRD manifests live in `deploy/manifests/crds/`.
- Contract examples live in `deploy/manifests/examples/contracts/`.
- Helm charts live in `deploy/helm/`.
- Operator package boundaries live under `operator/`.
- Gateway package boundaries live under `gateway/`.

The important missing behavior is full reconciliation. Today, applying a
`ModelDeployment` records the desired state in Kubernetes, but it does not yet
perform the full workflow of creating the runtime workload, wiring the Service,
checking the cache, updating status, and enabling gateway routing.

That is why this tutorial does the work manually. Each command is a future
operator or gateway responsibility made visible.

## What We Are Building

We will run one model called `qwen-chat`.

The runtime pod will:

- Mount an existing model cache from `/var/lib/inferops/models/qwen-chat`.
- Request one whole NVIDIA GPU.
- Start the nano-vLLM engine's own server on port `8000`.
- Expose the pod through a stable Kubernetes Service.
- Optionally route traffic through a simple gateway-style reverse proxy.

The InferOps custom resources will describe the same thing at a higher level:

- `ModelRuntime`: what nano-vLLM is.
- `ModelCache`: where the model is stored.
- `ModelDeployment`: what model endpoint the user wants.

In a finished InferOps control plane, creating the `ModelDeployment` should be
enough. For now, we will create both the custom resources and the ordinary
Kubernetes objects so the full shape is visible.

InferOps does not implement the inference server or construct nano-vLLM engine
classes. The nano-vLLM release pipeline builds the engine image and owns its
OpenAI API, health, metrics, and shutdown behavior. The InferOps runtime image
adds only a small environment-to-CLI entrypoint.

## Host Layout

On the GPU node, assume the model already exists here:

```txt
/var/lib/inferops/models/qwen-chat
```

For example:

```bash
sudo mkdir -p /var/lib/inferops/models/qwen-chat
sudo chown -R 1000:1000 /var/lib/inferops/models/qwen-chat
ls -lah /var/lib/inferops/models/qwen-chat
```

The exact files depend on the model and runtime, but you should expect things
like tokenizer files, config files, and weight files.

```txt
config.json
generation_config.json
tokenizer.json
tokenizer_config.json
model.safetensors
```

The important idea is this: Kubernetes will not download the model for us in
this manual path. We are giving the runtime pod a mounted directory that already
contains the model.

## Step 1: Verify The GPU Host

Before Kubernetes enters the picture, the host must see the GPU.

```bash
nvidia-smi
```

If this fails, stop here. Kubernetes cannot schedule a GPU workload on a host
that does not have a working NVIDIA driver.

The InferOps quickstart should not guess how to install host drivers because the
right command depends on the Linux distribution, driver branch, Secure Boot,
kernel version, and GPU family. Use your distro package manager and the official
NVIDIA driver docs for that part.

## Step 2: Install The NVIDIA Container Toolkit

The NVIDIA driver lets the host use the GPU. The NVIDIA Container Toolkit lets
containers use it.

For Ubuntu or Debian-derived systems, the official NVIDIA docs currently show
this repository setup pattern:

```bash
sudo apt-get update
sudo apt-get install -y --no-install-recommends ca-certificates curl gnupg2

curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey \
  | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg

curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list \
  | sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' \
  | sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list

sudo apt-get update
sudo apt-get install -y nvidia-container-toolkit
```

For a normal containerd installation, NVIDIA documents this configuration step:

```bash
sudo nvidia-ctk runtime configure --runtime=containerd
sudo systemctl restart containerd
```

k3s is slightly different because it ships and manages its own containerd
configuration under `/var/lib/rancher/k3s`. The practical rule is:

1. Install the NVIDIA runtime packages before installing k3s when possible.
2. If k3s is already installed, restart k3s after installing the NVIDIA runtime.
3. Verify that k3s detected the runtime.

```bash
sudo systemctl restart k3s
sudo grep nvidia /var/lib/rancher/k3s/agent/etc/containerd/config.toml
```

You want to see NVIDIA runtime entries in the generated containerd config.

## Step 3: Install k3s

For a single-node homelab cluster:

```bash
curl -sfL https://get.k3s.io | sh -
```

Configure your shell:

```bash
mkdir -p ~/.kube
sudo cp /etc/rancher/k3s/k3s.yaml ~/.kube/config
sudo chown "$USER":"$USER" ~/.kube/config
kubectl get nodes
```

Check whether k3s created an NVIDIA runtime class:

```bash
kubectl get runtimeclass
```

Some clusters expose a `RuntimeClass` named `nvidia`; some only need the device
plugin and extended resource. In the manifests below, `runtimeClassName:
nvidia` is shown as optional. Keep it if your cluster has that runtime class.
Remove it if your cluster does not.

## Step 4: Install The NVIDIA Kubernetes Device Plugin

The container runtime lets a container use GPUs. The Kubernetes device plugin
advertises GPUs to the scheduler as allocatable resources.

```bash
helm repo add nvdp https://nvidia.github.io/k8s-device-plugin
helm repo update

helm upgrade -i nvdp nvdp/nvidia-device-plugin \
  --namespace nvidia-device-plugin \
  --create-namespace \
  --set runtimeClassName=nvidia \
  --set gfd.enabled=true
```

If your cluster does not have a `RuntimeClass` called `nvidia`, omit this value:

```bash
helm upgrade -i nvdp nvdp/nvidia-device-plugin \
  --namespace nvidia-device-plugin \
  --create-namespace \
  --set gfd.enabled=true
```

Now check whether Kubernetes sees the GPU:

```bash
kubectl get nodes -o custom-columns=NAME:.metadata.name,GPU:.status.allocatable.nvidia\\.com/gpu
```

Expected shape:

```txt
NAME         GPU
gpu-node-1   1
```

The number is the number of whole GPUs Kubernetes can allocate on that node.
InferOps starts with whole-GPU scheduling, not slicing.

Run a simple GPU pod test:

```bash
kubectl run cuda-test \
  --rm \
  -it \
  --restart=Never \
  --image=nvidia/cuda:12.4.1-base-ubuntu22.04 \
  --limits=nvidia.com/gpu=1 \
  -- nvidia-smi
```

If this works, the cluster can schedule GPU workloads.

## Step 5: Create The InferOps Namespace

```bash
kubectl create namespace inferops
```

Label the GPU node so our manual runtime pod lands on the node that has the
model cache.

```bash
kubectl get nodes
kubectl label node gpu-node-1 inferops.dev/gpu-node=true
```

Replace `gpu-node-1` with your real node name.

## Step 6: Install The InferOps CRDs

From the repository root:

```bash
kubectl apply -f deploy/manifests/crds/modelruntimes.yaml
kubectl apply -f deploy/manifests/crds/modelcaches.yaml
kubectl apply -f deploy/manifests/crds/modeldeployments.yaml
```

Check that Kubernetes learned the new nouns:

```bash
kubectl api-resources | grep inference.inferops.dev
```

Expected shape:

```txt
modelcaches        mcache     inference.inferops.dev/v1alpha1   true   ModelCache
modeldeployments   mdeploy    inference.inferops.dev/v1alpha1   true   ModelDeployment
modelruntimes      mruntime   inference.inferops.dev/v1alpha1   true   ModelRuntime
```

This is the first big control-plane idea. Kubernetes did not know what a model
deployment was. The CRD taught it a new API shape.

## Step 7: Declare The nano-vLLM Runtime

`ModelRuntime` describes the reusable runtime contract.

```bash
kubectl apply -n inferops -f - <<'YAML'
apiVersion: inference.inferops.dev/v1alpha1
kind: ModelRuntime
metadata:
  name: nano-vllm
spec:
  engine: nano-vllm
  protocol: openai
  defaultImage: ghcr.io/your-org/inferops-runtime:nano-vllm
  port: 8000
  healthPath: /health
  readinessPath: /health
  metricsPath: /metrics
YAML
```

Check it:

```bash
kubectl get modelruntime -n inferops
kubectl describe modelruntime nano-vllm -n inferops
```

This object does not start a pod by itself. It says: when a deployment asks for
`runtime.ref: nano-vllm`, this is the runtime contract to use.

## Step 8: Declare The Model Cache

`ModelCache` records where the model lives.

```bash
kubectl apply -n inferops -f - <<'YAML'
apiVersion: inference.inferops.dev/v1alpha1
kind: ModelCache
metadata:
  name: qwen-chat-cache
spec:
  modelRepo: Qwen/Qwen2.5-7B-Instruct
  revision: main
  storage:
    type: nodeLocal
    size: 100Gi
    nodeName: gpu-node-1
    path: /var/lib/inferops/models/qwen-chat
YAML
```

Check it:

```bash
kubectl get modelcache -n inferops
kubectl describe modelcache qwen-chat-cache -n inferops
```

In the future, the operator can make this object active by checking the cache,
downloading missing files, writing status, and deciding whether the model is
ready. In the manual path, it is a declaration and a debugging handle.

## Step 9: Declare The Model Deployment

`ModelDeployment` is the user-facing object. It says:

```txt
I want this model, using this runtime, with this resource shape,
cache behavior, activation state, and route.
```

```bash
kubectl apply -n inferops -f - <<'YAML'
apiVersion: inference.inferops.dev/v1alpha1
kind: ModelDeployment
metadata:
  name: qwen-chat
spec:
  model:
    name: qwen-chat
    source: huggingface
    repo: Qwen/Qwen2.5-7B-Instruct
    revision: main
  runtime:
    ref: nano-vllm
    maxModelLen: 4096
    tensorParallelSize: 1
    gpuMemoryUtilization: 0.85
  resources:
    cpu: "8"
    memory: 32Gi
    gpu:
      count: 1
      vendor: nvidia
  activation:
    desiredState: Active
    whenFull: Queue
    priority: 50
    drainTimeout: 5m
  scaling:
    minReplicas: 0
    maxReplicas: 1
  routing:
    enabled: true
    path: /models/qwen-chat
    openAICompatible: true
  cache:
    enabled: true
    type: nodeLocal
    size: 100Gi
    path: /var/lib/inferops/models
YAML
```

Check it:

```bash
kubectl get modeldeployment -n inferops
kubectl describe modeldeployment qwen-chat -n inferops
```

Because the operator is not fully reconciling yet, this object will not create
the runtime `Deployment` and `Service` for us. That is the next manual step.

## Step 10: Create The Runtime Deployment Manually

This is the ordinary Kubernetes workload that the future operator should
generate from the `ModelDeployment`.

Replace `ghcr.io/your-org/inferops-runtime:nano-vllm` with the thin runtime
image built from `runtimes/nano_vllm/Dockerfile`. Its `NANOVLLM_IMAGE` build
argument must reference the engine image produced by the nano-vLLM project.

```bash
kubectl apply -n inferops -f - <<'YAML'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: qwen-chat-runtime
  labels:
    app.kubernetes.io/name: qwen-chat-runtime
    app.kubernetes.io/part-of: inferops
    inferops.dev/modeldeployment: qwen-chat
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: qwen-chat-runtime
      inferops.dev/modeldeployment: qwen-chat
  template:
    metadata:
      labels:
        app.kubernetes.io/name: qwen-chat-runtime
        app.kubernetes.io/part-of: inferops
        inferops.dev/modeldeployment: qwen-chat
    spec:
      nodeSelector:
        inferops.dev/gpu-node: "true"
      runtimeClassName: nvidia
      terminationGracePeriodSeconds: 330
      containers:
        - name: runtime
          image: ghcr.io/your-org/inferops-runtime:nano-vllm
          imagePullPolicy: IfNotPresent
          env:
            - name: MODEL_REPO
              value: Qwen/Qwen2.5-7B-Instruct
            - name: MODEL_PATH
              value: /models/qwen-chat
            - name: MAX_MODEL_LEN
              value: "4096"
            - name: TENSOR_PARALLEL_SIZE
              value: "1"
            - name: GPU_MEMORY_UTILIZATION
              value: "0.85"
            - name: PORT
              value: "8000"
          ports:
            - name: http
              containerPort: 8000
          readinessProbe:
            httpGet:
              path: /health
              port: http
            periodSeconds: 5
            failureThreshold: 12
          livenessProbe:
            httpGet:
              path: /health
              port: http
            periodSeconds: 10
            failureThreshold: 3
          startupProbe:
            httpGet:
              path: /health
              port: http
            periodSeconds: 10
            failureThreshold: 60
          resources:
            requests:
              cpu: "8"
              memory: 32Gi
              nvidia.com/gpu: "1"
            limits:
              cpu: "8"
              memory: 32Gi
              nvidia.com/gpu: "1"
          volumeMounts:
            - name: model-cache
              mountPath: /models/qwen-chat
              readOnly: true
      volumes:
        - name: model-cache
          hostPath:
            path: /var/lib/inferops/models/qwen-chat
            type: Directory
YAML
```

If your cluster has no `RuntimeClass` named `nvidia`, remove this line before
applying:

```yaml
runtimeClassName: nvidia
```

Watch the pod:

```bash
kubectl get pods -n inferops -w
```

Debug it:

```bash
kubectl describe pod -n inferops -l inferops.dev/modeldeployment=qwen-chat
kubectl logs -n inferops -l inferops.dev/modeldeployment=qwen-chat -c runtime
```

If the pod stays `Pending`, check scheduling:

```bash
kubectl describe pod -n inferops -l inferops.dev/modeldeployment=qwen-chat
kubectl get nodes -o custom-columns=NAME:.metadata.name,GPU:.status.allocatable.nvidia\\.com/gpu
```

Common problems:

- The node label does not match.
- The GPU is already allocated to another pod.
- The NVIDIA device plugin is not running.
- The runtime class name does not exist.
- The hostPath model directory exists on a different node.

## Step 11: Create A Stable Runtime Service

Pods are replaceable. Services are stable.

```bash
kubectl apply -n inferops -f - <<'YAML'
apiVersion: v1
kind: Service
metadata:
  name: qwen-chat-runtime
  labels:
    app.kubernetes.io/name: qwen-chat-runtime
    app.kubernetes.io/part-of: inferops
    inferops.dev/modeldeployment: qwen-chat
spec:
  type: ClusterIP
  selector:
    app.kubernetes.io/name: qwen-chat-runtime
    inferops.dev/modeldeployment: qwen-chat
  ports:
    - name: http
      port: 8000
      targetPort: http
YAML
```

Check endpoints:

```bash
kubectl get svc qwen-chat-runtime -n inferops
kubectl get endpoints qwen-chat-runtime -n inferops
```

If endpoints are empty, the pod is not ready or the selector labels do not
match.

Test from your machine with a port-forward:

```bash
kubectl port-forward -n inferops svc/qwen-chat-runtime 8000:8000
```

In another terminal:

```bash
curl http://127.0.0.1:8000/health
```

Try an OpenAI-compatible request:

```bash
curl http://127.0.0.1:8000/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "qwen-chat",
    "messages": [
      {"role": "user", "content": "Explain Kubernetes Services in one paragraph."}
    ],
    "max_tokens": 128
  }'
```

At this point, Kubernetes is running the model.

## What The Manual Runtime Deployment Means

This manual `Deployment` is the low-level version of the `ModelDeployment`.

The relationship looks like this:

```txt
ModelDeployment.spec.model.repo
  -> MODEL_REPO

ModelDeployment.spec.runtime.maxModelLen
  -> MAX_MODEL_LEN

ModelDeployment.spec.resources.gpu.count
  -> resources.requests["nvidia.com/gpu"]

ModelDeployment.spec.cache.path + model name
  -> hostPath volume

ModelDeployment.metadata.name
  -> qwen-chat-runtime Service name
```

So the operator's job is not magic. It will read the high-level custom resource
and create stable Kubernetes resources with clear names, labels, probes,
volumes, and resource requests.

## Step 12: Add A Simple Gateway Manually

The InferOps gateway accepts:

```txt
/models/qwen-chat/v1/chat/completions
```

and forward it to:

```txt
qwen-chat-runtime.inferops.svc.cluster.local:8000/v1/chat/completions
```

The following small NGINX Deployment remains a useful way to understand and
debug the networking shape without discovery or lifecycle filtering.

Create a reverse proxy config:

```bash
kubectl apply -n inferops -f - <<'YAML'
apiVersion: v1
kind: ConfigMap
metadata:
  name: inferops-gateway-nginx
data:
  default.conf: |
    server {
      listen 8080;

      location /models/qwen-chat/ {
        rewrite ^/models/qwen-chat/(.*)$ /$1 break;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_pass http://qwen-chat-runtime.inferops.svc.cluster.local:8000;
      }
    }
YAML
```

Create the gateway workload and service:

```bash
kubectl apply -n inferops -f - <<'YAML'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: inferops-gateway-manual
  labels:
    app.kubernetes.io/name: inferops-gateway-manual
    app.kubernetes.io/part-of: inferops
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: inferops-gateway-manual
  template:
    metadata:
      labels:
        app.kubernetes.io/name: inferops-gateway-manual
        app.kubernetes.io/part-of: inferops
    spec:
      containers:
        - name: nginx
          image: nginx:1.27-alpine
          ports:
            - name: http
              containerPort: 8080
          volumeMounts:
            - name: config
              mountPath: /etc/nginx/conf.d
              readOnly: true
      volumes:
        - name: config
          configMap:
            name: inferops-gateway-nginx
---
apiVersion: v1
kind: Service
metadata:
  name: inferops-gateway-manual
  labels:
    app.kubernetes.io/name: inferops-gateway-manual
    app.kubernetes.io/part-of: inferops
spec:
  type: ClusterIP
  selector:
    app.kubernetes.io/name: inferops-gateway-manual
  ports:
    - name: http
      port: 80
      targetPort: http
YAML
```

Test the gateway:

```bash
kubectl port-forward -n inferops svc/inferops-gateway-manual 8080:80
```

In another terminal:

```bash
curl http://127.0.0.1:8080/models/qwen-chat/health
```

OpenAI-compatible request through the gateway:

```bash
curl http://127.0.0.1:8080/models/qwen-chat/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "qwen-chat",
    "messages": [
      {"role": "user", "content": "Say hello from inside Kubernetes."}
    ],
    "max_tokens": 64
  }'
```

This is the data-plane flow:

```txt
curl
  -> port-forward
  -> inferops-gateway-manual Service
  -> NGINX pod
  -> qwen-chat-runtime Service
  -> nano-vLLM pod
```

The packaged gateway discovers active `ModelDeployment`, Service, and
EndpointSlice objects and routes only to ready runtime endpoints. The manual
NGINX proxy is here to make the network path concrete.

## Step 13: Expose The Gateway With Tailscale

For a private homelab, Tailscale is a good fit because you can expose the
gateway to your tailnet without opening a public cloud load balancer.

Install the Tailscale Kubernetes Operator with Helm:

```bash
helm repo add tailscale https://pkgs.tailscale.com/helmcharts
helm repo update

helm upgrade \
  --install \
  tailscale-operator \
  tailscale/tailscale-operator \
  --namespace=tailscale \
  --create-namespace \
  --set-string oauth.clientId="<OAuth client ID>" \
  --set-string oauth.clientSecret="<OAuth client secret>" \
  --wait
```

Do not commit the OAuth credentials. Put them in your shell history only if
that is acceptable for your environment; otherwise use a values file stored
outside the repository or a secret management workflow.

After the operator is installed, expose the manual gateway through a Tailscale
`LoadBalancer` Service. This keeps the example at layer 3: Tailscale gives you
a private tailnet address, and the Service still forwards to the same gateway
pods inside the cluster.

```bash
kubectl apply -n inferops -f - <<'YAML'
apiVersion: v1
kind: Service
metadata:
  name: inferops-gateway-tailnet
  annotations:
    tailscale.com/hostname: inferops-gateway
spec:
  type: LoadBalancer
  loadBalancerClass: tailscale
  selector:
    app.kubernetes.io/name: inferops-gateway-manual
  ports:
    - name: http
      port: 80
      targetPort: http
YAML
```

Then check:

```bash
kubectl get svc -n inferops
kubectl describe svc inferops-gateway-tailnet -n inferops
```

If you do not use Tailscale yet, keep using `kubectl port-forward` while you
are learning. It is boring, but very clear.

## Step 14: Package The Runtime With The Existing Helm Chart

The repository already has a runtime Helm chart:

```txt
deploy/helm/inferops-runtime
```

The chart creates a `Deployment` and `Service` and mounts an existing
node-local model cache using `cache.hostPath`. The cache directory must be
prepared before installation, and the caller must ensure the pod is placed on
the node that owns that path.

Render it first:

```bash
helm template qwen-chat-runtime ./deploy/helm/inferops-runtime \
  --namespace inferops \
  --set image.repository=ghcr.io/your-org/inferops-runtime \
  --set image.tag=nano-vllm \
  --set model.repo=Qwen/Qwen2.5-7B-Instruct \
  --set model.path=/models/qwen-chat \
  --set cache.hostPath=/var/lib/inferops/models/qwen-chat \
  --set runtime.maxModelLen=4096 \
  --set runtime.tensorParallelSize=1 \
  --set runtime.gpuMemoryUtilization=0.85 \
  --set resources.requests.cpu=8 \
  --set resources.requests.memory=32Gi \
  --set 'resources.requests.nvidia\.com/gpu=1' \
  --set resources.limits.cpu=8 \
  --set resources.limits.memory=32Gi \
  --set 'resources.limits.nvidia\.com/gpu=1'
```

Install it:

```bash
helm upgrade -i qwen-chat-runtime ./deploy/helm/inferops-runtime \
  --namespace inferops \
  --set image.repository=ghcr.io/your-org/inferops-runtime \
  --set image.tag=nano-vllm \
  --set model.repo=Qwen/Qwen2.5-7B-Instruct \
  --set model.path=/models/qwen-chat \
  --set cache.hostPath=/var/lib/inferops/models/qwen-chat \
  --set runtime.maxModelLen=4096 \
  --set runtime.tensorParallelSize=1 \
  --set runtime.gpuMemoryUtilization=0.85 \
  --set resources.requests.cpu=8 \
  --set resources.requests.memory=32Gi \
  --set 'resources.requests.nvidia\.com/gpu=1' \
  --set resources.limits.cpu=8 \
  --set resources.limits.memory=32Gi \
  --set 'resources.limits.nvidia\.com/gpu=1'
```

Check the release:

```bash
helm status qwen-chat-runtime -n inferops
kubectl get deploy,svc,pods -n inferops
```

For the chart to fully replace the manual manifest, it should grow values for:

- `nodeSelector`
- `runtimeClassName`
- alternative model cache volume types such as PVCs
- pod labels tied to `ModelDeployment`

That is a good next implementation task because it turns the manual runtime
shape into reusable packaging.

## Step 15: Package The Gateway With Helm

The repository also has a gateway chart:

```txt
deploy/helm/inferops-gateway
```

Render it:

```bash
helm template inferops-gateway ./deploy/helm/inferops-gateway \
  --namespace inferops \
  --set image.repository=ghcr.io/inferops/inferops-gateway \
  --set image.tag=0.0.0 \
  --set service.type=ClusterIP \
  --set service.port=80
```

Install it:

```bash
helm upgrade -i inferops-gateway ./deploy/helm/inferops-gateway \
  --namespace inferops \
  --set image.repository=ghcr.io/inferops/inferops-gateway \
  --set image.tag=0.0.0 \
  --set service.type=ClusterIP \
  --set service.port=80
```

Check it:

```bash
kubectl get deploy,svc -n inferops -l app.kubernetes.io/name=inferops-gateway
```

The Go gateway discovers `ModelDeployment`, runtime Service, and EndpointSlice
objects in its namespace. It routes only current, ready `Active` deployments
with ready endpoints and immediately rejects newly admitted requests after
observing a drain transition. The manual NGINX gateway above remains useful as
a transparent debugging example.

## Step 16: Install The Operator Chart

The operator chart deploys the manager process:

```bash
helm template inferops-operator ./deploy/helm/inferops-operator \
  --namespace inferops-system
```

Install it:

```bash
helm upgrade -i inferops-operator ./deploy/helm/inferops-operator \
  --namespace inferops-system \
  --create-namespace
```

Check it:

```bash
kubectl get deploy,pods,serviceaccount -n inferops-system
kubectl logs -n inferops-system deploy/inferops-operator
```

The operator currently does not complete the full deployment workflow. The
target behavior is:

```txt
watch ModelDeployment
  -> validate runtime and cache
  -> decide placement
  -> create/update Deployment
  -> create/update Service
  -> update status conditions
  -> enable gateway route
```

That is the heart of InferOps.

## Step 17: The Same Workflow As An Operator Reconcile Loop

The manual commands above are useful because each one maps to a future
controller action.

```txt
kubectl apply CRDs
  -> platform install step

kubectl apply ModelRuntime
  -> runtime catalog

kubectl apply ModelCache
  -> cache controller verifies model files

kubectl apply ModelDeployment
  -> modeldeployment controller starts reconciliation

kubectl apply Deployment
  -> operator creates runtime workload

kubectl apply Service
  -> operator creates stable endpoint

kubectl apply gateway route
  -> gateway routes active model traffic
```

The controller's loop should keep asking:

```txt
What did the user ask for?
What exists now?
What small, safe change moves the cluster closer?
What status should I report?
```

That is Kubernetes-native thinking.

## Step 18: Debugging Commands You Will Actually Use

Check custom resources:

```bash
kubectl get modelruntime,modelcache,modeldeployment -n inferops
kubectl describe modeldeployment qwen-chat -n inferops
```

Check workloads:

```bash
kubectl get deploy,pods,svc,endpoints -n inferops
```

Check pod scheduling:

```bash
kubectl describe pod -n inferops -l inferops.dev/modeldeployment=qwen-chat
```

Check GPU capacity:

```bash
kubectl get nodes -o custom-columns=NAME:.metadata.name,GPU:.status.allocatable.nvidia\\.com/gpu
kubectl describe node gpu-node-1
```

Check runtime logs:

```bash
kubectl logs -n inferops -l inferops.dev/modeldeployment=qwen-chat -c runtime --tail=200
```

Check service routing:

```bash
kubectl get endpoints qwen-chat-runtime -n inferops
kubectl port-forward -n inferops svc/qwen-chat-runtime 8000:8000
curl http://127.0.0.1:8000/health
```

Check Helm output before installing:

```bash
helm template qwen-chat-runtime ./deploy/helm/inferops-runtime --namespace inferops
helm lint ./deploy/helm/inferops-runtime
```

Run the repository verification:

```bash
make verify
```

## What To Learn From This

The practical Kubernetes pieces for InferOps are:

- CRDs teach Kubernetes the InferOps API.
- `ModelDeployment` is the desired state.
- The operator should turn desired state into ordinary Kubernetes objects.
- A runtime `Deployment` runs the model server.
- A runtime `Service` gives the model a stable in-cluster address.
- GPU scheduling is just a resource request: `nvidia.com/gpu: "1"`.
- Model caching is storage plus placement. The pod must run where the model
  files exist, unless the cache is on shared storage.
- The gateway is data-plane routing. It should only send traffic to ready
  runtime Services.
- Helm packages repeatable installs, but the rendered YAML still needs to be
  understandable.

The full InferOps workflow should eventually feel like this:

```bash
kubectl apply -f modeldeployment.yaml
kubectl get modeldeployment qwen-chat -n inferops
curl http://gateway/models/qwen-chat/v1/chat/completions
```

But underneath that simple path are the exact Kubernetes pieces we walked
through manually.

## Where Contributors Can Help

This tutorial is also a map of useful work.

If you want to work on the operator, start by turning the manual runtime
`Deployment` and `Service` into controller output. A good first target is:

```txt
When ModelDeployment qwen-chat has activation.desiredState=Active,
create or update qwen-chat-runtime Deployment and qwen-chat-runtime Service.
```

The controller should use stable names, predictable labels, explicit resource
requests, readiness probes, and safe update behavior. It should also write
useful status conditions so a user can run:

```bash
kubectl describe modeldeployment qwen-chat -n inferops
```

and understand whether the model is waiting for cache, waiting for GPU,
starting, ready, or failed.

If you want to work on model caching, start from the `ModelCache` object. The
manual tutorial assumes the files already exist at:

```txt
/var/lib/inferops/models/qwen-chat
```

A useful cache controller would verify that path, check revision metadata,
eventually download missing model files, and report `Pending`, `Downloading`,
`Ready`, or `Failed` through status conditions.

If you want to work on scheduling, start from the gap between:

```yaml
resources:
  gpu:
    count: 1
```

and:

```yaml
resources:
  requests:
    nvidia.com/gpu: "1"
  limits:
    nvidia.com/gpu: "1"
```

The platform needs clear rules for whole-GPU placement, CPU-only placement,
node-local cache placement, and what happens when capacity is full.

If you want to work on the gateway, compare the manual NGINX proxy section with
the discovery and proxy packages. The gateway discovers active
`ModelDeployment` objects and forwards:

```txt
/models/qwen-chat/v1/chat/completions
```

to:

```txt
qwen-chat-runtime.inferops.svc.cluster.local:8000/v1/chat/completions
```

It avoids routing to inactive, failed, draining, or unready runtimes.

If you want to work on Helm, start from the difference between the manual
runtime manifest and `deploy/helm/inferops-runtime`. The chart should grow the
values needed for node-local cache mounts, `runtimeClassName`, node selectors,
stable labels, and secret references without making the template hard to read.

If you want to work on docs and examples, keep the same discipline as this
tutorial: show the Kubernetes object, show the command, show how to inspect it,
and explain what component should automate it later.

## References

- NVIDIA Container Toolkit installation:
  <https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html>
- k3s NVIDIA runtime support:
  <https://docs.k3s.io/advanced#nvidia-container-runtime>
- NVIDIA Kubernetes device plugin:
  <https://github.com/NVIDIA/k8s-device-plugin>
- Tailscale Kubernetes Operator:
  <https://tailscale.com/docs/kubernetes-operator/install-operator>
- Tailscale layer 3 workload exposure:
  <https://tailscale.com/docs/kubernetes-operator/ingress/expose-workload-to-tailnet-l3>
