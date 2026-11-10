---
name: inferops-status
description: Inspect InferOps cluster, model, cache, and connection health and report blockers. Use for a status assessment without repairs.
---

# Inspect InferOps status

Collect existing resource evidence and report observed health, blockers, impact, and next actions without changing workloads or creating cache probes.

Read [targeting and command behavior](references/operations.md), then follow
the [runbook](references/runbook.md). Resolve the installed skill directory
as SKILL_DIR when invoking bundled helpers. This skill is self-contained;
neither the InferOps source checkout nor sibling skills are required.

Finish with observed results, verification performed, and any specific blocker.
Continue ordinary steps already authorized by the user; do not expand scope
to unrelated workloads, destructive cleanup, or external exposure.
