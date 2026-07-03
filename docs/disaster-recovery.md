# Backup and disaster recovery

InferOps stores desired state in Kubernetes custom resources and model bytes in
the configured cache storage. Back up both layers. A logical Kubernetes backup
alone cannot recover a lost node-local model cache.

## Backup scope

Back up:

- the cluster datastore or the InferOps CRDs and all `ModelDeployment`,
  `ModelRuntime`, and `ModelCache` objects;
- every Secret referenced by `spec.secrets.huggingFaceTokenSecretName` or
  `ModelCache.spec.secretRef`;
- Helm values and the exact operator, gateway, downloader, and runtime image
  digests;
- cache data when avoiding a full model re-download is a recovery requirement.

Treat backups containing Secrets as credentials: encrypt them, restrict access,
and define a retention period. Do not commit them to this repository.

For k3s, an etcd snapshot or SQLite datastore backup is the preferred
cluster-level recovery mechanism. Follow the datastore procedure for the exact
k3s version in use. Velero or an equivalent Kubernetes backup controller is
appropriate when it is already part of the cluster's operations stack.

## Logical backup

The following example requires `kubectl` and a source checkout containing
`scripts/sanitize_kubernetes_export.py`. It exports restorable specs without
status, UIDs, resource versions, owner references, or managed fields:

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

Export referenced Secrets separately through the organization's secret backup
process. The example intentionally does not dump every Secret in a namespace.

Record a checksum for each file and copy the directory to encrypted storage.
For node-local caches, stop writers or take a storage-level snapshot that is
consistent with the recorded `ModelCache.spec.revision`.

## Restore order

1. Restore the Kubernetes datastore, or create a clean compatible cluster.
2. Install required storage classes, GPU device plugins, ingress components,
   and Secret-management controllers.
3. Apply the backed-up CRDs, then apply the current additive CRD upgrade.
4. Restore referenced Secrets.
5. Install or upgrade the operator and gateway with the recorded values and
   pinned images. This lets Helm create or adopt its packaged `ModelRuntime`
   objects before applying backed-up runtime overrides.
6. Restore node-local cache data to the recorded node and path when preserving
   it is required. Otherwise allow the cache controller to download it again.
7. Restore `ModelRuntime`, then `ModelCache`, then `ModelDeployment` objects.

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

Never restore `status`: it describes the old cluster, including node-local
placements. Controllers must reconstruct it from the recovered cluster.

## Recovery validation

Run the following drill on a disposable cluster before relying on a release:

1. Back up a namespace containing an inactive CPU deployment and an active GPU
   deployment.
2. Restore into a clean cluster without copying status.
3. Verify admission rejects an invalid runtime image and accepts the backed-up
   resources.
4. Confirm the CPU deployment becomes ready through the llama.cpp path.
5. Confirm missing GPU capacity is reported as a condition, then install the
   device plugin and verify recovery without editing status.
6. Drain a node and verify each configured `PodDisruptionBudget` behaves as
   intended.
7. Delete a disposable node-local cache and verify a fresh download and
   checksum before routing becomes ready.
8. Run `make verify` against the exact source revision used for the recovered
   images.

Record Kubernetes, Helm, GPU plugin, storage, and InferOps versions plus the
time to restore. A drill is incomplete if any untested hardware-dependent case
is silently marked successful.
