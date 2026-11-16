# Toggle local gateway bearer-token authentication

## Resolve ownership and exposure

Resolve CTX, NS and the gateway Deployment/Service from the user or existing
session. Use explicit --context for kubectl and --kube-context for Helm; do not
change global kubeconfig context. Carry a supplied --kubeconfig consistently.
If the target is unknown, inspect context names and release metadata. Discover
the Helm release name/namespace from the Deployment's meta.helm.sh annotations;
do not assume every installation uses inferops-system or default.

~~~sh
kubectl config get-contexts -o name
helm list --kube-context "$CTX" --namespace "$NS"
kubectl --context "$CTX" -n "$NS" get deployment,service,ingress
kubectl --context "$CTX" -n "$NS" get configmap inferops-cloud-connection --ignore-not-found -o name
kubectl --context "$CTX" -n "$NS" get deployment inferops-cloud-agent --ignore-not-found -o name
~~~

Inspect only relevant configuration fields: gateway container image and
INFEROPS_GATEWAY_AUTH_TOKEN_FILE presence, cloud usage queue presence, mounted
auth Secret name/key, Service type/externalIPs/mesh annotations, ingress and
Gateway API routes, sidecars, hostNetwork/hostPort, and gateway Helm auth/exposure
settings. Auth enabled locally does not imply cloud connectivity. Missing cloud
receipt configuration does not prove there is no connection: also check the
connection metadata, agent, configured mesh and exposure. Permission errors mean
unknown, not absent. Check that any forward binds to 127.0.0.1.

Never print Secret values, environment credentials, full Pod specs or full Helm
values/manifests. Capture Helm JSON/YAML privately and output only selected
non-secret fields or a redacted diff. Do not retrieve credentials merely to
inspect auth status. A local ClusterIP gateway still serves other cluster
clients; state that the toggle affects all models and clients of that gateway.

Disable auth only for the user's local-development gateway without configured
public/LAN/mesh routes, cloud connection, or cloud usage receipts. If exposure is
shared or uncertain, report the exact blocker; do not silently disable cloud
features, weaken a public endpoint, or create a second gateway. Local enablement
does not require cloud setup. For status-only requests, stop after inspection.

## Preserve the release and select its chart

Record the current Helm revision, auth enabled flag and Secret name/key. Verify
the gateway's actual release/chart source, pinned version and deployed image.
Use the same trusted chart version or a verified matching chart from the
installed CLI package. Obtain a missing chart from the installation's verified
upstream source/revision; never depend on a developer's local checkout path.

For a packaged CLI chart, resolve it using the CLI's Python environment:

~~~sh
python3 - <<'PY'
from importlib.resources import files
print(files("inferops_cli").joinpath("charts", "inferops-gateway"))
PY
~~~

Use the interpreter that actually contains inferops_cli. Confirm Chart.yaml and
that rendering it with the existing values matches the installed manifest before
changing auth; matching chart version strings alone are insufficient. Compare
parsed resources privately so credentials cannot leak. Do not use a newer chart
or change image tags as an incidental auth toggle. If an exact/matching chart
cannot be obtained, report that rather than fall back to a live Deployment patch
that Helm would overwrite later. Non-Helm installations require an explicitly
identified declarative owner and an equivalent scoped change to its source.

## Enable or disable

If already in the requested state, skip the Helm upgrade and verify. For enabling,
reuse the existing auth Secret/key and confirm its existence and key names
without dumping values. If no usable Secret exists, create one through kubectl
stdin, using an existing securely supplied INFEROPS_GATEWAY_TOKEN or a newly
generated strong random token. Save a generated token only in a user-owned
credential file with mode 0600 inside a mode-0700 directory; report its path,
not its contents. Do not place tokens in arguments, shell history, tool output
or source files. Do not overwrite/rotate an existing Secret as part of a toggle.
Reuse the local gateway token, not an InferCloud consumer/provider credential.

Set RELEASE and GATEWAY_CHART to the verified release/chart reference and, for
enabling, AUTH_SECRET and AUTH_KEY to the actual names. For a versioned upstream
chart reference also pass its exact --version. Helm 3 supports these bounded,
rollback-on-failure upgrades:

~~~sh
# Enable local bearer-token authentication.
helm upgrade "$RELEASE" "$GATEWAY_CHART" --kube-context "$CTX" --namespace "$NS" \
  --reuse-values --set auth.enabled=true \
  --set-string auth.secretName="$AUTH_SECRET" --set-string auth.secretKey="$AUTH_KEY" \
  --atomic --timeout 3m
~~~

~~~sh
# Disable local auth; retain Secret and its release references for re-enabling.
helm upgrade "$RELEASE" "$GATEWAY_CHART" --kube-context "$CTX" --namespace "$NS" \
  --reuse-values --set auth.enabled=false --atomic --timeout 3m
~~~

Only apply the requested auth delta after privately reviewing rendered changes.
Retain workload resources, runtime image, cache, models, rate limits, existing
exposure and unrelated values. Do not use helm --install to conceal a missing
release, uninstall anything, edit Helm storage Secrets, or remove the auth Secret
when disabling. Do not disable auth merely to make an inference probe pass.

## Verify and return model access

Wait for gateway rollout; inspect ready containers and the configured auth flag
again. A Helm success alone is not proof of request behavior. A rollout can end
an existing port-forward; re-establish the user's same localhost port if free
and verify it is listening. Do not kill an unrelated listener. Keep the managed
session id, restart command and stop method. Explain brief gateway disruption
if a forward needed replacement; do not restart model runtimes.

~~~sh
kubectl --context "$CTX" -n "$NS" rollout status deployment/"$GATEWAY_DEPLOYMENT" --timeout=120s
inferops models --context "$CTX" --namespace "$NS" --output json
inferops gateway forward --context "$CTX" --namespace "$NS" --local-port 8080
~~~

Replace the illustrative 8080 with the user's existing local port and substitute
custom Service/remote port when present. After disabling, make one
bounded unauthenticated request to an existing Active model and require HTTP 200
with a valid inference result. If no model is Active, use a model route to check
that the gateway no longer returns 401, and distinguish routing/readiness failure
from auth success; do not activate a model just for this test.

After enabling, require a no-token model request to return 401 and a bounded
request with the valid local token to succeed for an Active model. Use a secure
environment or stdin for the authorized request. If necessary, load the specific
authorized local auth Secret privately into the request process; never print it
or pass the token in arguments. Health probes may be unauthenticated, so /healthz
alone is not an authentication test. Keep generated model output and request
bodies out of diagnostic logs; report only status and structural validity.

If rollout or behavior fails, inspect bounded evidence and return the specific
blocker. Preserve the previous revision for recovery; a rollback is appropriate
only if it restores the reviewed prior release and no newer user change would
be overwritten. Do not repeatedly retry an unchanged failure.

Return observed before/after auth state, local target/URL, verification results,
and a copyable request using the actual model/port. Auth-off examples omit a
token; auth-on examples securely read the local gateway token. State explicitly
that no cloud connection was added. A local ClusterIP/port-forward is not a
publicly hosted endpoint, and disabling local auth does not disable InferCloud
consumer authentication.
