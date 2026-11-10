---
name: inferops-install
description: Install InferOps with system-aware Kubernetes setup and cache sizing. Use for first-time setup on Linux, WSL, a local container cluster, or an existing cluster.
---

# Install InferOps

Inspect the host and target nodes, resolve the latest stable InferOps tag and
matching source commit, create or reuse Kubernetes, calculate a measured cache
budget, install that version directly, and verify images and readiness. An
explicitly requested version takes precedence.

Read [targeting and command behavior](references/operations.md), then follow
the [runbook](references/runbook.md). Resolve the installed skill directory
as SKILL_DIR when invoking bundled helpers. This skill is self-contained;
neither the InferOps source checkout nor sibling skills are required.

Read or execute only the supporting resources needed for the current step:

- [capacity.md](references/capacity.md)
- [releases.md](references/releases.md)
- [inspect_system.py](scripts/inspect_system.py)
- [resolve_release.py](scripts/resolve_release.py)

Finish with observed results, verification performed, and any specific blocker.
Continue ordinary steps already authorized by the user; do not expand scope
to unrelated workloads, destructive cleanup, or external exposure.
