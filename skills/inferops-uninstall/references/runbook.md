# Uninstall InferOps and account for retained data

Resolve the exact context/namespace and requested removal scope. Default removal
keeps CRDs, custom resources, node-local model files, Kubernetes, and independent
monitoring. Extra scope may include CRD deletion, physical cache purge, removing
mesh exposure, or deleting a dedicated local cluster; do not infer those from
uninstall InferOps. Reuse prior user authorization and do not ask again for the
same scope. Review the installed uninstall --help before destructive options.

## Inventory and stop workloads while the operator still exists

~~~sh
helm list -A --kube-context "$CTX"
inferops models --context "$CTX" --namespace "$NS" --output json
inferops cache list --context "$CTX" --namespace "$NS" --output json
inferops cloud status --context "$CTX" --namespace "$NS" --output json
~~~

If cluster-wide release listing is Forbidden, use helm list --namespace "$NS"
--kube-context "$CTX" for the requested namespace and mark wider ownership unknown.


Record affected releases, models, namespaces, cloud/mesh state, and cache roots.
Cloud not configured is expected for a local installation. Identify any other
InferOps installations sharing the cluster-scoped CRDs, nodes, or filesystem.
For CRD removal inventory ModelDeployments, ModelCaches, and ModelRuntimes across
ALL namespaces; insufficient access is a blocker to deleting shared CRDs.

Deactivate each affected model with the normal draining workflow BEFORE removing
the operator. Inspect each final state and verify its runtime is no longer
serving. Do not assume uninstalling Helm will stop CR-owned runtime Deployments.

~~~sh
inferops deactivate "$MODEL" --context "$CTX" --namespace "$NS" --timeout 10m --output json
inferops status "$MODEL" --context "$CTX" --namespace "$NS" --output json
~~~

If diagnostic receipt queues contain pending records, preserve the agent,
credentials, and PVC needed for their delivery until reconciliation or an explicit
durable handoff is verified. Do not disconnect first and strand those records.
If connected and queue handling is complete, remove the agent/connection while
the gateway/credentials still exist. For removal of managed mesh exposure as part of this uninstall use:

~~~sh
inferops cloud disconnect --context "$CTX" --namespace "$NS" --disable-mesh-exposure --output json
~~~

Inspect retained receipt queues/PVCs if diagnostic reporting was enabled. Never
delete a queue with pending receipts to complete an uninstall. Default disconnect
without the exposure flag may leave exposure; report its state explicitly.

## Choose the requested uninstall mode

Normal platform removal:

~~~sh
inferops uninstall --context "$CTX" --namespace "$NS" --output json
~~~

This removes the operator, gateway, and optional dashboard Helm releases,
preserves CRDs/custom resources and node-local files, and does not remove
kube-prometheus-stack, Python packages, k3s, or kind. The cloud agent is removed
by the preceding cloud disconnect; uninstall itself does not remove that release. Inspect returned
resources/release outcomes; already absent releases are not proof about runtimes.

Only when ALL InferOps custom resources across the cluster may be removed:

~~~sh
inferops uninstall --context "$CTX" --namespace "$NS" --crds --output json
~~~

Deleting CRDs removes every InferOps custom resource in every namespace. Do not
use it as a namespace-scoped cleanup, and do not attempt it on a cluster with an
unrelated InferOps installation. Export resources only through a sanitizing
procedure; never save credentials or raw Secret exports into the repository.

If physical cache deletion is explicitly requested, verify the canonical cache
root, exact node selector, all deployment/cache references, and all active model
and download Pods across the affected nodes/namespaces. Ensure no other system
uses the path. The current CLI requires explicit purge arguments and a token:

~~~sh
inferops uninstall --context "$CTX" --namespace "$NS" --purge-cache-files --cache-path "$CACHE_PATH" \
  --cache-node-selector "$CACHE_SELECTOR" --confirm-cache-purge DELETE-CACHE-FILES --output json
~~~

The token is a CLI guard, not a substitute for user authorization. This operation
runs a root hostPath purge DaemonSet on matching nodes and runs purge BEFORE Helm
removal. Therefore all users/downloaders must already be stopped; if that cannot
be established, do not invoke it. Never point it at /, a home directory, or an
unverified shared mount. Do not run the purge as a live cluster cache-cleaning
shortcut. Combining --crds requires the independent cluster-wide scope above.
Cache object deletion alone does not free files.

## Verify removal

Inspect Helm releases, remaining runtime/control-plane Pods, custom resources,
Services/mesh exposure, retained PVCs/Secrets and cache files as permitted. Report
exactly what was removed and retained, any credentials requiring cloud-side
revocation, and any unfinished draining or receipt reconciliation. Do not dump
Secret contents. Remove a dedicated kind/k3s cluster only when separately included
in scope and ownership is established; use its official removal workflow rather
than deleting arbitrary Kubernetes directories. Finish with a retained-resource
inventory, not a blanket claim that every trace of InferOps is gone.
