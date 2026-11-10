---
name: inferops-status-fix
description: Diagnose InferOps blockers, apply scoped repairs, and verify recovery. Use when the user asks to fix an installation, workload, or cluster issue.
---

# Diagnose and fix InferOps

Inspect the current state, repair proven causes within the user request, and verify recovery without treating diagnostics as permission for unrelated destructive changes.

Read [targeting and command behavior](references/operations.md), then follow
the [runbook](references/runbook.md). Resolve the installed skill directory
as SKILL_DIR when invoking bundled helpers. This skill is self-contained;
neither the InferOps source checkout nor sibling skills are required.

Read or execute only the supporting resources needed for the current step:

- [status.md](references/status.md)

Finish with observed results, verification performed, and any specific blocker.
Continue ordinary steps already authorized by the user; do not expand scope
to unrelated workloads, destructive cleanup, or external exposure.
