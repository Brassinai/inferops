---
name: inferops-deploy
description: Choose or validate a suitable model, deploy it, activate it, verify inference, and return working model-call examples, including llama.cpp vision requests. Explicit invocation includes completing deployment when asked to find the best model; honor explicit assessment-only requests.
---

# Deploy an InferOps model

## Diagnose and recover within the task

When an authorized operation fails or stalls, investigate the actual cause using
current status, events, and bounded logs. Apply a targeted InferOps repair within
the requested outcome, retry after the relevant state changes, and verify the
result; do not stop at the first error or wait for the user to request debugging.
Preserve unrelated workloads, credentials, data, and explicit user constraints.
If a fix needs missing authorization for a system-wide, destructive, or external
action, explain the concrete action and ask before performing it; continue any
independent authorized work. If recovery is impossible, report what was tried,
the observed blocker, and the next actionable step. Respect commands-only,
assessment-only, and status-only requests.


An explicit invocation is an end-to-end deployment request, including wording
such as "find the best llama.cpp model for this system that supports vision."
Choose a suitable model, generate its supported configuration, prepare the cache,
deploy, activate without implicit preemption, and verify real inference. Do not
stop at a shortlist or ask the user to choose again when selection was delegated.
Only stop at assessment when the user explicitly asks for recommendation only,
comparison only, a plan, or no deployment. Do not turn an ordinary untagged
recommendation question into permission to change a cluster.

Honor required capabilities throughout selection. For llama.cpp vision, select a
matching language-model/projector pair and create a dedicated runtime configuration.
Vision requests require image-bearing inference verification; a text-only test
does not satisfy them.
Continue ordinary configuration and a bounded startup test within measured
resource limits without seeking approval again. Stop for a concrete blocker when
no candidate can safely meet the request, or when progress requires authority
outside the requested deployment, such as stopping unrelated workloads.

Read [targeting and command behavior](references/operations.md), then follow
the [runbook](references/runbook.md). Resolve the installed skill directory
as SKILL_DIR when invoking bundled helpers. This skill is self-contained;
neither the InferOps source checkout nor sibling skills are required.

Read or execute only the supporting resources needed for the current step:

- [capacity.md](references/capacity.md)
- [model_plan.py](scripts/model_plan.py)
- [create_hf_secret.py](scripts/create_hf_secret.py)
- [smoke_inference.py](scripts/smoke_inference.py)

After successful deployment, return the actual gateway URL, model name, and
copyable inference examples in the final answer; include an image request for
vision models. Show authentication via a secure environment variable when needed,
plus how to keep or restart the local forward. A smoke-test helper command alone
is not a model-call example. Follow the runbook's model-call handoff instructions.
For a blocked deployment, report the specific blocker and do not present an
untested endpoint as working.
Continue ordinary steps already authorized by the user; do not expand scope
to unrelated workloads, destructive cleanup, or external exposure.
