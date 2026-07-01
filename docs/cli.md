# CLI Reference

All commands target the `default` namespace unless overridden. Use
`--namespace`, `--context`, or `--kubeconfig` as needed. Install the
cluster-wide operator once, and use the install namespace for model deployments
that reference its default namespaced `ModelRuntime`.

## Global flags

```
--namespace    Kubernetes namespace (default: default)
--context      Kubeconfig context
--kubeconfig   Path to kubeconfig file
--output       text | json | yaml (default: text)
```

## Commands

### install

Install or upgrade the CRDs, operator, gateway, and packaged `nano-vllm`,
`vllm`, `sglang`, and `llama-cpp` `ModelRuntime` definitions with Helm:

```bash
inferops install --profile homelab \
  --cache-path /var/lib/inferops/models
```

Profiles:

| Profile | Use case |
| --- | --- |
| `default` | Minimal operator + gateway |
| `homelab` | Includes cache-path defaults and sensible resource defaults |

The command uses repeatable `helm upgrade --install` operations with atomic
waits. It forwards `--context` and `--kubeconfig` to Helm. Use
`--tailscale-hostname` only after installing and configuring the Tailscale
Kubernetes Operator:

```bash
inferops install --profile homelab \
  --tailscale-hostname inferops
```

InferOps does not install or guess host NVIDIA drivers, the NVIDIA Container
Toolkit, a device plugin, k3s, or Tailscale. Those host and cluster
prerequisites must be installed and verified independently.

When running a CLI development build outside this repository, use
`--charts-dir /path/to/inferops/deploy/helm` if the packaged charts cannot be
detected.

All component images are configurable in chart values. For a private registry
or a development build, override `image.repository` and `image.tag` in each
chart; the operator chart also exposes the `modelRuntimes` map and
`cache.downloaderImage`.

Set `rbac.create=false` when cluster-scoped RBAC is provisioned separately.
The supplied ClusterRole deliberately excludes Secret reads: model download
credentials are referenced by name from downloader Jobs and resolved by
Kubernetes rather than read into operator memory.

The install profile does not choose the engine for every model. Each
deployment selects a registered runtime and independently declares whether it
needs GPUs:

| Runtime reference | Packaged compute path |
| --- | --- |
| `nano-vllm` | NVIDIA GPU; SDK default |
| `vllm` | NVIDIA GPU |
| `sglang` | NVIDIA GPU |
| `llama-cpp` | Portable CPU |

For example, an SDK declaration selects the out-of-the-box CPU path with
`engine="llama-cpp"` and `gpu=None`:

```python
@app.model(
    name="cpu-smollm",
    engine="llama-cpp",
    model="jc-builds/SmolLM2-135M-Instruct-Q4_K_M-GGUF",
    gpu=None,
    cpu="4",
    memory="2Gi",
    max_model_len=512,
)
class CPUSmolLM:
    pass
```

Selecting `gpu=None` only removes the Kubernetes GPU request; it does not make
a CUDA image CPU-compatible. Use `llama-cpp` for the portable CPU default, or
provide an explicitly built CPU image through `runtime_image`.

### deploy

Load an SDK `app.py`, generate manifests, and apply them.

```bash
inferops deploy app.py
inferops deploy app.py --activate
inferops deploy app.py --activate --when-full ReplaceOldest
```

Idempotent: re-deploying the same app with no changes is a no-op.

### generate

Print YAML without applying.

```bash
inferops generate app.py > manifests.yaml
```

### activate

Start runtime and route traffic.

```bash
inferops activate qwen-chat
```

If no GPU slot is free and `whenFull=Queue`, the deployment waits at `WaitingForGPU`.

### deactivate

Drain traffic, stop runtime, release GPU. Cache is kept.

```bash
inferops deactivate qwen-chat
```

### status

Show current phase, conditions, and assigned node.

```bash
inferops status qwen-chat
```

### logs

Show runtime pod logs.

```bash
inferops logs qwen-chat
inferops logs qwen-chat --tail 100
```

### delete

Remove the deployment and its managed Service.

```bash
inferops delete qwen-chat
```

Cache is not deleted. Use `cache delete` to remove cache.

### gpu list

Show allocatable and used GPU slots.

```bash
inferops gpu list
```

### cache list

Show prepared caches.

```bash
inferops cache list
```

### cache delete

Remove a model cache.

```bash
inferops cache delete qwen-chat
inferops cache delete qwen-chat --force
```

`--force` is required when the cache is still referenced by a deployment.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Success |
| `1` | General error |
| `2` | Invalid input / validation failed |
| `3` | Kubernetes API error |
