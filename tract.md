# InferOps Delivery Track

This document converts `phase.md` into an ordered issue backlog for three people
working with coding agents over one month.

## Delivery Decision

The MVP includes the complete roadmap represented in this file. Delivery is
split into two ordered gates:

1. **Core platform gate:** a usable homelab platform that installs into a user's
   Kubernetes cluster, caches a nano-vLLM model, activates it on an exclusive
   GPU, exposes it through a stable gateway, and supports safe deactivation or
   replacement.
2. **Final MVP expansion gate:** advanced scaling, rollouts, cache sources,
   multi-runtime support, observability, tenancy, hardening, dashboard, and
   broader cluster support.

The core platform must land first because the final expansion depends on it.
All final expansion issues remain required before the MVP is considered fully
complete.

## Working Model

Use three parallel lanes. Owners may swap issues, but each lane should have one
person responsible for integration quality.

| Lane | Primary ownership | Suggested owner |
| --- | --- | --- |
| A: Control Plane | CRDs, operator, scheduler, cache, Kubernetes resources | Person 1 |
| B: Runtime and Gateway | nano-vLLM packaging, proxy, routing, auth, metrics | Person 2 |
| C: Developer Experience | Python SDK, CLI, install, doctor, docs, examples | Person 3 |

Every issue should:

- Be developed on a short-lived branch and merged behind stable interfaces.
- Include focused tests and documentation for public behavior.
- Avoid changing an agreed CRD field, CLI flag, label, or runtime contract
  without agreement from all three owners.
- Leave a runnable fixture, fake, or sample manifest when another lane depends
  on behavior that is not implemented yet.
- Prefer several small pull requests over one week-long pull request.

Coding agents should be assigned narrowly scoped issues. Agents must read
`AGENTS.md`, run the relevant verification commands, and avoid unrelated
refactors.

## Labels

- Priority: `P0`, `P1`, `P2`
- Lane: `lane/control-plane`, `lane/runtime-gateway`, `lane/dx`, `lane/shared`
- Type: `type/design`, `type/feature`, `type/test`, `type/docs`, `type/release`
- Parallelism: `parallel-ready`, `integration`
- Size: `S` is up to half a day, `M` is about one day, `L` is about two days

No month-one issue should be larger than `L`. Split an issue if it grows beyond
two focused engineering days.

## June 10-30, 2026 Plan

There are 15 weekdays from Wednesday, June 10 through Tuesday, June 30. This is
an aggressive schedule, even with coding agents, so issues on the critical path
take precedence over all `P1` work.

| Dates | Goal | Exit gate |
| --- | --- | --- |
| June 10-12 | Freeze contracts and make each lane independently runnable | CRDs render, gateway proxies test upstreams, SDK generates matching YAML |
| June 15-19 | Implement and integrate the happy path end to end | A cached model can be activated and reached through the gateway |
| June 22-25 | Add lifecycle failure handling and operational workflows | Deactivate and replace work; failures appear in status and CLI |
| June 26-30 | Pass all acceptance gates and publish the full MVP | Core platform and final MVP expansion integrate successfully |

Reserve June 26-30 for integration and release defects. Start final MVP
expansion issues as soon as their specific dependencies are stable, but do not
reduce lifecycle safety, status visibility, or acceptance testing to create
room.

## Owner Assignment

This is the starting assignment, not a permanent ownership boundary. It matches
technical strength, hardware access, and project-lead responsibilities. Pull
the next unblocked issue from another lane when a lane is waiting.

| Person | Role | Primary issue sequence |
| --- | --- | --- |
| @emmanuelalo52 | Person 1: Control Plane | MVP-101 through MVP-109, MVP-401 |
| @opeyemibami | Person 2: Runtime, Gateway, and GPU Validation | MVP-201 through MVP-205, MVP-403, MVP-404 |
| @steveoni | Person 3: Project Lead and Developer Experience | MVP-001, MVP-002, MVP-301 through MVP-307, MVP-402, MVP-405 |

Use agents heavily for tests, fixtures, generated manifests, documentation
cross-checks, and narrowly bounded implementation tasks. Human owners remain
responsible for contract decisions, security review, and integration behavior.
Pull MVP-501 through MVP-510 forward as soon as their dependencies clear, then
redistribute all three people to finish MVP-511.

GPU-dependent work is not owned exclusively by the person with GPU hardware.
@opeyemibami runs real-GPU validation and records reproducible results. The
assigned issue owner then continues from those results and updates the
codebase. @steveoni owns integration and release gates, so the project lead can
continue implementation where lane work meets.

The team currently has access to one real GPU, so MVP-404 cannot prove
multi-GPU hardware behavior. @opeyemibami owns the issue and runs single-GPU
failure drills; @emmanuelalo52 supports simulated multi-GPU scheduler tests.
Any real multi-GPU acceptance case that cannot be run must remain documented as
a release risk until suitable hardware is available.

## Critical Paths

```txt
Control plane: MVP-001 -> MVP-101 -> MVP-105 -> MVP-106 -> MVP-107 -> MVP-401
Runtime/gateway: MVP-001 -> MVP-202 -> MVP-203 -> MVP-205 -> MVP-402
Developer experience: MVP-001 -> MVP-301 -> MVP-303 -> MVP-305 -> MVP-402

MVP-201 runtime image + MVP-304 install + MVP-401 + MVP-402
    -> MVP-403 homelab acceptance
    -> MVP-404 capacity and failure drills
    -> MVP-405 core-platform gate

Earliest stable core dependencies
    -> MVP-501 through MVP-510 final expansion in parallel

MVP-405 + MVP-501 through MVP-510
    -> MVP-511 full MVP release
```

`MVP-001` is intentionally short. Once its fixtures are merged, all three lanes
can proceed against the agreed contracts without waiting for full
implementations.

---

# June 10-12: Contracts and Independent Foundations

## MVP-001: Freeze MVP contracts and checked-in fixtures

**Priority:** P0  
**Lane:** Shared  
**Assignee:** @steveoni  
**Size:** M  
**Labels:** `type/design`, `parallel-ready`  
**Depends on:** None

Agree and document the minimum contracts shared by the operator, gateway, SDK,
and CLI.

### Scope

- Freeze the month-one `ModelDeployment`, `ModelRuntime`, and `ModelCache`
  shapes, including status phases and conditions.
- Define the stable runtime Service naming and gateway route convention.
- Define the nano-vLLM container environment, ports, readiness, and drain
  behavior.
- Add checked-in example objects for inactive, waiting, active, and failed
  states.
- Record explicit non-goals: GPU slicing, hosted control plane, automatic
  eviction, advanced autoscaling, and dashboard.

### Acceptance criteria

- `docs/architecture.md` and `docs/crds.md` describe the agreed contracts.
- Example YAML can be consumed by all three lanes without live dependencies.
- Contract changes after merge require review from all lane owners.

---

## MVP-002: Establish repository verification and CI

**Priority:** P0  
**Lane:** Shared  
**Assignee:** @steveoni  
**Size:** M  
**Labels:** `type/test`, `parallel-ready`  
**Depends on:** None

### Scope

- Make `make verify` reliable from a clean checkout.
- Add CI for Go formatting, Go tests, Go vet, Helm lint/template, and Python
  syntax/tests.
- Add YAML or CRD schema validation where practical.
- Document required local tool versions.

### Acceptance criteria

- A pull request cannot merge when required verification fails.
- The command set in `AGENTS.md` and `README.md` matches CI.
- CI does not require a GPU or live Kubernetes cluster.

---

## MVP-101: Implement v1alpha1 API types and CRD schemas

**Priority:** P0  
**Lane:** Control Plane  
**Assignee:** @emmanuelalo52  
**Size:** L  
**Labels:** `type/feature`, `parallel-ready`  
**Depends on:** MVP-001

### Scope

- Implement typed specs and statuses for `ModelDeployment`, `ModelRuntime`, and
  `ModelCache`.
- Include activation state, `whenFull`, priority, drain timeout, runtime,
  resources, routing, cache, and secret references.
- Include observed phase, endpoint, assigned node, replicas, cache state, and
  conditions.
- Add OpenAPI validation, enums, defaults where safe, and generated CRDs.

### Acceptance criteria

- Valid examples apply to a test API server.
- Invalid activation policies, GPU counts, and scaling bounds are rejected.
- Status and spec remain clearly separated.
- Table-driven API validation tests pass.

---

## MVP-102: Implement deterministic managed-resource builders

**Priority:** P0  
**Lane:** Control Plane  
**Assignee:** @emmanuelalo52  
**Size:** L  
**Labels:** `type/feature`, `parallel-ready`  
**Depends on:** MVP-001

### Scope

- Build runtime Deployment, Service, ConfigMap, cache downloader Job, and
  node-local cache volume/affinity objects.
- Add stable labels, selectors, owner references, resource requests/limits,
  probes, and secret references.
- Request whole GPUs from the configured vendor resource.
- Keep inactive deployments free of runtime GPU pods.

### Acceptance criteria

- Builders are deterministic and table-tested.
- Runtime pods request equal GPU requests and limits.
- Runtime pods are constrained to the node containing the ready cache.
- Sensitive values are referenced from Secrets and are not copied into logs.

---

## MVP-201: Package a Kubernetes-ready nano-vLLM runtime

**Priority:** P0  
**Lane:** Runtime and Gateway  
**Assignee:** @opeyemibami  
**Size:** L  
**Labels:** `type/feature`, `parallel-ready`  
**Depends on:** MVP-001

### Scope

- Add the runtime image/build definition and entrypoint.
- Load models from the prepared mounted cache path.
- Translate the shared runtime environment to the upstream engine server CLI.
- Require the upstream engine image to own OpenAI endpoints, health,
  readiness, metrics, and native signal handling.
- Document the engine-image build boundary and upstream nano-vLLM assumptions.

### Acceptance criteria

- Entrypoint contract tests validate argument translation without embedding a
  second server or fake inference engine in InferOps.
- Release validation starts the upstream image with a lightweight test model
  and checks readiness, OpenAI endpoints, metrics, streaming, and shutdown.
- Runtime pods use only the prepared local cache path and do not download model
  weights.

---

## MVP-202: Implement the gateway proxy core with a fake registry

**Priority:** P0  
**Lane:** Runtime and Gateway  
**Assignee:** @opeyemibami  
**Size:** L  
**Labels:** `type/feature`, `parallel-ready`  
**Depends on:** MVP-001

### Scope

- Implement model-path parsing and OpenAI-compatible reverse proxying.
- Preserve streaming and cancellation.
- Route using an in-memory/fake model registry initially.
- Return explicit inactive, activating, unavailable, and unknown-model errors.
- Add health and readiness endpoints.

### Acceptance criteria

- Proxy tests cover normal, streaming, cancelled, and upstream failure cases.
- The fake registry allows gateway development without the operator.
- Unknown or inactive models never reach an unrelated runtime.

---

## MVP-301: Complete the Python SDK surface, endpoint semantics, and YAML generation

**Priority:** P0  
**Lane:** Developer Experience  
**Assignee:** @steveoni  
**Size:** L  
**Labels:** `type/feature`, `parallel-ready`  
**Depends on:** MVP-001

### Scope

- Implement `App`, `@app.model`, and endpoint metadata needed for the MVP.
- Define two first-class SDK lanes:
  - Model lane: the built-in inference API where InferOps manages routing,
    rollout, scaling, auth, and streaming passthrough.
  - Custom endpoint lane: arbitrary app logic around a model such as
    preprocessing, RAG, tools, guardrails, custom JSON routes, and SSE.
- Support the model lane through both a Python client API and the stable
  OpenAI-compatible HTTP API.
- Make custom endpoint semantics explicit:
  - `return ...` means a normal JSON or HTTP response.
  - `yield ...` or returning an async iterator means a streaming response.
  - `self.generate(...)` calls the selected runtime through the runtime
    abstraction rather than a specific engine implementation.
  - `self.generate_stream(...)` yields runtime-agnostic token or event chunks.
- Validate model, runtime, GPU, activation, scaling, routing, and cache inputs.
- Generate a `ModelDeployment` matching the frozen CRD contract.
- Keep generated YAML deterministic for GitOps diffs.

### SDK shape example

Model lane:

```python
app = inferops.App("customer-support")

@app.model(name="qwen-chat", model="Qwen/Qwen2.5-7B-Instruct")
class QwenChat:
    pass
```

Users call the built-in inference API through either:

```python
client = inferops.Client(base_url="https://api.example.com", api_key="...")

resp = client.responses.create(
    model="qwen-chat",
    input="Explain Kubernetes Services simply.",
)

stream = client.responses.stream(
    model="qwen-chat",
    input="Write a short poem.",
)
```

or the stable OpenAI-compatible HTTP API with streaming enabled through
`stream=True`.

Custom endpoint lane:

```python
app = inferops.App("customer-support")

@app.model(name="qwen-chat", model="Qwen/Qwen2.5-7B-Instruct")
class QwenChat:
    @inferops.web_endpoint(method="POST", path="/chat")
    async def chat(self, request):
        prompt = request["prompt"]
        return await self.generate(prompt)

    @inferops.web_endpoint(method="POST", path="/chat/stream")
    async def stream_chat(self, request):
        async for chunk in self.generate_stream(request["prompt"]):
            yield chunk
```

### Acceptance criteria

- `inferops generate app.py` emits valid, deterministic YAML.
- SDK validation fails early with actionable messages.
- Generated fixtures match the CRD examples from MVP-001.
- Python tests cover defaults, invalid configurations, model-lane client usage,
  and custom endpoint response versus streaming semantics.

---

## MVP-302: Build the CLI command structure and Kubernetes client boundary

**Priority:** P0  
**Lane:** Developer Experience  
**Assignee:** @steveoni  
**Size:** L  
**Labels:** `type/feature`, `parallel-ready`  
**Depends on:** MVP-001

### Scope

- Add command groups and shared options for namespace, context, kubeconfig, and
  output.
- Define a small Kubernetes client boundary that can be faked in tests.
- Implement consistent errors and exit codes.
- Add placeholders wired to fakes for deploy, activate, deactivate, status,
  logs, GPU, cache, and install workflows.

### Acceptance criteria

- Every MVP command has help text and a testable handler.
- Commands can be tested without a live cluster.
- The CLI never logs kubeconfig contents, tokens, or Secret data.

---

# June 15-19: End-to-End Happy Path

## MVP-103: Implement validation and runtime resolution

**Priority:** P0  
**Lane:** Control Plane  
**Assignee:** @emmanuelalo52  
**Size:** M  
**Labels:** `type/feature`, `parallel-ready`  
**Depends on:** MVP-101

### Scope

- Validate compatibility rules not expressible in CRD OpenAPI.
- Resolve `ModelRuntime` defaults.
- Validate tensor parallel size against requested GPU count.
- Surface missing runtime, secret reference, or unsupported configuration as
  status conditions and Events.

### Acceptance criteria

- Invalid resources do not create workloads.
- Failures have stable reason codes and useful messages.
- Table-driven tests cover compatibility and missing dependency cases.

---

## MVP-104: Implement node-local ModelCache reconciliation

**Priority:** P0  
**Lane:** Control Plane  
**Assignee:** @emmanuelalo52  
**Size:** L  
**Labels:** `type/feature`, `parallel-ready`  
**Depends on:** MVP-101, MVP-102

### Scope

- Select a compatible GPU node with sufficient configured cache capacity.
- Create a retry-safe downloader Job for Hugging Face models.
- Track revision, node, path, size, readiness, last-used time, and failures.
- Preserve cache when a runtime is deactivated.
- Make cache deletion a separate explicit operation.

### Acceptance criteria

- Reconciliation is idempotent.
- A completed cache is not downloaded again on activation.
- Failed downloads are visible and retry safely.
- Cache paths are predictable and cannot escape the configured root.

---

## MVP-105: Reconcile declared deployments and stable Services

**Priority:** P0  
**Lane:** Control Plane  
**Assignee:** @emmanuelalo52  
**Size:** L  
**Labels:** `type/feature`, `integration`  
**Depends on:** MVP-102, MVP-103, MVP-104

### Scope

- Reconcile `ModelDeployment` into cache state and a stable Service identity.
- Do not create a runtime Deployment while desired state is inactive.
- Create/update managed resources idempotently.
- Watch owned resources and reflect progress in status.

### Acceptance criteria

- Applying the same object repeatedly causes no resource churn.
- An inactive deployment can become `Cached` without occupying a GPU.
- Service naming and selectors match the gateway contract.
- Deleting a deployment cleans up owned resources without deleting shared data
  unexpectedly.

---

## MVP-203: Add gateway Kubernetes-backed model discovery

**Priority:** P0  
**Lane:** Runtime and Gateway  
**Assignee:** @opeyemibami  
**Size:** L  
**Labels:** `type/feature`, `integration`  
**Depends on:** MVP-001, MVP-202

### Scope

- Watch or query `ModelDeployment` and runtime Service state.
- Route only to ready, active model backends.
- Keep the external model path stable while backend pods change.
- React to activation, deactivation, and drain state.

### Acceptance criteria

- Registry updates are eventually consistent and retry safely.
- The gateway stops sending new requests to a draining backend.
- Tests use fake Kubernetes objects and do not require the operator.

---

## MVP-204: Add basic gateway authentication and metrics

**Priority:** P0  
**Lane:** Runtime and Gateway  
**Assignee:** @opeyemibami  
**Size:** M  
**Labels:** `type/feature`, `parallel-ready`  
**Depends on:** MVP-202

### Scope

- Validate bearer tokens from referenced Kubernetes Secrets.
- Add request count, latency, active request, and upstream error metrics.
- Avoid logging authorization headers or request bodies by default.

### Acceptance criteria

- Missing or invalid credentials are rejected when auth is configured.
- Secrets are reloadable without gateway restart where practical.
- Metrics distinguish model and response class without high-cardinality labels.

---

## MVP-303: Implement idempotent deploy and generate workflows

**Priority:** P0  
**Lane:** Developer Experience  
**Assignee:** @steveoni  
**Size:** L  
**Labels:** `type/feature`, `integration`  
**Depends on:** MVP-301, MVP-302

### Scope

- Implement `inferops generate app.py`.
- Implement `inferops deploy app.py` using the user's kubeconfig.
- Store non-secret state and last-applied spec hash under `.inferops/`.
- Patch existing resources rather than creating duplicates.
- Add `--activate` without changing default inactive behavior.

### Acceptance criteria

- Repeated deploy with no changes is a no-op.
- Default deploy never activates or replaces another model.
- Generated YAML and applied objects are equivalent.
- Local state contains no credentials or Secret values.

---

## MVP-304: Implement install profile and Helm chart integration

**Priority:** P0  
**Lane:** Developer Experience  
**Assignee:** @steveoni  
**Size:** L  
**Labels:** `type/feature`, `integration`  
**Depends on:** MVP-101, MVP-302

### Scope

- Complete operator and gateway Helm charts with minimal RBAC.
- Add CRDs, default `ModelRuntime`, probes, resources, and configurable images.
- Implement `inferops install --profile homelab`.
- Support configurable cache root and optional Tailscale exposure values.

### Acceptance criteria

- `helm lint` and `helm template` pass.
- Installation is repeatable and upgrades do not recreate stable resources
  unnecessarily.
- RBAC includes only resources actually used by the MVP.
- Installation does not guess host NVIDIA driver or package commands.

---

# June 22-25: Lifecycle, Failure Handling, and Operations

## MVP-106: Implement exclusive GPU-slot scheduling and activation

**Priority:** P0  
**Lane:** Control Plane  
**Assignee:** @emmanuelalo52  
**Size:** L  
**Labels:** `type/feature`, `integration`  
**Depends on:** MVP-104, MVP-105

### Scope

- Observe allocatable GPU resources and active InferOps workloads.
- Select compatible capacity, preferring a node with a ready cache.
- Implement `Queue` and `Reject` behavior when no slot is available.
- Create the runtime workload only after activation is permitted.
- Record assigned node and observed runtime readiness.

### Acceptance criteria

- The operator never creates an unbounded set of unschedulable GPU pods.
- `Queue` produces `WaitingForGPU`; `Reject` produces a clear failure condition.
- Kubernetes remains responsible for final physical GPU assignment.
- Scheduler decisions are isolated and table-tested.

---

## MVP-107: Implement deactivation, drain, and cache preservation

**Priority:** P0  
**Lane:** Control Plane  
**Assignee:** @emmanuelalo52  
**Size:** L  
**Labels:** `type/feature`, `integration`  
**Depends on:** MVP-106, MVP-203

### Scope

- Move active models through draining and deactivating phases.
- Coordinate gateway drain state before stopping the runtime.
- Enforce a bounded drain timeout.
- Remove the runtime workload and preserve the cache and stable Service.

### Acceptance criteria

- No new requests are sent after drain begins.
- In-flight requests get the configured grace period.
- The GPU slot is released after deactivation.
- Re-activation reuses the existing model cache.

---

## MVP-108: Implement explicit single-GPU replacement with rollback attempt

**Priority:** P0  
**Lane:** Control Plane  
**Assignee:** @emmanuelalo52  
**Size:** L  
**Labels:** `type/feature`, `integration`  
**Depends on:** MVP-106, MVP-107

### Scope

- Implement opt-in `ReplaceOldest` and `ReplaceLowestPriority`.
- Prepare the replacement cache before draining the current model.
- Activate the replacement and switch traffic only after readiness.
- Attempt to restore the previous model when replacement activation fails.

### Acceptance criteria

- Replacement never occurs under `Queue` or `Reject`.
- Failures and rollback outcomes are visible in status and Events.
- Tests cover successful replacement, failed activation, and failed rollback.
- Documentation states that single-GPU switching has unavoidable downtime.

---

## MVP-109: Complete status conditions, Events, and controller metrics

**Priority:** P1  
**Lane:** Control Plane  
**Assignee:** @emmanuelalo52  
**Size:** M  
**Labels:** `type/feature`, `parallel-ready`  
**Depends on:** MVP-103, MVP-105

### Scope

- Implement consistent phases and conditions across reconcilers.
- Emit useful Kubernetes Events for major lifecycle transitions and failures.
- Add controller metrics for GPU slots, queue depth, activation duration, cache
  download duration, and failures.

### Acceptance criteria

- Status writes avoid unnecessary reconcile loops.
- Conditions use stable types and reasons.
- Metrics avoid model-repository or object-UID cardinality explosions.

---

## MVP-205: Complete gateway drain and cold-start responses

**Priority:** P0  
**Lane:** Runtime and Gateway  
**Assignee:** @opeyemibami  
**Size:** M  
**Labels:** `type/feature`, `integration`  
**Depends on:** MVP-203, MVP-107

### Scope

- Track active requests per backend.
- Expose drain completion to the operator.
- Return explicit, documented responses for cached, waiting, activating,
  draining, and failed models.
- Preserve streaming behavior during lifecycle changes.

### Acceptance criteria

- Draining backends receive no new requests.
- The operator can determine when drain is complete or timed out.
- Lifecycle response behavior is documented and tested.

---

## MVP-305: Implement activation and operational CLI commands

**Priority:** P0  
**Lane:** Developer Experience  
**Assignee:** @steveoni  
**Size:** L  
**Labels:** `type/feature`, `integration`  
**Depends on:** MVP-302, MVP-106, MVP-107

### Scope

- Implement `activate`, `deactivate`, `status`, `logs`, `models`, `endpoints`,
  and `delete`.
- Support explicit `--when-full` replacement selection.
- Watch status transitions and print actionable progress.
- Make delete behavior clear about cache preservation.

### Acceptance criteria

- Commands operate through Kubernetes APIs and work with the fake client.
- `activate` reports waiting, rejection, active, and failure states.
- Replacement requires an explicit policy.
- Output never exposes Secret values.

---

## MVP-306: Implement doctor, GPU, and cache CLI commands

**Priority:** P0  
**Lane:** Developer Experience  
**Assignee:** @steveoni  
**Size:** L  
**Labels:** `type/feature`, `parallel-ready`  
**Depends on:** MVP-302, MVP-304

### Scope

- Implement `inferops doctor`.
- Check Kubernetes API access, device plugin health, allocatable and occupied GPU
  slots, cache path/free space, RuntimeClass, gateway readiness, and optional
  Tailscale configuration.
- Implement `gpu list`, `cache list`, and explicit `cache delete`.

### Acceptance criteria

- Each check passes, warns, or fails independently with remediation guidance.
- Doctor does not hide or auto-fix host prerequisite failures.
- Cache deletion refuses to remove an in-use cache unless explicitly forced.

---

## MVP-307: Write homelab, CLI, SDK, and CRD documentation

**Priority:** P0  
**Lane:** Developer Experience  
**Assignee:** @steveoni  
**Size:** L  
**Labels:** `type/docs`, `parallel-ready`  
**Depends on:** MVP-001

### Scope

- Complete quickstart and homelab documentation.
- Document SDK, CLI, raw YAML, activation policies, cache semantics, routing,
  security assumptions, and failure modes.
- Link to current official k3s NVIDIA runtime, NVIDIA Container Toolkit, NVIDIA
  device plugin, and Tailscale Kubernetes Operator documentation.
- Update examples for the frozen MVP API.

### Acceptance criteria

- A new user can follow either SDK/CLI or raw YAML workflow.
- Docs never guess distribution-specific NVIDIA driver installation commands.
- Examples are validated in CI where practical.

---

# June 26-30: Integration, Full MVP Validation, and Release

## MVP-401: Add control-plane integration tests

**Priority:** P0  
**Lane:** Shared, led by Control Plane  
**Assignee:** @emmanuelalo52  
**Size:** L  
**Labels:** `type/test`, `integration`  
**Depends on:** MVP-106, MVP-107, MVP-108, MVP-109

### Scope

- Test CRDs and controllers with envtest or an equivalent local API server.
- Cover inactive-to-cached, activation, waiting, reject, deactivate,
  replacement, failed cache, and failed runtime readiness.
- Verify idempotence and status transitions.

### Acceptance criteria

- Tests run without a GPU.
- Tests assert managed-resource shape and lifecycle status.
- Common partial-failure cases are deterministic.

---

## MVP-402: Add gateway and CLI integration tests

**Priority:** P0  
**Lane:** Shared, led by Runtime/Gateway and DX  
**Assignee:** @steveoni  
**Size:** L  
**Labels:** `type/test`, `integration`  
**Depends on:** MVP-204, MVP-205, MVP-303, MVP-305, MVP-306

### Scope

- Run gateway against fake runtime backends and fake Kubernetes state.
- Exercise CLI deploy, activate, status, deactivate, and replacement workflows.
- Cover auth, streaming, drain, cold-start responses, and failures.

### Acceptance criteria

- Tests run in CI without a GPU.
- Streaming and cancellation regressions are detected.
- CLI output and exit codes are asserted for key workflows.

---

## MVP-403: Run the single-node GPU homelab acceptance test

**Priority:** P0  
**Lane:** Shared  
**Assignee:** @opeyemibami  
**Size:** L  
**Labels:** `type/test`, `integration`  
**Depends on:** MVP-201, MVP-304, MVP-401, MVP-402

### Scope

- Start from the documented host prerequisites.
- Install with `inferops install --profile homelab`.
- Run `doctor`, `gpu list`, deploy, cache, activate, inference, deactivate,
  re-activate, and replacement.
- Record timings and all defects found.

### Acceptance criteria

- A model serves a streaming OpenAI-compatible request through the gateway.
- Deactivation releases the GPU and preserves the cache.
- Re-activation does not download the model again.
- Explicit replacement works or rolls back with visible status.
- The quickstart is corrected based on the test.

---

## MVP-404: Run multi-GPU scheduling acceptance and failure drills

**Priority:** P0  
**Lane:** Shared  
**Assignee:** @opeyemibami  
**Size:** L  
**Labels:** `type/test`, `integration`  
**Depends on:** MVP-403

### Scope

- Validate two simultaneous active models when capacity exists.
- Validate cache-local preference and waiting behavior when full.
- Use simulated multi-GPU capacity when real multi-GPU hardware is unavailable.
- Drill image pull failure, missing Secret, cache download failure, insufficient
  GPU, runtime readiness failure, and replacement rollback.

### Acceptance criteria

- Capacity is never overcommitted by InferOps.
- Each failure appears through status, Events, CLI output, and useful logs.
- Any skipped hardware-only case is documented as release risk.

---

## MVP-405: Core-platform security, upgrade, and readiness gate

**Priority:** P0  
**Lane:** Shared  
**Assignee:** @steveoni  
**Size:** L  
**Labels:** `type/test`, `integration`  
**Depends on:** MVP-002, MVP-307, MVP-403, MVP-404

### Scope

- Review RBAC, Secret handling, logs, network exposure, container permissions,
  probes, resources, graceful shutdown, and Helm upgrade behavior.
- Verify public CRD fields, CLI flags, labels, and Helm values are documented.
- Approve the core platform as the stable base for final MVP expansion.

### Acceptance criteria

- `make verify` passes from a clean checkout.
- Helm install and upgrade are tested.
- No credentials or real tokens exist in the repository or logs.
- Core-platform limitations and known risks are documented.

---

# Integration Rules That Reduce Blocking

1. **Merge contracts before implementations.** MVP-001 supplies examples and
   interfaces so no lane waits for a live implementation.
2. **Use fakes at lane boundaries.** The gateway uses a fake registry, the CLI
   uses a fake Kubernetes client, and controller tests use fake runtime states.
3. **Integrate daily from June 15 onward.** A small broken integration should be
   fixed before new feature work.
4. **Keep one issue owned by one person.** Other people and agents can assist,
   but one person is accountable for merge quality.
5. **Do not expand MVP silently.** New requests must replace an existing issue
   or be explicitly accepted into the MVP by all three owners.
6. **Escalate contract blockers the same day.** The three owners decide public
   contract changes together; implementation details stay within each lane.
7. **Keep the main branch releasable.** Incomplete behavior should be isolated
   behind configuration or omitted until it works.

# Core Platform Gate

The core platform is ready for final MVP expansion only when:

- A user can install InferOps into a prepared single-node k3s GPU homelab.
- `inferops doctor` clearly reports prerequisite and platform health.
- SDK decorators and raw YAML both create valid `ModelDeployment` objects.
- Deploying an inactive model prepares and records a node-local NVMe cache
  without occupying a GPU.
- Activating a model acquires a compatible whole-GPU slot and reaches `Active`.
- The stable gateway serves an authenticated, streaming OpenAI-compatible
  request.
- Deactivation drains traffic, releases the GPU, and preserves the cache.
- Explicit single-GPU replacement succeeds or attempts a visible rollback.
- Multi-GPU scheduling uses free compatible capacity without overcommit.
- Status, Events, logs, metrics, and CLI output expose major failure states.
- CI, Helm checks, integration tests, and the homelab acceptance test pass.

---

# Final MVP Expansion

These issues are part of the MVP and preserve the rest of `phase.md`. They are
placed at the final end because they expand the stable core platform. Their
dependencies point to the earliest core interfaces they need so work can begin
in parallel before the core-platform gate.

Suggested parallel execution as dependencies clear:

| Person | Final expansion sequence |
| --- | --- |
| @emmanuelalo52 | MVP-501, MVP-503, MVP-508 |
| @opeyemibami | MVP-502, MVP-504, MVP-505, MVP-506 |
| @steveoni | MVP-507, MVP-509, MVP-510, MVP-511 |

## MVP-501: Capacity-aware replica scaling and idle scale-to-zero

**Priority:** P1  
**Lane:** Control Plane  
**Assignee:** @emmanuelalo52  
**Size:** L  
**Labels:** `type/feature`, `integration`  
**Depends on:** MVP-106, MVP-109, MVP-205

- Add pending-request and idle-timeout scaling.
- Cap desired replicas by compatible free GPU capacity.
- Keep HPA/KEDA integration optional until it can respect capacity.

## MVP-502: Advanced rollouts with spare-capacity validation

**Priority:** P1  
**Lane:** Shared  
**Assignee:** @opeyemibami  
**Size:** L  
**Labels:** `type/feature`, `integration`  
**Depends on:** MVP-108, MVP-205, MVP-501

- Add rolling, blue-green, and canary strategies.
- Validate spare compatible GPU slots before starting.
- Require explicit fallback to single-GPU replacement.

## MVP-503: Additional cache sources and multi-node cache distribution

**Priority:** P1  
**Lane:** Control Plane  
**Assignee:** @emmanuelalo52  
**Size:** L  
**Labels:** `type/feature`, `parallel-ready`  
**Depends on:** MVP-104

- Add S3, R2, MinIO, PVC, and local PV support.
- Add safe cache copy/pre-pull across GPU nodes.
- Add disk pressure, eviction, checksum, and revision policies.

## MVP-504: Multi-runtime conformance and custom runtime support

**Priority:** P1  
**Lane:** Runtime and Gateway  
**Assignee:** @opeyemibami  
**Size:** L  
**Labels:** `type/feature`, `parallel-ready`  
**Depends on:** MVP-103, MVP-201

- Define runtime conformance tests.
- Validate the abstraction with vLLM and SGLang, plus a custom FastAPI runtime
  where practical.
- Keep protocol, health, metrics, and lifecycle behavior explicit.

## MVP-505: Advanced gateway routing and traffic policy

**Priority:** P1  
**Lane:** Runtime and Gateway  
**Assignee:** @opeyemibami  
**Size:** L  
**Labels:** `type/feature`, `integration`  
**Depends on:** MVP-203, MVP-204, MVP-205, MVP-502

- Add least-loaded routing, rate limiting, request logging policy, and canary
  traffic weights.
- Investigate prefix-cache-aware routing only after reliable metrics exist.

## MVP-506: Prometheus, Grafana, and expanded observability

**Priority:** P1  
**Lane:** Runtime and Gateway  
**Assignee:** @opeyemibami  
**Size:** L  
**Labels:** `type/feature`, `parallel-ready`  
**Depends on:** MVP-109, MVP-204

- Package ServiceMonitors and dashboards.
- Add TTFT, tokens/sec, pending tokens, GPU utilization, model load time, and
  switch time.
- Add OpenTelemetry later without making it an MVP dependency.

## MVP-507: Production security and namespace tenancy

**Priority:** P1  
**Lane:** Shared  
**Assignee:** @steveoni  
**Size:** L  
**Labels:** `type/feature`, `parallel-ready`  
**Depends on:** MVP-204, MVP-304

- Add namespace isolation guidance, NetworkPolicies, quotas, per-team access,
  and audit-friendly behavior.
- Add optional OIDC and multi-tenant gateway design later.

## MVP-508: Production hardening and admission validation

**Priority:** P1  
**Lane:** Control Plane  
**Assignee:** @emmanuelalo52  
**Size:** L  
**Labels:** `type/feature`, `parallel-ready`  
**Depends on:** MVP-101, MVP-105

- Add admission webhooks, PodDisruptionBudgets, topology controls, and richer
  preflight checks.
- Test operator upgrades, CRD compatibility, backup, and disaster recovery.

## MVP-509: Self-hosted dashboard

**Priority:** P2  
**Lane:** Developer Experience  
**Assignee:** @steveoni  
**Size:** L  
**Labels:** `type/feature`, `integration`  
**Depends on:** MVP-505, MVP-506, MVP-507

- Build an in-cluster dashboard and backend for deployments, GPUs, caches,
  endpoints, logs, metrics, activation, scaling, and generated YAML.
- Do not introduce an InferOps-hosted control plane.

## MVP-510: Broader cluster and ingress support

**Priority:** P2  
**Lane:** Developer Experience  
**Assignee:** @steveoni  
**Size:** L  
**Labels:** `type/test`, `parallel-ready`  
**Depends on:** MVP-304, MVP-307

- Validate managed Kubernetes, multi-node clusters, Gateway API, NGINX,
  Traefik, Istio, and cloud load balancers.
- Keep Tailscale as the documented homelab private-access path.

## MVP-511: Full MVP integration, acceptance, and release

**Priority:** P0  
**Lane:** Shared  
**Assignee:** @steveoni  
**Size:** L  
**Labels:** `type/release`, `integration`  
**Depends on:** MVP-405, MVP-501, MVP-502, MVP-503, MVP-504, MVP-505, MVP-506, MVP-507, MVP-508, MVP-509, MVP-510

### Scope

- Run end-to-end acceptance across the core platform and final MVP expansion.
- Verify upgrades from the core-platform gate to the full MVP.
- Resolve integration defects and publish the full MVP release.

### Acceptance criteria

- All MVP issues and required verification pass.
- Advanced features fail safely when their optional cluster dependencies are
  unavailable.
- Upgrade, security, operations, and compatibility limitations are documented.
- Release notes distinguish the supported homelab path from broader validated
  cluster configurations.

# Full MVP Definition of Done

The MVP is complete only when the core platform gate passes, MVP-501 through
MVP-510 meet their acceptance criteria, and MVP-511 publishes the integrated
release.
