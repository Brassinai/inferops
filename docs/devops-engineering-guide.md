# Manual Deployment Walkthrough

Deploy a nano-vLLM model by hand to see the Kubernetes objects InferOps
automates. This is the same path the operator eventually reconciles for you.

## Goal

Run `qwen-chat` on a GPU node with:

- a node-local model cache;
- a runtime `Deployment` requesting one GPU;
- a stable runtime `Service`;
- a gateway route at `/models/qwen-chat/v1/...`.

## Prerequisites

- Linux GPU node with working NVIDIA driver.
- NVIDIA Container Toolkit and k3s/containerd configured for NVIDIA runtime.
- NVIDIA Kubernetes device plugin installed.
- `kubectl`, Helm, and this repo checked out.

## 1. Verify the host

```bash
nvidia-smi
```

If this fails, fix the driver first.

## 2. Verify the cluster sees the GPU

```bash
kubectl get nodes -o custom-columns=NAME:.metadata.name,GPU:.status.allocatable.nvidia\.com/gpu
kubectl run cuda-test --rm -it --restart=Never \
  --image=nvidia/cuda:12.4.1-base-ubuntu22.04 \
  --limits=nvidia.com/gpu=1 -- nvidia-smi
```

## 3. Prepare the model cache

```bash
sudo mkdir -p /var/lib/inferops/models/qwen-chat
sudo chown -R 1000:1000 /var/lib/inferops/models/qwen-chat
# place model files here
```

## 4. Install InferOps CRDs

```bash
kubectl apply -f deploy/manifests/crds
kubectl api-resources | grep inference.inferops.dev
```

## 5. Create the namespace and label the node

```bash
kubectl create namespace inferops
kubectl label node <gpu-node-name> inferops.dev/gpu-node=true
```

## 6. Declare the runtime

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

## 7. Declare the cache

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
    nodeName: <gpu-node-name>
    path: /var/lib/inferops/models/qwen-chat
YAML
```

## 8. Declare the deployment

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
  resources:
    cpu: "8"
    memory: 32Gi
    gpu:
      count: 1
      vendor: nvidia
  activation:
    desiredState: Active
    whenFull: Queue
  scaling:
    minReplicas: 1
    maxReplicas: 1
  routing:
    enabled: true
  cache:
    enabled: true
    type: nodeLocal
    size: 100Gi
    path: /var/lib/inferops/models
YAML
```

## 9. Create the runtime workload manually

Replace the image with your nano-vLLM runtime image. Remove `runtimeClassName`
if your cluster has no `nvidia` RuntimeClass.

```bash
kubectl apply -n inferops -f - <<'YAML'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: qwen-chat-runtime
  labels:
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
        inferops.dev/modeldeployment: qwen-chat
    spec:
      nodeSelector:
        inferops.dev/gpu-node: "true"
      runtimeClassName: nvidia
      terminationGracePeriodSeconds: 330
      containers:
        - name: runtime
          image: ghcr.io/your-org/inferops-runtime:nano-vllm
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

Watch:

```bash
kubectl get pods -n inferops -w
kubectl logs -n inferops -l inferops.dev/modeldeployment=qwen-chat -c runtime
```

## 10. Create the runtime Service

```bash
kubectl apply -n inferops -f - <<'YAML'
apiVersion: v1
kind: Service
metadata:
  name: qwen-chat-runtime
  labels:
    inferops.dev/modeldeployment: qwen-chat
spec:
  selector:
    app.kubernetes.io/name: qwen-chat-runtime
    inferops.dev/modeldeployment: qwen-chat
  ports:
    - name: http
      port: 8000
      targetPort: http
YAML
```

Test:

```bash
kubectl port-forward -n inferops svc/qwen-chat-runtime 8000:8000
curl http://127.0.0.1:8000/health
curl http://127.0.0.1:8000/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"qwen-chat","messages":[{"role":"user","content":"Hello"}]}'
```

## 11. Add a simple gateway

A minimal NGINX proxy shows the routing shape. The packaged gateway does this
with discovery and lifecycle checks.

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
        proxy_pass http://qwen-chat-runtime:8000;
      }
    }
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: inferops-gateway-manual
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: inferops-gateway-manual
  template:
    metadata:
      labels:
        app.kubernetes.io/name: inferops-gateway-manual
    spec:
      containers:
        - name: nginx
          image: nginx:1.27-alpine
          ports:
            - containerPort: 8080
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
spec:
  selector:
    app.kubernetes.io/name: inferops-gateway-manual
  ports:
    - port: 80
      targetPort: 8080
YAML
```

Test:

```bash
kubectl port-forward -n inferops svc/inferops-gateway-manual 8080:80
curl http://127.0.0.1:8080/models/qwen-chat/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"qwen-chat","messages":[{"role":"user","content":"Hello"}]}'
```

## 12. Use the packaged Helm charts

Install the operator:

```bash
helm upgrade -i inferops-operator ./deploy/helm/inferops-operator \
  --namespace inferops-system --create-namespace
```

Install the gateway:

```bash
helm upgrade -i inferops-gateway ./deploy/helm/inferops-gateway \
  --namespace inferops \
  --set image.repository=ghcr.io/brassinai/inferops-gateway \
  --set image.tag=v0.1.0
```

Install the runtime chart:

```bash
helm upgrade -i qwen-chat-runtime ./deploy/helm/inferops-runtime \
  --namespace inferops \
  --set image.repository=ghcr.io/your-org/inferops-runtime \
  --set image.tag=nano-vllm \
  --set model.repo=Qwen/Qwen2.5-7B-Instruct \
  --set model.path=/models/qwen-chat \
  --set cache.hostPath=/var/lib/inferops/models/qwen-chat \
  --set runtime.maxModelLen=4096 \
  --set 'resources.requests.nvidia\.com/gpu=1' \
  --set 'resources.limits.nvidia\.com/gpu=1'
```

## 13. Debug

```bash
kubectl get modelruntime,modelcache,modeldeployment -n inferops
kubectl describe modeldeployment qwen-chat -n inferops
kubectl get deploy,pods,svc,endpoints -n inferops
kubectl describe pod -n inferops -l inferops.dev/modeldeployment=qwen-chat
kubectl logs -n inferops -l inferops.dev/modeldeployment=qwen-chat -c runtime --tail=200
```

## What each object maps to

| Custom resource | Kubernetes object | Purpose |
| --- | --- | --- |
| `ModelRuntime` | runtime image + contract | Reusable engine definition |
| `ModelCache` | cache path on node | Model storage declaration |
| `ModelDeployment` | `Deployment` + `Service` | User endpoint |
| Gateway | `Deployment` + `Service` | Routes `/models/<name>/v1/...` |

## Where to contribute

- **Operator:** turn the manual `Deployment` and `Service` into reconciled output.
- **Cache controller:** verify, download, and report cache status.
- **Scheduler:** map `resources.gpu.count` to `nvidia.com/gpu` requests.
- **Gateway:** discover active deployments and skip inactive/draining ones.
- **Helm:** grow `inferops-runtime` values for node selectors, runtime classes,
  and cache volume types.
