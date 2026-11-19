---
name: inferops-deactivate
description: Safely deactivate an InferOps model while retaining its configuration and cache. Use when stopping a model or releasing its inference resources.
---

# Deactivate an InferOps model

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


Resolve the intended model, request normal draining and deactivation, verify non-serving observed state and resource release, and preserve the deployment and cache.

Read [targeting and command behavior](references/operations.md), then follow
the [runbook](references/runbook.md). Resolve the installed skill directory
as SKILL_DIR when invoking bundled helpers. This skill is self-contained;
neither the InferOps source checkout nor sibling skills are required.

Finish with observed results, verification performed, and any specific blocker.
Continue ordinary steps already authorized by the user; do not expand scope
to unrelated workloads, destructive cleanup, or external exposure.
