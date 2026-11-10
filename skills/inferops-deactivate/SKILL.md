---
name: inferops-deactivate
description: Safely deactivate an InferOps model while retaining its configuration and cache. Use when stopping a model or releasing its inference resources.
---

# Deactivate an InferOps model

Resolve the intended model, request normal draining and deactivation, verify non-serving observed state and resource release, and preserve the deployment and cache.

Read [targeting and command behavior](references/operations.md), then follow
the [runbook](references/runbook.md). Resolve the installed skill directory
as SKILL_DIR when invoking bundled helpers. This skill is self-contained;
neither the InferOps source checkout nor sibling skills are required.

Finish with observed results, verification performed, and any specific blocker.
Continue ordinary steps already authorized by the user; do not expand scope
to unrelated workloads, destructive cleanup, or external exposure.
