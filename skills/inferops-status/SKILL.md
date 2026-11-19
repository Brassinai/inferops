---
name: inferops-status
description: Inspect InferOps cluster, model, cache, and connection health and report blockers. Use for a status assessment without repairs.
---

# Inspect InferOps status

## Diagnose without changing state

Investigate errors and inconsistent status with available read-only evidence
before reporting a cause and actionable next steps. This status-only skill does
not authorize repairs; if the user also requests a fix, use a repair workflow
and its authorization boundaries.


Collect existing resource evidence and report observed health, blockers, impact, and next actions without changing workloads or creating cache probes.

Read [targeting and command behavior](references/operations.md), then follow
the [runbook](references/runbook.md). Resolve the installed skill directory
as SKILL_DIR when invoking bundled helpers. This skill is self-contained;
neither the InferOps source checkout nor sibling skills are required.

Finish with observed results, verification performed, and any specific blocker.
Continue ordinary steps already authorized by the user; do not expand scope
to unrelated workloads, destructive cleanup, or external exposure.
