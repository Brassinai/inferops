---
name: inferops-setup
description: Set up InferOps end to end, from missing Kubernetes and Helm prerequisites or an existing-platform upgrade to model selection, deployment, activation, and verified local gateway access without authentication. Use when the user wants the complete setup rather than one individual installation or deployment stage.
---

# Set up InferOps and run a model

Complete the whole requested path: inspect the target, install missing host and
Kubernetes prerequisites, install or upgrade InferOps, choose or validate a model,
deploy and activate it, and return working localhost model calls without gateway
authentication. Reuse healthy existing components and preserve version pins,
workloads, cache data, and credentials. This is an additional combined workflow;
the individual InferOps skills remain available.

An explicit invocation authorizes the ordinary prerequisite installation,
InferOps upgrade, model download/deployment/activation, local auth configuration,
and bounded inference checks needed for that outcome. Honor an explicit
assessment-only or commands-only request.

Gateway auth-off is a local-development outcome, not permission to weaken a
cloud-connected or exposed gateway. Check that boundary before changing an
existing installation. If the gateway has public/LAN/mesh exposure, cloud usage
receipts, or a cloud connection, retain auth and report the concrete conflict.
Do not disconnect cloud, remove exposure, or create another cluster/gateway to
force auth-off without the corresponding user instruction.

Read [targeting and command behavior](references/operations.md), then follow
the [combined runbook](references/runbook.md). Resolve the installed directory
as SKILL_DIR for all bundled helpers. No InferOps checkout or other installed
skill is required. Read stage details only when needed:

- [Installation and upgrade](references/installation.md): Python/SDK/CLI,
  kubectl, Helm, k3s/WSL/kind, persistent cache, and ready platform components.
- [Release selection](references/releases.md) and [capacity sizing](references/capacity.md).
- [Local auth configuration](references/auth.md): exposure checks, a scoped
  declarative auth change, credential retention, and behavior verification.
- [Model deployment](references/deployment.md): model/revision selection,
  resources, cache preparation, activation, and capability checks.
- [Gateway access](references/gateway.md): managed localhost forwarding,
  actual model-call examples, and process lifetime.

The stage references describe narrower workflows; their installation-only or
auth-only stopping points are not completion of this combined request. Continue
through the remaining authorized stages. Their retention, targeting, exposure,
and resource constraints still apply.

Bundled helpers cover host inspection, release resolution, metadata-only model
planning, secure model-registry credential creation, and bounded inference:
[inspect_system.py](scripts/inspect_system.py),
[resolve_release.py](scripts/resolve_release.py),
[model_plan.py](scripts/model_plan.py),
[create_hf_secret.py](scripts/create_hf_secret.py), and
[smoke_inference.py](scripts/smoke_inference.py). For general chat,
[verify_chat.py](scripts/verify_chat.py) also checks two simple answers without
sending an auth token or printing generated text.

Finish only after observed Active/readiness, a running localhost gateway forward,
and successful unauthenticated model inference. Include a meaningful response
check for the intended task; HTTP 200 or a valid stream alone is insufficient.
Return the actual model name/URL, auth state, copyable request, forward restart
and stop instructions, and any remaining blocker. Do not stop after installing
packages or selecting a model, or claim completion of an unverified stage.
