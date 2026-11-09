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

## Optional provider usage diagnostics

Updated cloud-connected gateways reserve and persist per-request usage receipts
on a PVC. The agent polls the gateway every five seconds, signs the observed
runtime counts and sends them to InferCloud. Delivery retries survive gateway
and agent restarts; receipts are removed only after a verified cloud acknowledgement.
No request bodies, generated text or credentials are stored in receipts.

`inferops cloud connect` leaves `cloudUsage.enabled=false` on the gateway.
Provider earnings and payouts use InferCloud gateway metering; no provider receipt
is required. Agent `cloud.usageReporting.enabled` also defaults to false. To opt
into diagnostic receipts, enable both chart settings explicitly. A default
single gateway uses a retained 1Gi ReadWriteOnce PVC and a Recreate rollout;
expect a brief gateway interruption during updates. Multiple gateways require
`cloudUsage.persistence.accessModes=[ReadWriteMany]` with shared storage supporting
file locks and fsync. An existing PVC can be supplied with
`cloudUsage.persistence.existingClaim`; `storageClass` and `size` configure a new
claim. Missing/default StorageClass configuration must be resolved before the
cloud connection becomes ready. Never delete a queue PVC with pending receipts.

The agent uses `cloud.usageReporting.enabled` and `gatewayUrl` (defaulting to the
namespace's inferops-gateway Service). Existing mesh and agent credentials are
mounted read-only and reloaded for delivery. Kubernetes RBAC remains read-only.
Deploy updated InferCloud API/gateway/metering services and migration 000030
before enabling the updated gateway and agent.

Diagnostic token counts come from JSON/SSE usage objects. Missing usage,
mismatches, rejected signatures and disabled reporting do not hold cloud-metered
earnings. Old requests cannot be reconstructed as independent provider reports.
Reporting does not initiate payouts. Existing opt-in installations can disable
both settings without deleting their retained receipt PVC.

Monitor the agent's `inferops_cloud_usage_reports_delivered_total`,
`inferops_cloud_usage_report_failures_total`, `inferops_cloud_usage_queue_ready`,
`inferops_cloud_usage_queue_pending` and `inferops_cloud_usage_queue_quarantined`.
A full queue rejects new cloud requests before runtime execution; accepted records
without a terminal receipt and permanent rejections require operator review.
