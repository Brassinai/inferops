# Complete setup through working local inference

## Establish the requested target and scope

Resolve the host, Kubernetes context/namespace, requested version, model and
capabilities from the current request and session. Reuse the established target;
do not reinterpret an unreachable cluster as permission to create a replacement.
If the user delegated model selection, choose a feasible model rather than
stopping at a shortlist. With no task specified, use general text chat as the
default and state that assumption. Preserve explicit vision, coding, runtime,
model, hardware, and version requirements.

Inspect host tools, existing contexts/releases, node architecture and effective
RAM/VRAM, disk/cache capacity, existing models, gateway ownership/exposure, and
cloud connection metadata. A missing tool on a fresh host is a prerequisite to
install; Forbidden or a failed inspection on an existing cluster is unknown,
not proof that the component is absent. The [installation procedure](installation.md)
covers host and cluster bootstrap, and [capacity sizing](capacity.md) distinguishes
node/VM measurements from the workstation controlling them.

For an existing gateway, perform the exposure/cloud checks in
[local authentication](auth.md) before upgrades, downloads, or auth changes.
The unauthenticated outcome is supported only on a local-development gateway
without public/LAN/mesh exposure, cloud connection, or cloud usage receipts.
If that cannot be established, preserve auth and explain the specific blocker
before starting work that cannot meet the requested outcome. A ClusterIP
Service alone is not proof: inspect mesh sidecars, ingress, Gateway API routes,
host networking, and cloud metadata. Gateway auth applies to every routed model.

## Install missing prerequisites and make InferOps current

Follow [installation and upgrade](installation.md) through its platform readiness
checks, using the bundled [release resolver](../scripts/resolve_release.py) and
[capacity guidance](capacity.md). This covers the SDK and CLI as well as the
Kubernetes operator, gateway, runtime definitions and persistent model cache.

- Install missing Python/venv, Git, kubectl and compatible Helm tooling using
  verified official sources. Select the Helm 3 release line supported by the
  current InferOps CLI rather than replacing it with an arbitrary latest major.
- On a new Linux host use the k3s path after inspecting its service manager,
  networking, storage and compute prerequisites. Pin the selected k3s version,
  review the downloaded installer, protect kubeconfig, and verify Ready nodes.
  Reuse an existing healthy cluster; do not install k3s over it.
- On macOS reuse a suitable existing context, or use the documented kind CPU
  path with a working container engine and persistent cache mount. Do not run
  the Linux k3s installer on macOS. On Windows use the WSL2 path or an existing
  cluster; an actual OS/session restart is a concrete handoff, not something
  to issue silently while the agent depends on that session.
- Measure disk, memory and GPU capacity on the actual node/VM. Prepare missing
  persistent storage and its bounded cache budget. Verify Kubernetes GPU
  allocation when required; host GPU detection does not establish Pod support.
- Resolve the published operator image and its matching SDK/CLI source once.
  On a new install use the individually verified companion defaults, then apply
  the scoped operator upgrade if the resolver requires it. On an existing
  platform retain release values, companion/runtime images, models and cache.
  Do not propagate an operator commit tag to every container package.

An explicit setup invocation includes the necessary install/upgrade steps;
continue ordinary authorized work without requiring a second confirmation for
each missing component. Preserve a tighter user scope or version pin. Do not
enlarge a shared VM, reboot a shared host, stop unrelated workloads, migrate
occupied caches, or grant broad RBAC merely to make the setup fit.

Do not continue model installation until the selected node, operator, gateway,
runtime and measured persistent cache are usable. A completed installation
stage is not completion of this skill; continue below.

Official prerequisite references: [k3s installation](https://docs.k3s.io/quick-start)
and [Helm 3 installation](https://helm.sh/docs/v3/intro/install/).

## Configure local gateway authentication

Follow [the auth procedure](auth.md) to recheck exposure and identify the gateway's
actual Helm release/chart, current revision and existing auth Secret references.
If auth is already off, retain that state and verify it after deployment.
Otherwise render and privately review the auth-only delta, apply
`auth.enabled=false` through the matching declarative owner, and wait for a
successful gateway rollout. Preserve credentials for later re-enabling and
retain unrelated values; do not patch only the live Deployment when Helm owns it.
Carry the selected `--kube-context "$CTX"`, `--namespace "$NS"` and any supplied
kubeconfig through both the render/review and upgrade commands in that procedure.

For a fresh unexposed gateway, configuring auth off is part of this workflow;
do not require the user to supply a gateway token just to reach the requested
local setup. The fresh-install defaults already use an unauthenticated ClusterIP
gateway; retain those defaults instead of adding external exposure or an
unnecessary auth Secret/toggle. Model-registry access is separate: a gated model may still require
its authorized Hugging Face credential. Never print credentials or put them
in process arguments, generated app files, or model-call examples.

If no model is active yet, an unauthenticated model route can distinguish 401
from a routing error but cannot prove successful inference. Defer the complete
auth-off behavior check until the new model is Active. A successful /healthz
request is not an authentication test.

## Select, deploy and activate the model

Follow [the deployment procedure](deployment.md), using its copies of the
metadata planner, credential helper and inference checker. Assess model/runtime
compatibility and complete snapshot storage against measured remaining budgets;
do not treat parameter count, a filename, or a healthy API as capability proof.
Pin the resolved repository revision and use conservative supported context,
concurrency and resource requests. Record the generated app/runtime configuration
so interrupted work can resume without reselecting or redownloading unnecessarily.

Before reusing a deployment, confirm that its repository, pinned revision and
runtime satisfy the requested configuration. If it is already Active, reuse and
verify it. If its phase is Cached and desiredState is Inactive, activate it.
Inactive is a desired state, not an observed lifecycle phase. If a download or
activation is already in progress, inspect and resume it rather than submitting
duplicate deployments or resetting the transition.
A missing desired model should be deployed with its configured deployment name,
then activated with Queue/Reject capacity policy. Do not preempt another model
or switch models implicitly. Cached weights alone are not a serving deployment.

The install stage's provisional cache budget may need recalculation for the
selected full snapshot and staging space. Expand only within measured safe free
space and the user's budget, preserving retained artifacts/reservations. Reuse
verified matching caches. If the requested model cannot fit, choose a smaller
candidate only when model selection was delegated and it still meets the task;
otherwise explain the measured shortfall.

## Verify the actual unauthenticated gateway request

Follow [gateway access](gateway.md) to establish a managed forward bound to
127.0.0.1, selecting a free local port without killing unrelated listeners. Keep
the context/namespace and actual Service/remote port fixed. A remote execution
machine's localhost is not automatically the user's laptop.

Use the real deployment name at
`/models/DEPLOYMENT_NAME/v1/chat/completions`. Run the model check without an
Authorization header, including when the parent shell has an old token:

~~~sh
env -u INFEROPS_GATEWAY_TOKEN python3 "$SKILL_DIR/scripts/smoke_inference.py" \
  --gateway-url "$GATEWAY_URL" --model "$MODEL" --requests 1 --timeout 60
~~~

Remove the token only from the probe's child environment; do not delete the
user's stored credential. Require observed Active, ready/routing conditions,
status.observedGeneration matching metadata.generation, HTTP 200 with structurally
valid inference, and the requested auth-off behavior. An old Active status from
before a spec change does not establish readiness for the requested configuration.
For general chat also run the bundled behavioral check:

~~~sh
python3 "$SKILL_DIR/scripts/verify_chat.py" \
  --gateway-url "$GATEWAY_URL" --model "$MODEL" --timeout 60
~~~

It sends two independent synthetic requests (a greeting and simple arithmetic),
requires the corresponding concise answers and completed assistant messages,
and prints only check results. It always omits Authorization, regardless of the
shell's token, and uses only the localhost forward. A canned greeting cannot
pass both checks. Failure is a diagnostic signal, not proof that the model lacks
chat support: inspect prompt formatting, truncation, runtime settings and model
suitability before repeating or declaring success. These samples do not certify
general model quality. Check any additional requested task with bounded synthetic
input too. Keep generated text out of diagnostic logs.
For vision, also require the contrasting image-bearing checks in the deployment
procedure; text-only success does not establish image understanding.

Use a measured timeout and output budget appropriate to this runtime. If a stage
fails, diagnose its concrete cause and apply scoped repairs within the request.
Stop after two unchanged failed attempts and inspect fresh evidence. Do not
erase state, switch clusters, change exposure, or claim completion to bypass a
blocker. A partial run should report each unfinished stage and preserve its
configuration and current lifecycle state for resumption.

## Hand off a working model

Return the observed setup/upgrade result, actual model and runtime/revision,
context/namespace, localhost origin and full model API URL, auth-off status,
and successful inference/capability checks. Include a complete copyable request
with the actual model and port, omitting Authorization. Include a runnable image
request when vision was requested. Report the forward's managed process/session,
exact stop method and restart command; a local forward is not permanent hosting.
The deployment procedure's final model-call examples show the required shape.
