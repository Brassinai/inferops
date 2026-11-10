---
name: inferops-switchdeploy
description: Switch from one InferOps model to another with preflight capacity checks and recovery. Use when replacing the currently running model.
---

# Switch InferOps models

Prepare and validate the replacement before downtime, deactivate only the selected current model, activate and test the replacement, and restore the previous model on failure when possible.

Read [targeting and command behavior](references/operations.md), then follow
the [runbook](references/runbook.md). Resolve the installed skill directory
as SKILL_DIR when invoking bundled helpers. This skill is self-contained;
neither the InferOps source checkout nor sibling skills are required.

Read or execute only the supporting resources needed for the current step:

- [deployment.md](references/deployment.md)
- [capacity.md](references/capacity.md)
- [model_plan.py](scripts/model_plan.py)
- [create_hf_secret.py](scripts/create_hf_secret.py)
- [smoke_inference.py](scripts/smoke_inference.py)

Finish with observed results, verification performed, and any specific blocker.
Continue ordinary steps already authorized by the user; do not expand scope
to unrelated workloads, destructive cleanup, or external exposure.
