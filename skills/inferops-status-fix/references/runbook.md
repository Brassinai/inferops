# Diagnose and repair InferOps

Read the bundled [status procedure](status.md) and perform its inventory first.
This skill includes the procedure locally; inferops-status need not be installed.
Record the affected context/namespace/model, observed blocker, relevant evidence,
and intended post-repair state. Then select the smallest supported repair below.

| Evidence | Action and verification |
| --- | --- |
| Kubernetes unreachable | Check the selected context, API reachability, DNS, and user credential validity. Do not switch clusters or disable TLS. On an authorized local k3s host inspect service status/journal before a scoped restart. |
| Forbidden / missing RBAC | Identify the exact resource/verb and identity. Apply a documented least-privilege role only when authorized; otherwise report the missing permission. Never grant cluster-admin as a generic fix. |
| Missing platform installation | Verify Helm releases and namespace first. Install using a measured cache plan only when setup is in the repair scope; do not overwrite a live release with default values. |
| Pending Pod | Inspect FailedScheduling, requests, taints/selectors, quota, node readiness, GPU availability, and cache locality. Correct a proven configuration error; do not remove taints or preempt another model implicitly. |
| WaitingForGPU / WaitingForCapacity | Acknowledge queued work; obtain free compatible capacity, reduce the planned supported resource requirement, or perform a user-requested model switch. Do not assume the queue is a broken controller. |
| Model download 401/403 | Check repository/revision and required HF access/terms. Replace the specific credential through secure input if supplied; do not expose the token or keep retrying authorization failures. |
| ImagePullBackOff | Verify the exact image/tag/platform, registry access, and imagePullSecret reference. Repair the reference or authorized registry credential; do not replace with latest or an unrelated public image. |
| Cache path / capacity failure | Verify the node filesystem, declared cache budget, path permissions, references, and existing Jobs. See cache rules below. Correct only the measured target; do not recursively chmod/chown a shared cache. |
| OOMKilled / runtime failure | Compare container limits, actual host/VRAM capacity, model precision, context, and runtime compatibility. Change only supported model/runtime settings and retest with a bounded inference request. |
| Gateway 503 / model not routed | Check observed Active/Ready state, routing enabled, Service selectors, EndpointSlices, and gateway logs. Fix the actual missing route/upstream; do not expose runtime Pods publicly. |
| Repeated crashes | Inspect the termination reason and bounded relevant logs, correct the cause, then restart only the affected component if required. Stop after two unchanged failures and report evidence. |

## Cache measurements and cleanup

For repair, a targeted cache probe is an ordinary diagnostic step within the
requested repair scope, if permissions and cluster policy allow it:

~~~sh
inferops doctor --context "$CTX" --namespace "$NS" --check cache --output json
inferops cache list --context "$CTX" --namespace "$NS" --output json
~~~

Doctor creates deadline/TTL-bound Jobs with read-only hostPath mounts and cleans
them up; report Forbidden or policy rejection instead of granting elevated access.
Do not call cache delete to promise disk reclamation: it deletes a ModelCache
object and leaves files. Never force-delete a referenced cache or purge a model
used by another deployment. For a user-requested unused-cache cleanup, establish
exact canonical artifact paths, references across relevant namespaces, active
Pods/downloaders, and the cache controller's ownership before removing physical
files through an approved node procedure. If that cannot be established, report
the blocker. Do not use the uninstall cache purge as a live cleanup shortcut.

## Apply, verify, and stop

Perform one evidence-backed repair at a time and preserve recovery information
for changed user configuration. Re-read live resource versions before applying
a change; do not blindly reapply exported status/metadata. For model changes use
its reviewed app/configuration and supported deploy/activation commands. After a
restart verify rollout readiness, then rerun affected status/diagnosis checks.
Use lifecycle timeouts rather than infinite watches. If a requested model is now
Active, validate a small real inference call through the user's gateway route;
status alone is not proof that inference works. Keep existing forwarding/auth.

Return a before/after table with the blocker, action, observed result, and any
remaining recovery step. Do not expand a repair into cluster teardown, model
replacement, public exposure, driver changes, or data deletion outside the user's
scope. Continue all ordinary authorized repairs without repeated approval prompts.
