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
sudo install -d -o 65532 -g 65532 -m 0750 /var/lib/inferops/models
df -h /var/lib/inferops/models
```

The downloader runs as UID/GID `65532` and deliberately uses a
`hostPath.type` of `Directory`; InferOps will not create a root-owned cache
directory that its non-root downloader cannot write.

Advertise the portion of that filesystem InferOps may reserve on each cache
node:

```bash
kubectl annotate node <node-name> inferops.dev/cache-capacity=500Gi
```

This is declared capacity, not a live free-space reading. Set it below the
filesystem's usable size to leave operating headroom. Live disk-pressure and
eviction reporting belongs to MVP-503.

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

## 7. Verify the platform

Run diagnostics after installation:

```bash
inferops doctor
```

The cache diagnostic uses temporary read-only probe Jobs on GPU, cache, or
otherwise schedulable nodes. It reports free bytes but does not create the
cache directory or repair host prerequisites. Jobs have an active deadline and
TTL for cleanup after interruption. If Job or Pod inspection is forbidden,
doctor reports a failed check with the required RBAC remediation.

Check GPU inventory:

```bash
inferops gpu list
```

## 8. Run the acceptance workflow

After the host prerequisites and install checks pass, run the recorded
single-node GPU acceptance workflow from the repository root. The script runs
install, doctor, GPU inventory, deploy, cache inspection, activation, streaming
OpenAI-compatible inference, deactivation, GPU release inspection,
re-activation, and cache inspection. It writes timings, command output, and
defects to a Markdown report.

```bash
scripts/homelab_acceptance.py \
  --manifest examples/yaml-deploy/modeldeployment.yaml \
  --model-name qwen-chat \
  --gateway-url http://<gateway-host> \
  --namespace inferops-system \
  --output homelab-acceptance.md
```

If the platform is already installed, add `--skip-install`. To validate
explicit single-GPU replacement, add a second manifest and name:

```bash
scripts/homelab_acceptance.py \
  --manifest examples/yaml-deploy/modeldeployment.yaml \
  --model-name qwen-chat \
  --replacement-manifest <replacement-modeldeployment.yaml> \
  --replacement-name <replacement-name> \
  --gateway-url http://<gateway-host> \
  --namespace inferops-system \
  --skip-install \
  --output homelab-acceptance.md
```

Keep the generated report with the release notes or issue evidence. A passing
run proves that streaming traffic reaches the gateway, deactivation releases
the GPU while preserving cache state, re-activation does not require another
model download, and explicit replacement either succeeds or reports visible
rollback status.

## 9. Hugging Face token (gated models only)

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
| Doctor reports missing namespace | Create the namespace or select the correct one with `--namespace` |
| Doctor cannot list pods | Grant broader Pod list permissions or run with elevated RBAC |
