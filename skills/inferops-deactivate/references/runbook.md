# Safely stop a model and retain its cache

Resolve the target model explicitly. If the user says stop without identifying
one and several are active, inspect the list and ask only for the ambiguous
selection. Do not stop every model to obtain free capacity.

~~~sh
inferops models --context "$CTX" --namespace "$NS" --output json
inferops status "$MODEL" --context "$CTX" --namespace "$NS" --output json
inferops cache list --context "$CTX" --namespace "$NS" --output json
inferops gpu list --context "$CTX" --namespace "$NS" --output json
~~~

Record desired state, observed phase, assigned node/GPU, cache reference, and
drain timeout. Cached plus desired Inactive is already stopped; report that
without deleting or redeploying it. A model being downloaded or activated still
needs its desired state changed if the user requested it stopped.

~~~sh
inferops deactivate "$MODEL" --context "$CTX" --namespace "$NS" --timeout 10m --output json
inferops status "$MODEL" --context "$CTX" --namespace "$NS" --output json
inferops gpu list --context "$CTX" --namespace "$NS" --output json
inferops cache list --context "$CTX" --namespace "$NS" --output json
~~~

The normal command waits for draining and observed deactivation. Choose a timeout
that accommodates the configured drainTimeout; 10m is a bounded example, not a
forced shutdown deadline. Do not use --no-wait and then claim completion. On
exit 12 the operation may continue: inspect observed state before any retry.
Use diagnose MODEL for an unexplained blocker. Do not bypass finalizers, kill
runtime Pods, or delete caches to force the state transition.

Completion requires desired Inactive, no serving runtime/active route, and a
non-serving stable observed state (normally Cached with a retained cache).
Compare runtime Pods and GPU reservations to the baseline, accounting for other
workloads that may immediately occupy the freed slot. Do not promise an exact
free-RAM change from a Kubernetes request alone. If the model is cloud-connected,
account for eventual heartbeat propagation; local deactivation is not immediate
proof that every remote client has observed it.

Return the final model state, draining outcome, released compute where observed,
retained deployment/cache, and any unfinished step. To start it again provide:

~~~sh
inferops activate "$MODEL" --context "$CTX" --namespace "$NS" --when-full Queue --timeout 10m
~~~

Deactivation never implies deletion of model artifacts or credentials.
