# Cloud connection troubleshooting

Read this section for a failed setup or an explicit diagnosis/repair request.
A status-only request remains read-only. Extra end-to-end reachability/model
checks remain conditional on the user's explicit request; do not run them just
because this section describes possible causes. For setup failures, inspect only
bounded relevant rollout/events/log evidence needed to explain the failure.

Headscale is a coordinator; the sidecar is a Tailscale client. In a local lab,
the InferCloud mesh node and provider gateway sidecar must join the SAME
Headscale network. Inspect the actual login-server setting, not only meshMode:
managed-sidecar may have a custom local login server. A hosted managed sidecar
normally uses cloud-managed Tailscale coordination. Operator mode requires an
existing provider-owned operator; do not force it as a sidecar workaround.
Cloudflare HTTPS reaches the Cloud API and does not replace private mesh traffic.

Check both sides of the path: control-server DNS/reachability, joined state,
peer identity/tags, peer DNS/IP and target port as needed for the requested
diagnosis. Authenticated gateway/model tests require an explicit test request.
A healthy Kubernetes sidecar alone does not establish cloud-to-provider access.
Host localhost, host.docker.internal and Docker service names may resolve
differently inside Pods and containers. In local labs, Tailscale DNS can replace
Docker DNS and make a short Docker control-server name unresolvable after a
restart; inspect the exact source process and resolver before changing settings.

An expired mesh join key differs from a provider API credential or gateway token.
Restarting Headscale cannot renew it. Within an authorized local-lab repair,
issue a fresh appropriately tagged key from the existing coordinator, update
only that mesh key in the target Secret and private API configuration, and
restart the affected mesh process. Reload the authorized local API if it would
otherwise return the stale key on the next connect. Use secure stdin/files,
never key values in arguments/output. Preserve existing node state, provider
registration and gateway token; do not rotate unrelated credentials. Hosted
coordinators require their authorized key-issuance path, not Headscale commands.

### Repair within the requested scope

| Failure | Evidence and action |
| --- | --- |
| 401/403 | Verify destination and provider credential scope, account state, and required provider terms; request a suitable credential through secure input when missing. Do not retry a rejected key indefinitely. |
| DNS/TLS/network | Check name resolution, certificates, and authorized egress. Fix trust/configuration; never use insecure certificate bypass. |
| Missing platform/cache permissions | Verify actual namespace, installed components, and precise required API verbs. Do not give the agent write RBAC. |
| Agent rollout fails | Inspect image/tag/platform, pull permissions, Pod scheduling and ConfigMap/Secret references without revealing credentials. Repair only the proven issue. |
| Mesh exposure fails | Inspect selected mode, gateway sidecar/operator state, mesh identity, and endpoint authentication. Preserve gateway auth; do not publish an unauthenticated public endpoint. |
| Heartbeat accepted but not routable | Report the status finding and preserve the agent/registration. Investigate cloud-side probes, model requests or publication evidence only when explicitly asked to diagnose routing. |
| Expired mesh key / sidecar crash loop | Renew only the affected join key through the authorized coordinator path, then rerun cloud status. Restart alone does not renew the key. |
| Benchmark not yet reported | Not a blocker for plain connect. Only investigate or enable benchmarking when explicitly requested, preserving existing registration and settings. |

Rerun cloud status after a scoped repair. Connection success, provider health,
offering discovery, benchmark results, and cloud routability are separate states.
Only the default InferOps OpenAI-compatible endpoint can be published; custom
SDK endpoints are ineligible. Provider agent Kubernetes permissions remain
read-only. Cloud gateway metering determines usage/earnings; provider usage
receipts are optional diagnostics and are not enabled just to connect.

Never delete a retained receipt PVC or reset mesh/identity state as a generic
fix. Report provider/cluster identifiers, destination and cloud status. Include
benchmark/routing results only when that additional work was explicitly requested.
If disconnecting is requested, use cloud disconnect; add
--disable-mesh-exposure only when exposure removal is intended. Default disconnect
keeps local workloads and can retain exposure, so verify the requested end state.

Finish a requested repair with resolved context/namespace, provider/cluster IDs,
coordinator/sidecar mode, the scoped change and cloud status result. Report any
remaining setup blocker. Do not generate or execute model-call examples unless
requested. Distinguish configuration/heartbeat success from model routability.
Stopping a foreground CLI/Helm wait does not uninstall Deployments: inspect
existing resources before resuming, and do not tell the user to start over or
paste an API key again when the saved credential still works.
