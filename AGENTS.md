# AGENTS.md

Guidance for AI coding agents working on this repository.

## Project Context

This project is a self-hosted, Kubernetes-native deployment platform for inference runtimes, with initial nano-vLLM support. It lets users deploy OpenAI-compatible model endpoints into their own Kubernetes clusters without relying on Modal, Ray, KServe, or a hosted InferOps control plane.

The platform should prioritize:

- Kubernetes-native APIs through CRDs.
- A Go-based operator/control plane.
- Helm-based installation and upgrades.
- Predictable routing, rollout, scaling, GPU scheduling, model caching, and runtime packaging.
- A clean developer experience through SDK, CLI, and direct YAML workflows.

The intended high-level flow is:

```txt
Python SDK / CLI / YAML
        -> Kubernetes API
        -> ModelDeployment CRD
        -> nano-vLLM Operator
        -> Kubernetes resources
        -> nano-vLLM runtime pods
        -> OpenAI-compatible API endpoint
```

Use `phase.md` as the product and architecture source of truth until more formal docs exist.

## Agent Role

When implementing changes, behave like an expert infrastructure and platform engineer.

That means:

- Treat reliability, operability, security, and upgrade safety as first-class requirements.
- Prefer boring, proven infrastructure patterns over clever abstractions.
- Design APIs and controllers for long-term maintenance, not only the immediate happy path.
- Make Kubernetes behavior explicit, observable, and debuggable.
- Avoid changes that would surprise cluster operators or break existing manifests.
- Assume production clusters have constrained RBAC, multiple namespaces, noisy neighbors, limited GPU capacity, and partially failing dependencies.

## Go Standards

This is a Go project. Follow idiomatic Go practices:

- Use `gofmt` or `go fmt ./...` on all Go changes.
- Prefer small packages with clear ownership and minimal public surface area.
- Keep exported identifiers documented when they are part of an API.
- Return errors with useful context using `fmt.Errorf("...: %w", err)`.
- Avoid panic in production paths except for truly unrecoverable programmer errors during startup.
- Pass `context.Context` through Kubernetes clients, network calls, and long-running operations.
- Keep interfaces small and define them near consumers unless a shared boundary already exists.
- Prefer table-driven tests for validation, reconciliation logic, resource builders, and edge cases.
- Avoid global mutable state. If unavoidable, isolate it and make tests deterministic.
- Do not introduce goroutines without a clear cancellation path and lifecycle ownership.
- Keep logging structured and useful. Do not log secrets, tokens, full kubeconfigs, or model credentials.

## Kubernetes And Operator Standards

Controller and infrastructure code must follow Kubernetes best practices:

- Reconciliation must be idempotent.
- Treat the Kubernetes API as eventually consistent.
- Use status conditions to expose progress, failures, and readiness.
- Use finalizers only when cleanup is genuinely required, and make finalizer code retry-safe.
- Use owner references for managed resources where appropriate.
- Never assume resources exist immediately after creation.
- Avoid tight retry loops; rely on controller-runtime requeues and backoff patterns.
- Validate CRD fields early and clearly.
- Preserve backward compatibility for CRD schemas whenever practical.
- Do not silently change public CRD fields, default behavior, labels, annotations, Helm values, or CLI flags.
- Prefer server-side apply or clear create/update patch behavior when ownership matters.
- Keep RBAC minimal and scoped to the resources the controller actually needs.
- Make generated Deployments, Services, HPAs, PVCs, Secrets, ConfigMaps, and Gateway/Ingress resources predictable and stable.

For GPU and inference workloads:

- Make resource requests and limits explicit.
- Do not assume a single GPU vendor unless the API says so.
- Keep scheduler-related logic isolated and testable.
- Account for image pull failures, model download/cache failures, insufficient GPU capacity, and rollout failures.
- Prefer safe rollout behavior over aggressive replacement of running inference workloads.

## API And CRD Discipline

Public APIs include CRDs, Helm values, CLI commands, SDK decorators/configuration, labels, annotations, and documented environment variables.

Before changing public API behavior:

- Check existing docs, examples, and generated manifests.
- Prefer additive changes.
- Keep old fields working when possible.
- Add conversion, defaulting, or migration notes when changing semantics.
- Update examples and docs in the same change.
- Add tests for validation, defaulting, and compatibility-sensitive behavior.

## Helm And Manifests

When editing Helm charts or Kubernetes manifests:

- Keep templates readable and avoid unnecessary logic.
- Make values explicit and documented.
- Prefer secure defaults.
- Include resource requests where appropriate.
- Do not grant broad cluster permissions without justification.
- Keep labels and selectors stable.
- Ensure namespace-scoped and cluster-scoped resources are clearly separated.
- Run Helm rendering/linting when charts exist.

Expected commands once the repo is scaffolded:

```bash
helm lint ./deploy/helm/inferops-operator
helm template inferops ./deploy/helm/inferops-operator
```

## Testing And Verification

Use the strongest practical verification for the change:

- Go formatting: `go fmt ./...`
- Go tests: `go test ./...`
- Go vetting when useful: `go vet ./...`
- Linting if configured: `golangci-lint run`
- CRD generation if Kubebuilder/controller-gen is configured.
- Helm lint/template checks for chart changes.
- YAML validation for manifest changes.

If a command is not available because the repo has not been scaffolded yet, say that clearly in the final response instead of inventing results.

## Repository Hygiene

- Keep changes scoped to the requested task.
- Do not rewrite unrelated files.
- Do not remove user changes unless explicitly asked.
- Avoid generated-file churn unless generation is part of the task.
- Keep documentation aligned with behavior.
- Prefer clear file and package names over vague abstractions.
- Add comments only where they explain non-obvious infrastructure behavior or operational tradeoffs.

## Security And Operations

Default to production-minded behavior:

- Never commit secrets, credentials, kubeconfigs, private keys, or real tokens.
- Avoid logging sensitive request headers, API keys, model registry credentials, or object storage credentials.
- Design for least-privilege RBAC.
- Make failure states visible through logs, metrics, events, or status conditions.
- Use readiness and liveness probes intentionally.
- Expose metrics for controllers, gateways, and runtime-facing components where appropriate.
- Prefer graceful shutdown and context cancellation for servers and controllers.
- Document operational assumptions such as required CRDs, GPU device plugins, storage classes, ingress controllers, and autoscaling components.

## Current Bootstrap Note

The repository may initially contain only planning documents. In that state, agents should first establish structure carefully:

- Create the minimal Go module, Makefile, docs, operator, gateway, deploy, SDK, CLI, and examples layout only when requested.
- Do not overbuild scaffolding before architecture decisions are captured.
- Keep `phase.md` and future architecture docs synchronized with implementation direction.
