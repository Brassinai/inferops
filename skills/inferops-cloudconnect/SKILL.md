---
name: inferops-cloudconnect
description: Connect or resume an InferOps provider cluster's InferCloud connection, configure its agent and mesh, and report inferops cloud status. Use for provider connection setup or recovery; extra reachability tests, inference calls and benchmarks require an explicit request.
---

# Connect InferOps to InferCloud

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


Resolve the destination and provider credential, reuse an existing matching
registration, configure its agent/mesh, and report inferops cloud status. Complete
the connection workflow without waiting for offering publication or routability.
An accepted heartbeat and a routable model are different outcomes. Stopping the
CLI does not necessarily stop its installed agent or gateway.

Run only necessary independent preflight reads together and catch deterministic
setup failures early. Use inferops cloud status for routine post-connect checks.
Do not add manual mesh pings/HTTP probes, test from the cloud gateway's network,
inspect recorded reachability evidence, invoke a model, or enable/run benchmarks
unless explicitly requested. A status-only request does not authorize repairs.

Read [targeting and command behavior](references/operations.md), then follow
the [runbook](references/runbook.md). Resolve the installed skill directory
as SKILL_DIR when invoking bundled helpers. This skill is self-contained;
neither the InferOps source checkout nor sibling skills are required.

Finish with the connection result, cloud status summary, and the exact command
the user can run later. Report setup blockers without expanding a connect request
into a model/routing assessment. Read [troubleshooting](references/troubleshooting.md)
only for a failed setup or explicitly requested diagnosis/repair.
Continue ordinary steps already authorized by the user; do not expand scope
to unrelated workloads, destructive cleanup, or external exposure.
