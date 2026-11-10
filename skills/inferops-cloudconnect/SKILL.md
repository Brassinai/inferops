---
name: inferops-cloudconnect
description: Connect an InferOps provider cluster to InferCloud with offering benchmarks and scoped connection repair. Use for provider cloud connection setup or recovery.
---

# Connect InferOps to InferCloud

Validate the destination and provider credential, connect using secure input and offering benchmarking, repair scoped blockers, and verify agent, mesh, and offering state.

Read [targeting and command behavior](references/operations.md), then follow
the [runbook](references/runbook.md). Resolve the installed skill directory
as SKILL_DIR when invoking bundled helpers. This skill is self-contained;
neither the InferOps source checkout nor sibling skills are required.

Finish with observed results, verification performed, and any specific blocker.
Continue ordinary steps already authorized by the user; do not expand scope
to unrelated workloads, destructive cleanup, or external exposure.
