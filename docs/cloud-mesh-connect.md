# Cloud Mesh Connect

Optionally connect a self-hosted InferOps cluster to a cloud control plane
through a private mesh. Local Kubernetes operation remains the source of
truth.

## Connect

```bash
inferops cloud connect --token <bootstrap-token> \
  --namespace inferops-system \
  --cluster-name homelab-a
```

Avoid shell history:

```bash
printf '%s' "$INFEROPS_CLOUD_TOKEN" | \
  inferops cloud connect --token-stdin --namespace inferops-system
```

`connect` checks that InferOps is installed, redeems the token, writes state
to Kubernetes, enables authenticated mesh gateway exposure, and installs the
persistent cloud agent.

Default mesh mode is InferCloud-managed Tailscale sidecar. To use your own
Tailscale operator:

```bash
inferops cloud connect --token <token> \
  --namespace inferops-system \
  --cluster-name homelab-a \
  --mesh-mode operator
```

## Stored state

| Resource | Contents |
| --- | --- |
| `Secret/inferops-cloud-connection` | Agent credential |
| `Secret/inferops-gateway-auth` | Gateway bearer token |
| `ConfigMap/inferops-cloud-connection` | Non-secret connection metadata |

Credentials are not stored in `.inferops/state.json`.

## Agent

The `inferops-cloud-agent` Helm chart runs in-cluster and:

- sends heartbeats to the cloud API;
- reports deployment counts, offerings, and node capacity;
- scrapes runtime metrics when available;
- exposes `/healthz`, `/readyz`, and `/metrics`.

## Status and disconnect

```bash
inferops cloud status --namespace inferops-system
inferops cloud disconnect --namespace inferops-system
```

`disconnect` removes the agent and cloud state but keeps operator, gateway,
deployments, and caches. Add `--disable-mesh-exposure` to also disable
Tailscale gateway exposure.
