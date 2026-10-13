# Core Platform Gate

Security, upgrade, and readiness checks before using the core platform as a
stable base.

## Required verification

```bash
make setup
make verify
```

Focused checks:

```bash
make helm-lint
make helm-template
make schema-check
```

## Install and upgrade

```bash
kubectl apply --server-side --dry-run=server -f deploy/manifests/crds
kubectl apply --server-side -f deploy/manifests/crds

helm upgrade --install inferops-operator deploy/helm/inferops-operator \
  --namespace inferops-system \
  --create-namespace \
  --atomic --wait

helm upgrade --install inferops-gateway deploy/helm/inferops-gateway \
  --namespace inferops-system \
  --atomic --wait
```

Back up custom resources and Secrets before changing CRDs or images.

## Security checklist

- Operator RBAC is limited to InferOps CRDs, managed resources, Node reads,
  referenced Secret gets, and Events.
- Gateway RBAC is read-only discovery.
- Dashboard RBAC is read-only and excludes Secrets and mutation.
- Secrets are mounted, not copied into ConfigMaps, logs, or generated YAML.
- Pods run non-root, drop capabilities, use runtime-default seccomp, and
  read-only root filesystems where supported.
- NetworkPolicies are enabled by default.
- Runtime pods are not directly exposed.

## Readiness checklist

- Operator, gateway, runtime, and dashboard charts render and pass schema validation.
- Probes configured for operator, gateway, runtime, and dashboard.
- `inferops doctor` passes.
- Homelab drill proves install, cache, activate, inference, deactivate,
  reactivate, and replacement.
- Multi-GPU tests prove no overcommitment.
- Failure drills cover image pull, missing Secret, cache failure, insufficient
  GPU, readiness failure, and replacement rollback.

## Known release risks

- Real multi-GPU coverage depends on available hardware.
- GPU slicing is out of scope.
- No hosted control plane; everything runs in-cluster.
- Dashboard is read-only.
