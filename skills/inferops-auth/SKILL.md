---
name: inferops-auth
description: Enable, disable, or inspect bearer-token authentication on a local InferOps gateway without connecting to InferCloud. Use when changing local model API authentication; disabling requires a local gateway without cloud or external exposure.
---

# Manage local gateway authentication

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


Apply the requested local gateway auth state and verify it. Authentication is a
gateway-wide setting, affecting every model routed through that gateway; it is
independent of InferCloud connection state. Do not connect or disconnect cloud,
change model activation, upgrade images, or change exposure as part of this task.
If the user asks only for commands, show them without execution.

Read the [runbook](references/runbook.md) for discovery, a scoped Helm change,
verification and recovery. This skill is standalone; it needs kubectl and Helm
for a Helm-managed gateway, not an InferOps source checkout or another skill.

Use the user-established context, namespace and gateway. Continue the requested
toggle without asking again when local scope and exposure are established. A
missing target, an unknown/shared public exposure, or a cloud-connected gateway
is a specific scope blocker for disabling auth; do not remove exposure or cloud
configuration to force the toggle. Retain existing credentials when disabling.

Finish with the target, observed auth state, verification, gateway URL, and a
matching model-call example. Never return credentials or claim that changing
local gateway auth changed InferCloud authentication.
