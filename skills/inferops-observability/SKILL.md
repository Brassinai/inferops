---
name: inferops-observability
description: Set up InferOps monitoring and guide Grafana access and metric inspection. Use when enabling observability or finding model and platform metrics.
---

# Set up InferOps observability

Discover or install monitoring, enable InferOps telemetry, verify scrape targets, provide secure login instructions, and walk the user through actual available metrics.

Read [targeting and command behavior](references/operations.md), then follow
the [runbook](references/runbook.md). Resolve the installed skill directory
as SKILL_DIR when invoking bundled helpers. This skill is self-contained;
neither the InferOps source checkout nor sibling skills are required.

Finish with observed results, verification performed, and any specific blocker.
Continue ordinary steps already authorized by the user; do not expand scope
to unrelated workloads, destructive cleanup, or external exposure.
