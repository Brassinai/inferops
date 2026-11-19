---
name: inferops-install
description: Install or upgrade the full InferOps environment, including SDK, CLI, Kubernetes prerequisites, operator, gateway, runtimes, and measured cache sizing. Use for initial setup or bringing an existing installation to the newest published operator image.
---

# Install InferOps

## Diagnose and recover within the task

When an authorized operation fails or stalls, investigate the actual cause using
current status, events, and bounded logs. Apply a targeted InferOps repair within
the requested outcome, retry after the relevant state changes, and verify the
result; do not stop at the first error or wait for the user to request debugging.
Preserve unrelated workloads, credentials, data, and explicit user constraints.
If a fix needs missing authorization for a system-wide, destructive, or external
action, explain the concrete action and ask before performing it; continue any
independent authorized work. If recovery is impossible, report what was tried,
the observed blocker, and the next actionable step. Respect commands-only,
assessment-only, and status-only requests.


Own the complete setup: inspect the host and target nodes, install or update the
SDK and CLI from upstream, create or reuse Kubernetes, provision persistent model
cache storage, install CRDs/operator/gateway/runtime definitions, and verify readiness.
Resolve the newest published operator image tag and its source commit; upgrade an
existing operator while preserving companion image versions declared by its source
charts and existing settings. Do not require other packages to share the operator
tag. An explicit tag or stable-only
request takes precedence. Missing GitHub releases alone do not block the default
operator-image path.

Measure disk cache, RAM, and GPU memory separately on the actual node/VM. Configure
cache capacity from measured free disk after reserves; use RAM/VRAM measurements
to establish compute limits rather than treating disk cache as memory. Installation
is complete only when the cluster platform and persistent cache are usable, not
when client packages alone are installed.

Read [targeting and command behavior](references/operations.md), then follow
the [runbook](references/runbook.md). Resolve the installed skill directory
as SKILL_DIR when invoking bundled helpers. This skill is self-contained;
neither the InferOps source checkout nor sibling skills are required.

Read or execute only the supporting resources needed for the current step:

- [capacity.md](references/capacity.md)
- [releases.md](references/releases.md)
- [inspect_system.py](scripts/inspect_system.py)
- [resolve_release.py](scripts/resolve_release.py)

Finish with observed results, verification performed, and any specific blocker.
Continue ordinary steps already authorized by the user; do not expand scope
to unrelated workloads, destructive cleanup, or external exposure.
