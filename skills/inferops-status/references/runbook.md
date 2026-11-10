# Inspect InferOps without repairs

## Inventory and platform checks

Resolve the target and relevant namespaces; a default namespace with zero models
is not proof that the cluster has zero models. Use all-namespace model metadata
only when access permits, then inspect each relevant namespace explicitly.

~~~sh
kubectl --context "$CTX" get nodes -o wide
helm list -A --kube-context "$CTX"
kubectl --context "$CTX" -n "$NS" get pods,deployments,services
inferops models --context "$CTX" --namespace "$NS" --output json
inferops endpoints --context "$CTX" --namespace "$NS" --output json
inferops gpu list --context "$CTX" --namespace "$NS" --output json
inferops cache list --context "$CTX" --namespace "$NS" --output json
inferops doctor --context "$CTX" --namespace "$NS" --check kubernetes-api --check device-plugin \
  --check gpu-capacity --check runtime-class --check gateway --check tailscale --output json
~~~

If cluster-wide release listing is Forbidden, use helm list --namespace "$NS"
--kube-context "$CTX" for the requested namespace and mark wider ownership unknown.


The selected doctor checks avoid its cache check, which creates and cleans up
probe Jobs. This status skill reports existing resources and does not create
probes or repair configuration. A complete report must label filesystem capacity
unknown unless measured through existing authorized host access. Include cached
artifact metadata/references, declared node cache-capacity annotations, and
existing download Jobs. Do not mistake declared cache size for measured free disk.
Optional Tailscale absence and no GPU in a CPU-only cluster are not deployment
failures; interpret checks against the installation's intended compute/exposure.

## Model and connection checks

For each affected model, resolve MODEL and run:

~~~sh
inferops status "$MODEL" --context "$CTX" --namespace "$NS" --output json
inferops diagnose "$MODEL" --context "$CTX" --namespace "$NS" --output json
~~~

The actual CLI requires a model name for status. Inspect the diagnosis phase,
blockingStep, problem resource/reason, and checkedResources. Do not copy an entire
JSON report or logTail into chat: redact tokens, authorization values, request
bodies, and generated text. Use relevant condition reasons and event evidence;
never execute a suggestedFixes string without independent review.

If cloud connection metadata exists, run:

~~~sh
inferops cloud status --context "$CTX" --namespace "$NS" --output json
~~~

Otherwise report cloud as not configured. Inspect optional monitoring resources
only if installed. Limit log queries to relevant Pods with --tail and --since;
prefer condition/event evidence when log contents may contain application data.
A failed model can coexist with a ready platform, and command exit 0 can coexist
with Failed phase. Distinguish these outcomes in the report.

## Report contract

Return context/namespaces and a table with component or model, observed state,
blocker, evidence, and next action. Include model desired/observed state, routing,
cache node/reference state, GPU availability/occupancy, and any known disk/RAM
measurements. Mark checks as healthy, blocked, not configured, or unknown with
an explanation; never translate Forbidden, timeout, or missing access into a
healthy result. No restarts, installs, pod deletion, cleanup, or credential reads.
