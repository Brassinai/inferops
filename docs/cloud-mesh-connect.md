# Cloud Mesh Connect

InferOps can optionally connect a self-hosted cluster to a cloud control plane
through a private mesh endpoint. Local Kubernetes operation remains the source
of truth: model deployments, caches, runtimes, and gateway routing continue to
work without a cloud connection.

## Connect

Use a short-lived bootstrap token from the cloud dashboard:

```bash
inferops cloud connect --token <bootstrap-token> \
  --namespace inferops-system \
  --cluster-name homelab-a
```

For shell history safety:

```bash
printf '%s' "$INFEROPS_CLOUD_TOKEN" | \
  inferops cloud connect --token-stdin --namespace inferops-system
```

`connect` verifies that InferOps is already installed, redeems the bootstrap
token, writes persistent connection state to Kubernetes, enables authenticated
Tailscale gateway exposure, and installs the persistent cloud agent.

The first supported mesh mode is the Tailscale Kubernetes Operator Ingress
path. The command does not install the Tailscale operator or manage a host
`tailscaled` daemon.

## Token Contract

The bootstrap token is used only to call:

```http
POST /v1/provider-connections/redeem
```

The cloud API returns the durable connection payload:

```json
{
  "cloudApiUrl": "https://cloud.example.com",
  "providerAccountId": "provider_123",
  "providerClusterId": "cluster_123",
  "agentCredential": "...",
  "meshProvider": "tailscale",
  "meshHostname": "inferops-homelab-a",
  "meshTags": ["tag:provider-agent", "tag:provider-endpoint"],
  "expectedEndpointPath": "/",
  "agentConfig": {
    "heartbeatIntervalSeconds": 30
  }
}
```

If the target cloud API is not the default, pass:

```bash
inferops cloud connect --cloud-api-url https://cloud.example.com --token ...
```

Remote cloud API URLs must use HTTPS. Localhost HTTP URLs are accepted only for
development and tests.

## Stored State

Secrets are stored in Kubernetes Secrets in the selected namespace:

| Resource | Contents |
| --- | --- |
| `Secret/inferops-cloud-connection` | provider agent credential |
| `Secret/inferops-gateway-auth` | gateway bearer token used by the mesh-facing gateway |

Non-secret connection state is stored in:

```text
ConfigMap/inferops-cloud-connection
```

The ConfigMap records cloud API URL, provider account ID, provider cluster ID,
mesh provider, mesh hostname, mesh endpoint URL, expected endpoint path,
heartbeat interval, mesh tags, and the last local connect timestamp.

The CLI does not store these cloud credentials in project-local
`.inferops/state.json`.

## Persistent Agent

`inferops cloud connect` installs the `inferops-cloud-agent` Helm chart. The
agent runs in-cluster and:

- reads its credential from the mounted Secret,
- reads the gateway bearer token from the gateway auth Secret,
- sends authenticated heartbeats to the cloud API with mesh identity, gateway
  credential, deployment counts, offerings, and node capacity evidence,
- exposes `/healthz`, `/readyz`, and `/metrics`,
- uses minimal namespace list RBAC plus node list access for capacity reporting.

## Status

```bash
inferops cloud status --namespace inferops-system
```

Status reports independent layers:

- cloud connection ConfigMap,
- agent credential Secret,
- gateway auth Secret,
- gateway readiness,
- mesh exposure readiness,
- provider agent Deployment readiness,
- cloud API credential check and heartbeat freshness.

Use `-o json` or `-o yaml` for automation.

## Disconnect

```bash
inferops cloud disconnect --namespace inferops-system
```

Disconnect removes the cloud agent release and cloud connection state while
preserving the operator, gateway, CRDs, model deployments, and model caches.

To also disable the InferOps-managed Tailscale gateway exposure:

```bash
inferops cloud disconnect --disable-mesh-exposure --namespace inferops-system
```

This does not uninstall the Tailscale Kubernetes Operator or alter unrelated
mesh resources.
