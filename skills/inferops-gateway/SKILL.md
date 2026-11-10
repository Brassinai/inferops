---
name: inferops-gateway
description: Forward the InferOps gateway locally and provide working model invocation examples. Use when accessing or testing a deployed model through the gateway.
---

# Access the InferOps gateway

Resolve an active routed model, establish a managed localhost forward, verify inference, and provide accurate client examples and process lifetime instructions.

Read [targeting and command behavior](references/operations.md), then follow
the [runbook](references/runbook.md). Resolve the installed skill directory
as SKILL_DIR when invoking bundled helpers. This skill is self-contained;
neither the InferOps source checkout nor sibling skills are required.

Read or execute only the supporting resources needed for the current step:

- [smoke_inference.py](scripts/smoke_inference.py)

Finish with observed results, verification performed, and any specific blocker.
Continue ordinary steps already authorized by the user; do not expand scope
to unrelated workloads, destructive cleanup, or external exposure.
