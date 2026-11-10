# Connect a provider cluster to InferCloud

## Preflight

Inspect the selected InferOps installation, current models/endpoints, and any
existing connection. Resolve the user-provided cloud API origin and cluster name.
A dashboard page URL is not necessarily the API origin; validate it before using
credentials. Require HTTPS for a hosted endpoint and never disable TLS validation;
plain HTTP is only for an explicitly requested trusted development environment.
Do not send a key to a guessed hostname or third-party redirect.

The credential is a provider API key / agent credential scoped for cluster
connection. A consumer inference API key cannot register a provider. The current
CLI calls the input a bootstrap token; its --token-stdin flag accepts the provider
connection credential. Do not infer credential permissions from its appearance
or print it to determine the type.

~~~sh
inferops models --context "$CTX" --namespace "$NS" --output json
inferops endpoints --context "$CTX" --namespace "$NS" --output json
inferops cloud status --context "$CTX" --namespace "$NS" --output json
~~~

An absent connection is expected for first-time setup. If the existing connection
already matches the requested destination and is healthy, report it and inspect
benchmark settings rather than redeeming credentials repeatedly. A different
connection or provider account needs an explicit migration scope; do not silently
overwrite it. Newly activated offerings can be discovered after connection.

## Connect and enable offering benchmarks

Use INFEROPS_CLOUD_TOKEN from the user's secure environment or --token-stdin
through a secure input channel. The environment path avoids key material in
arguments, shell history, transcript, and command output:

~~~sh
inferops cloud connect --context "$CTX" --namespace "$NS" --cloud-api-url "$CLOUD_API_URL" \
  --cluster-name "$CLUSTER_NAME" --benchmark-offerings --timeout 5m --output json
inferops cloud status --context "$CTX" --namespace "$NS" --output json
kubectl --context "$CTX" -n "$NS" rollout status deployment/inferops-cloud-agent --timeout=120s
~~~

The connect command reads INFEROPS_CLOUD_TOKEN when neither token flag is passed.
If --token-stdin is used, do not also pass --token or pipe a literal secret into
a tool call. Do not log environment variables. Remove a temporary task-created
token variable after use without changing a preexisting user's environment.

Default mesh exposure is the InferCloud-managed Tailscale sidecar. Choose
--mesh-mode operator only when a provider-owned Tailscale operator is installed
and that mode was requested. Connect installs the cloud agent and authenticated
mesh gateway exposure, and stores the agent credential in
Secret/inferops-cloud-connection, gateway auth in Secret/inferops-gateway-auth,
and non-secret metadata in ConfigMap/inferops-cloud-connection. Inspect Secret
existence/key NAMES only; never dump values or full gateway Helm values.

## Repair and verify

| Failure | Evidence and action |
| --- | --- |
| 401/403 | Verify destination and provider credential scope, account state, and required provider terms; request a suitable credential through secure input when missing. Do not retry a rejected key indefinitely. |
| DNS/TLS/network | Check name resolution, certificates, and authorized egress. Fix trust/configuration; never use insecure certificate bypass. |
| Missing platform/cache permissions | Verify actual namespace, installed components, and precise required API verbs. Do not give the agent write RBAC. |
| Agent rollout fails | Inspect image/tag/platform, pull permissions, Pod scheduling and ConfigMap/Secret references without revealing credentials. Repair only the proven issue. |
| Mesh exposure fails | Inspect selected mode, gateway sidecar/operator state, mesh identity, and endpoint authentication. Preserve gateway auth; do not publish an unauthenticated public endpoint. |
| Benchmark not yet reported | Check that an eligible default OpenAI-compatible offering is Active and reachable by the agent, and that benchmarking is enabled. Allow a bounded observation interval and inspect status; a connect exit 0 is not benchmark evidence. |

Rerun cloud status after a scoped repair. Connection success, provider health,
offering discovery, benchmark results, and cloud routability are separate states.
Only the default InferOps OpenAI-compatible endpoint can be published; custom
SDK endpoints are ineligible. Provider agent Kubernetes permissions remain
read-only. Cloud gateway metering determines usage/earnings; provider usage
receipts are optional diagnostics and are not enabled just to connect.

Never delete a retained receipt PVC or reset mesh/identity state as a generic
fix. Report provider/cluster identifiers, destination, observed agent/mesh state,
benchmark results or pending status, eligible offerings, and unresolved blockers.
If disconnecting is requested, use cloud disconnect; add
--disable-mesh-exposure only when exposure removal is intended. Default disconnect
keeps local workloads and can retain exposure, so verify the requested end state.
