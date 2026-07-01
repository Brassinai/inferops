# Homelab Setup

Single-node k3s with one NVIDIA GPU. Verified on Ubuntu 22.04/24.04.

## Node requirements

- NVIDIA GPU with compute capability 7.0+
- 32GB+ RAM
- 100GB+ NVMe for model cache
- Docker or containerd already running

## 1. k3s with NVIDIA runtime

Install k3s and tell it to use the NVIDIA container runtime:

```bash
curl -sfL https://get.k3s.io | sh -s - \
  --kubeconfig-mode 644 \
  --default-runtime nvidia
```

Verify:

```bash
kubectl get nodes -o wide
kubectl describe node <node> | grep -i nvidia
```

Docs: [k3s NVIDIA runtime](https://docs.k3s.io/advanced#nvidia-container-runtime-support)

## 2. NVIDIA Container Toolkit

If the runtime is not present:

```bash
# Add the NVIDIA package repositories and install nvidia-container-toolkit
# Follow the official install for your distro:
# https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html

sudo nvidia-ctk runtime configure --runtime=containerd
sudo systemctl restart k3s
```

Docs: [NVIDIA Container Toolkit install guide](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html)

## 3. NVIDIA Device Plugin

```bash
kubectl apply -f https://raw.githubusercontent.com/NVIDIA/k8s-device-plugin/v0.17.0/nvidia-device-plugin.yml
```

Verify allocatable GPUs:

```bash
kubectl describe node <node> | grep nvidia.com/gpu
```

Docs: [NVIDIA k8s-device-plugin](https://github.com/NVIDIA/k8s-device-plugin)

## 4. Storage

Model caches use `hostPath` under `/var/lib/inferops/models`. Ensure the path exists and has space:

```bash
sudo mkdir -p /var/lib/inferops/models
sudo chmod 755 /var/lib/inferops/models
df -h /var/lib/inferops/models
```

No special StorageClass is required for the homelab path.

## 5. (Optional) Tailscale private access

If you want to reach the gateway remotely without public ingress, install and
configure the Tailscale Kubernetes Operator by following its current official
documentation. InferOps deliberately does not install the operator or tailnet
credentials.

Docs: [Tailscale Kubernetes Operator](https://tailscale.com/docs/kubernetes-operator)
and [layer 7 Ingress](https://tailscale.com/docs/kubernetes-operator/ingress/expose-workload-to-tailnet-l7)

## 6. Install InferOps

The install command validates that the cache root is an absolute, clean path,
then passes it to the operator chart:

```bash
inferops install --profile homelab \
  --cache-path /var/lib/inferops/models
```

To create a private Tailscale ingress for the gateway after the Tailscale
operator is ready:

```bash
inferops install --profile homelab \
  --cache-path /var/lib/inferops/models \
  --tailscale-hostname inferops
```

Re-running either command performs an in-place Helm upgrade. It does not
install k3s, host drivers, the NVIDIA Container Toolkit, the NVIDIA device
plugin, or the Tailscale operator.

## 7. Hugging Face token (gated models only)

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
| `nvidia.com/gpu` not allocatable | Check device plugin pod logs; verify `nvidia-smi` on host |
| Image pull errors | Check image registry credentials; verify image tag exists |
| Cache download stuck | Check HF token; verify disk space and path permissions |
| Activation stays `WaitingForGPU` | Another model may be active; deactivate or use `--when-full ReplaceOldest` |
| Gateway 503 | Deployment not `Active`; check `inferops status` and operator logs |
