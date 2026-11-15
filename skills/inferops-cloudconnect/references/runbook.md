# Connect a provider cluster to InferCloud

## Preflight

Treat a follow-up after an interrupted command or accepted heartbeat as recovery,
not fresh onboarding. Collect the saved connection and relevant Helm/rollout
state in parallel, reusing the established context and namespace. Do not reinstall
the platform, research new models or repeat package discovery for a connection repair.

Inspect the selected InferOps installation and any existing connection. The
connect CLI checks its platform prerequisites; do not repeat unrelated inventory.
Resolve the user-provided cloud API origin and cluster name.
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
inferops cloud status --context "$CTX" --namespace "$NS" --output json
~~~

An absent connection is expected for first-time setup. If the existing connection
already matches the requested destination and is healthy, report its cloud
status rather than redeeming credentials repeatedly. A different connection or
provider account needs an explicit migration scope; do not silently
overwrite it. Newly activated offerings can be discovered after connection.

If matching connection resources exist but are unhealthy, resume them: preserve
provider/cluster IDs, agent credential, mesh identity, gateway token and models.
A missing local ConfigMap after an interrupted Helm command does not prove
redemption never occurred; inspect agent settings, Secret name/key existence,
Helm state and authorized cloud connection status. Never create another cluster
merely because routability is false. Do not use disconnect/reconnect or erase
Tailscale state as a generic repair.

## Connect once, then report cloud status

Use INFEROPS_CLOUD_TOKEN from the user's secure environment or --token-stdin
through a secure input channel. The environment path avoids key material in
arguments, shell history, transcript, and command output:

~~~sh
inferops cloud connect --context "$CTX" --namespace "$NS" --cloud-api-url "$CLOUD_API_URL" \
  --cluster-name "$CLUSTER_NAME" --timeout 2m --output json
inferops cloud status --context "$CTX" --namespace "$NS" --output json
~~~

The connect command reads INFEROPS_CLOUD_TOKEN when neither token flag is passed.
If --token-stdin is used, do not also pass --token or pipe a literal secret into
a tool call. Do not log environment variables. Remove a temporary task-created
token variable after use without changing a preexisting user's environment.

Use this initial connect only for new setup or an evidence-backed reconfiguration.
The 2m timeout is a bounded initial rollout budget, not a performance guarantee;
extend it for demonstrated image-pull/startup progress, not repeated auth failure.
Gateway and agent Helm operations are sequential and EACH receives the timeout,
not one total deadline. The agent release uses --atomic, which can wait even with
--no-wait. Do not use --no-wait to claim connection or bypass a failing rollout.

Run connect in a managed session and inspect Pod readiness, events and sanitized
bounded sidecar logs alongside it. Report the stage being waited on. Detect
expired/invalid keys, denied permissions and deterministic crashes immediately
rather than repeating them until timeout. If ending an in-flight CLI/Helm wait,
inspect pending release state and running resources before another mutation;
never run concurrent upgrades on the same release or edit Helm storage Secrets.
Stop repeating unchanged failures after two attempts and use fresh evidence.

Default hosted mesh exposure is the InferCloud-managed Tailscale sidecar. Choose
--mesh-mode operator only when a provider-owned Tailscale operator is installed
and that mode was requested. Connect installs the cloud agent and authenticated
mesh gateway exposure, and stores the agent credential in
Secret/inferops-cloud-connection, gateway auth in Secret/inferops-gateway-auth,
and non-secret metadata in ConfigMap/inferops-cloud-connection. Inspect Secret
existence/key NAMES only; never dump values or full gateway Helm values.

## Complete the connection handoff

Use inferops cloud status as the routine post-connect check. Return the connect
result, existing provider/cluster IDs, destination, its status summary and a
copyable status command for the resolved target. Normal agent/mesh configuration
and the connect command's rollout waits are part of setup; an additional cloud
reachability or inference test is not a completion requirement.

Do not manually ping peers, make HTTP/model requests from the cloud gateway's
network, query recorded reachability evidence, run inference or benchmarks, or
wait for publication/routability as part of a plain connect/resume request. Do
not add --benchmark-offerings by default or toggle an existing benchmark setting
incidentally. Those actions require an explicit test/benchmark/routing diagnosis
request. Existing cloud background health/probe loops continue independently;
this skill does not disable them or change the cloud's routing safeguards.

If status reports an accepted heartbeat but a model is not routable, report that
as a separate status finding and stop. Include any reason already returned by
status without starting another investigation. Do not infer that cloud status
certifies model inference or every publication gate. The user can request a
separate status/repair or model-call workflow. Do not disconnect/re-register or
ask for the credential again merely because routability is false.

## Setup errors and requested troubleshooting

For a failed setup or an explicitly requested diagnosis/repair, read
[troubleshooting](troubleshooting.md) and inspect only evidence relevant to the
failure. Additional cloud reachability, model requests and benchmarks still
require an explicit request; they are not follow-up steps for a normal connect.
