# CLI Reference

Cluster commands use the `default` namespace unless you pass `--namespace`.
Use `--context` and `--kubeconfig` to select the cluster explicitly. Pass these
options after the relevant subcommand; `inferops generate` operates locally
and does not accept cluster options.

## Global flags

```
--namespace    Kubernetes namespace (default: default)
--context      Kubeconfig context
--kubeconfig   Path to kubeconfig file
--output       text | json | yaml (default: text)
```

## Install and upgrade

```bash
# Install
inferops install --profile homelab \
  --compute-profile nvidia-gpu \
  --cache-path /var/lib/inferops/models \
  --cache-capacity 500Gi

# Install a specific image tag for operator, gateway, downloader, and runtimes
inferops install --profile homelab \
  --compute-profile nvidia-gpu \
  --cache-path /var/lib/inferops/models \
  --cache-capacity 500Gi \
  --tag cloud

# Upgrade all components
inferops upgrade --namespace inferops-system --tag v0.2.0

# Upgrade one component
inferops upgrade --component operator --tag v0.2.0

# Uninstall (keeps CRDs and caches)
inferops uninstall --namespace inferops-system

# Remove platform releases and cluster-wide CRDs/custom resources; retain model files
inferops uninstall --namespace inferops-system --crds
```

The [installation agent skill](../skills/inferops-install/SKILL.md) discovers a
published stable version, verifies required images for the target architecture,
and passes its concrete `--tag` during the initial installation. The CLI itself
continues to use packaged chart defaults when `--tag` is omitted. A fresh skill
installation does not need a second upgrade to reach the selected version.

## Lifecycle

```bash
inferops deploy app.py
inferops deploy app.py --activate
inferops deploy app.py --activate --when-full ReplaceOldest

inferops activate qwen-chat
inferops activate qwen-chat --when-full Reject
inferops activate qwen-chat --timeout 10m --verbose

inferops deactivate qwen-chat

inferops status qwen-chat
inferops status qwen-chat --watch --timeout 10m

inferops delete qwen-chat
```

## Inspection

```bash
inferops models
inferops endpoints
inferops logs qwen-chat
inferops logs qwen-chat --tail 100
inferops diagnose qwen-chat
inferops doctor
inferops gpu list
inferops cache list
inferops cache delete <cache-name>
```

`cache delete` removes the ModelCache record and retains node-local files. It
refuses referenced caches by default. Physical file purging is a separate,
explicitly destructive uninstall option; inspect `inferops uninstall --help`.
Deleting CRDs removes InferOps custom resources across every namespace.

## Gateway

```bash
inferops gateway forward
inferops gateway forward --namespace inferops-system --local-port 8081
```

## SDK endpoints

```bash
# Serve endpoints locally
inferops serve app.py --gateway-url http://127.0.0.1:8080 --port 9000

# Deploy endpoints into the cluster
inferops deploy-endpoints app.py \
  --image ghcr.io/brassinai/my-endpoint-app:v0.1.0 \
  --build
```

## Manifests

```bash
inferops generate app.py > manifests.yaml
```

## Observability

```bash
inferops observability setup \
  --namespace inferops-system \
  --monitoring-namespace monitoring

inferops observability open --namespace monitoring
```

Use the chart-generated Grafana admin Secret for login rather than a shared
example password. See the [observability guide](observability.md) for access.

## Cloud mesh

```bash
inferops cloud connect --token <token> --namespace inferops-system --cluster-name homelab-a
inferops cloud status --namespace inferops-system
inferops cloud disconnect --namespace inferops-system
```

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Success |
| `1` | General error |
| `2` | Invalid input |
| `3` | Kubernetes resource not found |
| `10` | Activation rejected |
| `11` | Activation failed |
| `12` | Activation timeout |
| `13` | Activation superseded |

## Install profiles

| Profile | Use |
| --- | --- |
| `default` | Minimal operator + gateway |
| `homelab` | Sensible defaults for single-node setups |

| Compute profile | Use |
| --- | --- |
| `cpu` | Any node (default) |
| `nvidia-gpu` | GPU-capable nodes required |

## Cache capacity flags

| Flag | Use |
| --- | --- |
| `--cache-capacity 100Gi` | One eligible node |
| `--cache-node node-a` | Specific node |
| `--cache-node-selector inferops.dev/cache=true` | All matching nodes |
| `--cache-node-capacity node-a=100Gi --cache-node-capacity node-b=500Gi` | Per-node capacities |
