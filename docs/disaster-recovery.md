# Backup and Disaster Recovery

Back up Kubernetes custom resources and model cache bytes. A logical
Kubernetes backup alone cannot recover a lost node-local cache.

## Backup scope

Back up:

- `ModelDeployment`, `ModelRuntime`, `ModelCache` objects;
- Secrets referenced by `spec.secrets.huggingFaceTokenSecretName` or
  `ModelCache.spec.secretRef`;
- Helm values and exact image digests;
- Cache data when avoiding re-download matters.

Encrypt backups and restrict access. Do not commit them.

## Logical backup

```bash
set -eu
namespace=default
backup_dir="inferops-backup-$(date -u +%Y%m%dT%H%M%SZ)"
umask 077
mkdir "$backup_dir"

for resource in modelruntimes modelcaches modeldeployments; do
  kubectl get "$resource" -n "$namespace" -o json \
    | python3 scripts/sanitize_kubernetes_export.py \
    > "$backup_dir/$resource.json"
done

kubectl get crd \
  modeldeployments.inference.inferops.dev \
  modelruntimes.inference.inferops.dev \
  modelcaches.inference.inferops.dev \
  -o json \
  | python3 scripts/sanitize_kubernetes_export.py \
  > "$backup_dir/crds.json"

helm get values inferops-operator -n "$namespace" --all -o yaml \
  > "$backup_dir/operator-values.yaml"

helm get values inferops-gateway -n "$namespace" --all -o yaml \
  > "$backup_dir/gateway-values.yaml"
```

Export Secrets through your organization's secret backup process.

## Restore order

1. Restore cluster datastore or create a compatible cluster.
2. Install storage classes, GPU plugins, ingress, and Secret controllers.
3. Apply backed-up CRDs, then current CRD upgrades.
4. Restore Secrets.
5. Install operator and gateway with recorded values and pinned images.
6. Restore node-local cache data when required, or let the controller re-download.
7. Restore `ModelRuntime`, then `ModelCache`, then `ModelDeployment`.

```bash
kubectl apply -f inferops-backup/crds.json
kubectl apply --server-side -f deploy/manifests/crds

helm upgrade --install inferops-operator ./deploy/helm/inferops-operator \
  --namespace "$namespace" --values inferops-backup/operator-values.yaml \
  --atomic --wait

helm upgrade --install inferops-gateway ./deploy/helm/inferops-gateway \
  --namespace "$namespace" --values inferops-backup/gateway-values.yaml \
  --atomic --wait

kubectl apply -f inferops-backup/modelruntimes.json
kubectl apply -f inferops-backup/modelcaches.json
kubectl apply -f inferops-backup/modeldeployments.json
```

Never restore `status`. Controllers reconstruct it.

## Recovery validation

Run this drill on a disposable cluster:

1. Back up an inactive CPU deployment and an active GPU deployment.
2. Restore into a clean cluster without copying status.
3. Verify admission rejects an invalid image and accepts backed-up resources.
4. Confirm CPU deployment becomes ready.
5. Confirm missing GPU capacity is reported, then add the device plugin and
   verify recovery.
6. Drain a node and verify `PodDisruptionBudget` behavior.
7. Delete a cache and verify fresh download and checksum before routing.
8. Run `make verify` against the source revision used for recovered images.
