# Switch models with a recovery path

A requested switch authorizes stopping the named current model and starting the
named replacement; it does not authorize eviction of unrelated models. Identify
OLD_MODEL and NEW_MODEL, context/namespace, and the replacement app/configuration.
If several current models are plausible, resolve the selection before stopping
one. Read the locally bundled [deployment procedure](deployment.md) and
[capacity reference](capacity.md); the separate deployment skill is optional.

~~~sh
inferops status "$OLD_MODEL" --context "$CTX" --namespace "$NS" --output json
inferops models --context "$CTX" --namespace "$NS" --output json
inferops gpu list --context "$CTX" --namespace "$NS" --output json
inferops cache list --context "$CTX" --namespace "$NS" --output json
~~~

## Prepare before downtime

Check replacement runtime/architecture, full snapshot disk budget, cache locality,
compute requirements, and credential access before stopping the current model.
If the replacement is new, generate a REVIEWED separate app file and deploy it
Inactive to prepare its cache while the old model serves, where storage allows:

~~~sh
inferops deploy replacement.py --context "$CTX" --namespace "$NS" --output json
inferops status "$NEW_MODEL" --context "$CTX" --namespace "$NS" --watch --timeout 10m --output json
~~~

Inspect the resulting phase; watch success does not prove Cached or Active.
For long downloads inspect existing Job progress rather than restarting them.
If both models' caches cannot coexist, explain the disk blocker before downtime;
do not erase the previous model's recovery cache to force a switch.

Record how to reactivate the OLD_MODEL, its observed settings and cache reference,
and whether it was active. Preserve the old deployment and cache. Avoid dumping
Secret values in backup/configuration files. Explain expected downtime on a
single exclusive GPU. A smoke test and actual benchmark occur after activation;
a preflight resource estimate is not the same as a successful benchmark.

## Switch and verify

~~~sh
inferops deactivate "$OLD_MODEL" --context "$CTX" --namespace "$NS" --timeout 10m --output json
inferops status "$OLD_MODEL" --context "$CTX" --namespace "$NS" --output json
inferops activate "$NEW_MODEL" --context "$CTX" --namespace "$NS" --when-full Reject --timeout 10m --output json
inferops status "$NEW_MODEL" --context "$CTX" --namespace "$NS" --output json
~~~

Stop the sequence if old deactivation is incomplete. Reject prevents a concurrent
capacity change from preempting someone else. Require Active/readiness on the
replacement, then use the existing gateway or establish a managed forward and
run the bundled bounded smoke_inference.py against NEW_MODEL. Report the working
endpoint and the actual observed sample. Do not remove the old cache afterward
unless cleanup was also requested.

## Recover on failure

If activation timed out, inspect live state first; it may still finish. If the
replacement fails or inference is broken, diagnose it and first deactivate it
with a bounded wait so it cannot later acquire the slot. Once it is actually
inactive, reactivate the old model if it was active before the switch:

~~~sh
inferops deactivate "$NEW_MODEL" --context "$CTX" --namespace "$NS" --timeout 10m --output json
inferops activate "$OLD_MODEL" --context "$CTX" --namespace "$NS" --when-full Reject --timeout 10m --output json
~~~

Verify old readiness and inference again. If either transition remains unknown,
report that blocker instead of issuing competing activation requests. Never
claim recovery merely because an activate request was accepted. Return old/new
observed states, downtime if measured, verification results, and retained caches.
