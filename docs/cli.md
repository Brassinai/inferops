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

Compute profiles:

| Compute profile | Use case |
| --- | --- |
| `cpu` | Default. Allows cache placement on any Ready cache-capable node. |
| `nvidia-gpu` | Requires cache nodes to advertise `nvidia.com/gpu` and makes missing GPU capacity a doctor failure. |

The command first applies the packaged CRDs with server-side apply, then uses
repeatable `helm upgrade --install` operations with atomic waits. This explicit
CRD step is required because Helm does not upgrade files from a chart's
`crds/` directory. It forwards `--context` and `--kubeconfig` to both
`kubectl` and Helm. Use
`--tailscale-hostname` only after installing and configuring the Tailscale
Kubernetes Operator:

```bash
inferops install --profile homelab \
  --tailscale-hostname inferops
```

For managed clusters, the install command also supports
`--exposure load-balancer`, `--exposure ingress`, and
`--exposure gateway-api`. These external methods require an existing bearer
token Secret selected with `--gateway-auth-secret`, or an explicit
`--allow-unauthenticated-exposure` acknowledgement when authentication is
enforced upstream. Provider-specific controllers remain cluster prerequisites.
See [Cluster and ingress support](cluster-ingress.md) for the complete flags,
values, security constraints, and acceptance checks.

InferOps does not install or guess host NVIDIA drivers, the NVIDIA Container
Toolkit, a device plugin, k3s, or Tailscale. Those host and cluster
prerequisites must be installed and verified independently.

For NVIDIA GPU homelabs, make GPU placement explicit:

```bash
inferops install --profile homelab \
  --compute-profile nvidia-gpu \
  --cache-path /var/lib/inferops/models
```

When running a CLI development build outside this repository, use
`--charts-dir /path/to/inferops/deploy/helm` if the packaged charts cannot be
detected.

All component images are configurable in chart values. For a private registry
or a development build, override `image.repository` and `image.tag` in each
chart; the operator chart also exposes the `modelRuntimes` map and
`cache.downloaderImage`.

Set `rbac.create=false` when cluster-scoped RBAC is provisioned separately.
The supplied ClusterRole grants only `get` on Secrets. The cache controller
checks that a same-namespace referenced Secret contains the required key, but
never logs or copies its value; the downloader Job receives the value through
`secretKeyRef`.

The charts enable operator, gateway, and runtime NetworkPolicies by default.
The gateway chart also provides opt-in `tenancy.access`, `tenancy.quota`, and
`tenancy.limitRange` resources for a team namespace. See
[Namespace tenancy](tenancy.md) to configure the exact Kubernetes API Service
IP before installation or binding users.

The self-hosted dashboard is packaged as a separate read-only chart,
`deploy/helm/inferops-dashboard`, so operators can choose their own access
boundary and authentication layer. See [Self-hosted dashboard](dashboard.md).

### upgrade

Upgrade installed control-plane images after publishing a new operator or
dashboard tag:

```bash
inferops upgrade \
  --context production \
  --namespace inferops-system \
  --tag v0.2.0 \
  --enable-observability
```

The command applies packaged CRDs, then runs Helm upgrades with
`--reuse-values` for:

- `inferops-operator`
- `inferops-dashboard`

It defaults to `ghcr.io/brassinai/inferops-operator:<tag>` and
`ghcr.io/brassinai/inferops-dashboard:<tag>`. Override repositories only when
using a mirror or private registry:

```bash
inferops upgrade \
  --tag v0.2.0 \
  --operator-image registry.example.com/inferops-operator \
  --dashboard-image registry.example.com/inferops-dashboard
```

Use `--skip-dashboard` when the dashboard chart is not installed.

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

### serve

Serve Python methods declared with `@inferops.web_endpoint` from an app file:

```bash
inferops serve app.py --gateway-url http://127.0.0.1:8080 --port 9000
```

The command imports the app, exposes its decorated endpoint methods over HTTP,
and binds `self.generate()` / `self.generate_stream()` to a live InferOps
gateway. Keep `inferops gateway forward` running, or pass a reachable gateway
URL with `--gateway-url`.

Example custom endpoint call:

```bash
curl -X POST http://127.0.0.1:9000/chat \
  -H "Content-Type: application/json" \
  -d '{"prompt":"Explain Kubernetes Services simply."}'
```

### deploy-endpoints

Deploy custom SDK web endpoints as a normal Kubernetes Deployment and Service.
Use this after the model runtime has already been deployed with `inferops
deploy` and activated.

```bash
inferops deploy-endpoints app.py \
  --context orbstack \
  --image ghcr.io/brassinai/my-endpoint-app:v0.1.0 \
  --build
```

With `--build`, the command builds the endpoint image with Docker, pushes it,
then applies the Kubernetes resources. Without `--build`, the image must
already exist in a registry the cluster can pull from.

The created Deployment runs:

```bash
inferops serve /app/app.py --host 0.0.0.0 --port 8080
```

By default the command sets:

```text
INFEROPS_GATEWAY_URL=http://inferops-gateway.<namespace>.svc
```

That lets custom handlers call `self.generate()` and `self.generate_stream()`
against the in-cluster InferOps gateway. The command refuses to deploy an app
that does not declare any `@inferops.web_endpoint` handlers.

For local clusters that can read from the local Docker image store, use
`--build --no-push`.

### gateway forward

Forward the installed gateway Service to localhost for local testing:

```bash
inferops gateway forward --context orbstack
```

Defaults are equivalent to:

```bash
kubectl --context orbstack --namespace default port-forward \
  --address 127.0.0.1 svc/inferops-gateway 8080:80
```

Override ports or namespace when needed:

```bash
inferops gateway forward \
  --context orbstack \
  --namespace inferops-system \
  --local-port 8081 \
  --remote-port 80
```

### generate

Print YAML without applying.

```bash
inferops generate app.py > manifests.yaml
```

### activate

Start runtime and route traffic.

```bash
inferops activate qwen-chat
inferops activate qwen-chat --when-full Reject
inferops activate qwen-chat --when-full ReplaceOldest
```

Activation waits up to five minutes for the operator to observe the new
generation. Use `--timeout 10m` to change that limit or `--no-wait` to return
after the Kubernetes patch is accepted. The command reports `active`,
`waiting`, `rejected`, or `failed`; waiting is an accepted queued request,
while rejection and failure exit nonzero. It reports `superseded` if another
writer changes the desired state during the wait and `timeout` if no stable
outcome is observed before the deadline; both exit nonzero.

The existing `whenFull` policy is preserved unless `--when-full` is supplied.
Replacement is never inferred: select `ReplaceOldest` or
`ReplaceLowestPriority` explicitly to authorize replacement. If no GPU slot is
free with `Queue`, the deployment reports `WaitingForGPU` and remains queued.
Single-GPU replacement prepares the new cache before draining the selected
runtime, but it still has unavoidable downtime because both runtimes cannot
occupy the only GPU. Status and Events report replacement and rollback
outcomes.

### deactivate

Drain traffic, stop runtime, release GPU. Cache is kept.

```bash
inferops deactivate qwen-chat
```

Deactivation watches `Draining` and `Deactivating` transitions by default.
Once the runtime has released capacity, the inactive deployment normally
settles at `Cached`. `--timeout` and `--no-wait` behave as they do for
activation.

### status

Show current phase, conditions, and assigned node.

```bash
inferops status qwen-chat
inferops status qwen-chat --watch --timeout 10m
```

Status output is a safe summary of the ModelDeployment. It includes observed
conditions, placement, replica state, cache state, and the endpoint, but never
returns the `spec.secrets` field or Kubernetes Secret objects. During a drain
or replacement, JSON/YAML output also includes the sanitized
`drainStartedAt` and `replacement` status fields.

### models

List ModelDeployments in the selected namespace:

```bash
inferops models
inferops models --output json
```

### endpoints

List stable routes for routing-enabled ModelDeployments, including endpoints
that are currently unavailable because their model is inactive or waiting:

```bash
inferops endpoints
```

### logs

Show runtime pod logs.

```bash
inferops logs qwen-chat
inferops logs qwen-chat --tail 100
```

### delete

Remove the deployment and its managed runtime resources.

```bash
inferops delete qwen-chat
```

The command reports `cachePreserved: true`: deleting a deployment does not
delete its `ModelCache` or node files. Use the separate `cache delete` command
when cache removal is intentional.

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
