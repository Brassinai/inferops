---
name: inferops-auth
description: Enable, disable, or inspect bearer-token authentication on a local InferOps gateway without connecting to InferCloud. Use when changing local model API authentication; disabling requires a local gateway without cloud or external exposure.
---

# Manage local gateway authentication

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
