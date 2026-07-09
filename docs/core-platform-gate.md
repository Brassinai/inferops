# Core Platform Gate

This gate records the security, upgrade, and readiness checks required before
using the core platform as the stable base for final MVP expansion.

## Required Verification

From a clean checkout:

```bash
make verify
```

The command covers Go formatting, Go tests, Go vet, Python syntax and unit
tests, Python packaging, Helm linting/rendering, YAML parsing, and Kubernetes
schema validation. If a local tool is missing, run `make setup` and repeat the
gate.

Focused chart checks are also available:

```bash
make helm-lint
make helm-template
make schema-check
```

## Helm Install And Upgrade

For a homelab release candidate:

```bash
kubectl apply --server-side --dry-run=server -f deploy/manifests/crds
kubectl apply --server-side -f deploy/manifests/crds

helm upgrade --install inferops-operator deploy/helm/inferops-operator \
  --namespace inferops-system \
  --create-namespace \
  --atomic \
  --wait

helm upgrade --install inferops-gateway deploy/helm/inferops-gateway \
  --namespace inferops-system \
  --atomic \
  --wait
```

Repeat the same commands with the next chart image tags to verify in-place
upgrade behavior. Back up custom resources and referenced Secrets before
changing CRDs or controller images.

## Security Checklist

- Operator RBAC is limited to InferOps CRDs, managed Kubernetes resources,
  node reads, referenced Secret gets, and Events.
- Gateway RBAC is read-only discovery of ModelDeployments, Services, and
  EndpointSlices.
- Dashboard RBAC is read-only and excludes Secrets and mutation.
- Gateway bearer tokens and model-download credentials are mounted or injected
  from Kubernetes Secrets and are not copied into ConfigMaps, generated YAML,
  logs, or dashboard responses.
- Pods run as non-root, drop Linux capabilities, use the runtime-default
  seccomp profile, and set read-only root filesystems where supported.
- NetworkPolicies are enabled by default. Operators should replace sample API
  server CIDRs with the cluster's exact Kubernetes Service IPs.
- Runtime pods are not directly exposed; only the gateway and optional
  dashboard Service are user-facing.

## Readiness Checklist

- Operator, gateway, runtime, and dashboard charts render and pass schema
  validation.
- Liveness and readiness probes are configured for operator, gateway, runtime,
  and dashboard pods.
- `inferops doctor` reports Kubernetes API access, GPU/device-plugin state,
  cache path state, gateway reachability, auth state, and chart release state.
- The single-node homelab drill proves install, cache, activate, streaming
  inference, deactivate, reactivate without redownload, and explicit
  replacement.
- Simulated multi-GPU tests and any available real multi-GPU hardware prove the
  scheduler does not overcommit compatible capacity.
- Failure drills cover image pull failure, missing Secret, cache download
  failure, insufficient GPU, runtime readiness failure, and replacement
  rollback.

## Known Release Risks

- Real multi-GPU coverage depends on available hardware. If only one GPU is
  available, record simulated multi-GPU results and call out the hardware gap in
  release notes.
- GPU slicing is out of scope.
- No hosted InferOps control plane exists; all UI, gateway, and operator
  components run in the user's cluster.
- The dashboard is read-only in this MVP.
