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

### doctor

Run cluster health diagnostics.

```bash
inferops doctor
inferops doctor --check kubernetes-api --check gateway
```

Checks:

| Check | What it validates |
| --- | --- |
| `kubernetes-api` | API connectivity and namespace existence |
| `device-plugin` | GPU extended resources and device plugin DaemonSet readiness |
| `gpu-capacity` | Total, allocatable, occupied, and available GPU slots |
| `cache` | Installation ConfigMap and read-only node cache path probes |
| `runtime-class` | Required RuntimeClass definitions |
| `gateway` | Deployment readiness and `/readyz` response |
| `tailscale` | Ingress and hostname status when configured |

Doctor exits `0` when all checks pass or warn, and exits `1` when any check
fails. Each check includes independent remediation guidance.

The cache check creates short-lived, node-pinned Jobs in the selected namespace.
Each Job mounts the configured cache root read-only, reports filesystem totals
and free bytes, and is deleted before the check returns. Jobs also have an
active deadline and TTL so Kubernetes cleans them up if the CLI is interrupted.
The command never creates a missing host directory or changes host files. The
caller therefore needs permission to create and delete Jobs and to list, read,
and log their Pods in that namespace.

The operator chart exposes these diagnostic values:

| Value | Default | Purpose |
| --- | --- | --- |
| `gpu.required` | `false` | Makes missing GPU resources a doctor failure instead of a warning |
| `diagnostics.cacheProbeImage` | Digest-pinned BusyBox image | Image used by read-only cache probe Jobs; overrides must be pinned by SHA-256 |

### gpu list

Show GPU capacity, occupancy, and availability per node and resource.

```bash
inferops gpu list
```

InferOps counts all scheduled, non-terminal Pod requests across namespaces,
including non-InferOps workloads. If cluster-wide Pod listing is forbidden,
occupied capacity is reported as unknown rather than presenting false
availability.

### cache list

Show prepared caches with observed status and referencing deployments.

```bash
inferops cache list
```

### cache delete

Delete a `ModelCache` Kubernetes object. The command refuses when the cache is
referenced by a deployment, or when references cannot be determined safely,
unless `--force` is used. Forced deletion records the intent before deleting
the object.

```bash
inferops cache delete qwen-chat
inferops cache delete qwen-chat --force
```

This command does not delete node-local files. Host filesystem cleanup belongs
to the cache controller and is not performed by the CLI. Forced deletion can
leave a deployment without its registered cache, so it should be used only
after deactivation or deletion of the referencing deployment.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Success |
| `1` | General error |
| `2` | Invalid input / validation failed |
| `3` | Requested Kubernetes resource not found |
