# CLI Reference

All commands target a namespace. Default is `default`. Use `--namespace`, `--context`, or `--kubeconfig` as needed.

## Global flags

```
--namespace    Kubernetes namespace (default: default)
--context      Kubeconfig context
--kubeconfig   Path to kubeconfig file
--output       text | json | yaml (default: text)
```

## Commands

### install

Install the operator and gateway into the cluster.

```bash
inferops install --profile homelab
```

Profiles:

| Profile | Use case |
| --- | --- |
| `default` | Minimal operator + gateway |
| `homelab` | Includes cache-path defaults and sensible resource defaults |

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
