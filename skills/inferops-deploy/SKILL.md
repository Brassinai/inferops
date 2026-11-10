---
name: inferops-deploy
description: Assess hardware fit, select or validate a Hugging Face model, then deploy, activate, and test it. Use for new model deployment or model-fit assessment.
---

# Deploy an InferOps model

Measure target capacity and model requirements, generate a supported configuration, prepare the cache, activate without implicit preemption, and verify real inference.

Read [targeting and command behavior](references/operations.md), then follow
the [runbook](references/runbook.md). Resolve the installed skill directory
as SKILL_DIR when invoking bundled helpers. This skill is self-contained;
neither the InferOps source checkout nor sibling skills are required.

Read or execute only the supporting resources needed for the current step:

- [capacity.md](references/capacity.md)
- [model_plan.py](scripts/model_plan.py)
- [create_hf_secret.py](scripts/create_hf_secret.py)
- [smoke_inference.py](scripts/smoke_inference.py)

Finish with observed results, verification performed, and any specific blocker.
Continue ordinary steps already authorized by the user; do not expand scope
to unrelated workloads, destructive cleanup, or external exposure.
