---
name: inferops-uninstall
description: Uninstall InferOps with explicit workload shutdown, resource retention, and cleanup scope. Use when removing an InferOps installation from a cluster.
---

# Uninstall InferOps safely

Inventory resources and shared ownership, drain models and account for cloud exposure, remove the requested platform components, and verify removed and retained resources.

Read [targeting and command behavior](references/operations.md), then follow
the [runbook](references/runbook.md). Resolve the installed skill directory
as SKILL_DIR when invoking bundled helpers. This skill is self-contained;
neither the InferOps source checkout nor sibling skills are required.

Finish with observed results, verification performed, and any specific blocker.
Continue ordinary steps already authorized by the user; do not expand scope
to unrelated workloads, destructive cleanup, or external exposure.
