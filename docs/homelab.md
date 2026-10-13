# Homelab Setup

Single-node k3s with one NVIDIA GPU. Verified on Ubuntu 22.04/24.04.

## Node requirements

- NVIDIA GPU, compute capability 7.0+
- 32GB+ RAM
- 100GB+ NVMe for model cache
- Docker or containerd running

## 1. Install k3s with NVIDIA runtime

```bash
curl -sfL https://get.k3s.io | sh -s - \
  --kubeconfig-mode 644 \
  --default-runtime nvidia
```

Check:

```bash
kubectl get nodes -o wide
kubectl describe node <node> | grep -i nvidia
```

## 2. Install NVIDIA Container Toolkit (if missing)

```bash
# Follow: https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html
sudo nvidia-ctk runtime configure --runtime=containerd
sudo systemctl restart k3s
```

## 3. Install NVIDIA Device Plugin

```bash
kubectl apply -f https://raw.githubusercontent.com/NVIDIA/k8s-device-plugin/v0.17.0/nvidia-device-plugin.yml
```

Check allocatable GPUs:

```bash
kubectl describe node <node> | grep nvidia.com/gpu
```

## 4. Create cache storage

```bash
sudo install -d -o 65532 -g 65532 -m 0750 /var/lib/inferops/models
df -h /var/lib/inferops/models
```

## 5. Install InferOps

```bash
# GPU
inferops install --profile homelab \
  --compute-profile nvidia-gpu \
  --cache-path /var/lib/inferops/models \
  --cache-capacity 500Gi

# CPU-only
inferops install --profile homelab \
  --cache-path /var/lib/inferops/models \
  --cache-capacity 500Gi
```

Optional Tailscale hostname after installing the Tailscale operator:

```bash
inferops install --profile homelab \
  --cache-path /var/lib/inferops/models \
  --cache-capacity 500Gi \
  --tailscale-hostname inferops
```

Re-running performs an in-place upgrade.

## 6. Verify

```bash
inferops doctor
inferops gpu list
```

## 7. Run acceptance workflow

```bash
scripts/homelab_acceptance.py \
  --manifest examples/yaml-deploy/modeldeployment.yaml \
  --model-name qwen-chat \
  --gateway-url http://<gateway-host> \
  --namespace inferops-system \
  --output homelab-acceptance.md
```

Use `--skip-install` if already installed. See the script for shared-cache and
replacement variants.

## 8. Hugging Face token (gated models only)

```bash
kubectl create secret generic hf-token \
  --from-literal=token=$HF_TOKEN \
  -n inferops-system
```

Reference it in `ModelDeployment`:

```yaml
spec:
  secrets:
    huggingFaceTokenSecretName: hf-token
```

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| `nvidia.com/gpu` not allocatable | Check device plugin pod logs; run `nvidia-smi` on host |
| Image pull errors | Check registry credentials and image tag |
| Cache download stuck | Check HF token, disk space, and path permissions |
| Stuck `WaitingForGPU` | Deactivate another model or use `--when-full ReplaceOldest` |
| Gateway 503 | Check `inferops status` and operator logs |
| Doctor namespace error | Create the namespace or pass `--namespace` |
| Doctor cannot list pods | Grant Pod list RBAC or run with elevated permissions |
