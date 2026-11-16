# Shared Model Cache TODO

Goal: make identical model artifacts share one cache record/path across
deployments, while keeping tenant, credential, deletion, and rollout behavior
safe for production.

## Cache Identity

- [ ] Define an artifact cache identity that does not include deployment name.
- [ ] Include namespace/tenant boundary in the identity.
- [ ] Include model source, repository, revision, cache type, and cache root/path.
- [ ] Include credential boundary, e.g. `secretRef`, so private/gated model access is not accidentally shared across different credentials.
- [ ] Normalize empty revision to `main` before hashing.
- [ ] Decide whether runtime engine belongs in the cache identity. Prefer excluding it when the artifact format is identical, but include it if runtimes require incompatible layouts.
- [ ] Add unit tests proving two deployments with the same identity resolve to the same cache name/path.
- [ ] Add unit tests proving different namespace, revision, repo, source, root, or secretRef creates a different cache identity.

## Cache Naming And Path

- [ ] Replace deployment-name cache paths with artifact-hash paths, for example:
  `/var/lib/inferops/models/<namespace>/artifacts/<hash>`.
- [ ] Keep names DNS-safe and stable, for example:
  `model-<hash>-cache`.
- [ ] Keep the human-readable repo/name visible in labels or annotations for CLI/Grafana display.
- [ ] Validate generated paths stay under the configured cache root.
- [ ] Ensure old deployment-scoped cache paths remain readable during migration.

## Ownership Model

- [ ] Remove single-deployment `ownerReference` from shared `ModelCache` objects.
- [ ] Label/annotate each shared cache with artifact identity fields and hash.
- [ ] Keep deployment references computed from live `ModelDeployment` objects, not from a stored manual counter.
- [ ] Avoid manual reference counters because they can drift after crashes, deletes, or failed reconciles.
- [ ] Decide if a future cache GC controller owns unreferenced cache cleanup.

## Reference Safety

- [ ] Update CLI `cache list` to compute `referencedBy` from artifact identity.
- [ ] Update CLI `cache delete` to block deletion while any live deployment references the shared cache.
- [ ] Keep `--force` available, but make it explicit and high-friction.
- [ ] Include live Pod mount checks before cache deletion.
- [ ] Add race checks so cache deletion fails if deployments or pods change during safety verification.
- [ ] Add tests for shared cache referenced by multiple deployments.
- [ ] Add tests for forced deletion reporting all affected deployments and pods.

## Operator Reconcile Flow

- [ ] Update `cachecontract.Identity`, `Name`, and `Path` to use artifact identity.
- [ ] Update `ensureModelCache` so multiple deployments reconcile to the same `ModelCache`.
- [ ] Ensure `ModelCache` spec updates are safe when a deployment changes model repo/revision.
- [ ] Ensure deployments wait on the shared cache until it is `Ready`.
- [ ] Ensure one cache download job is created for the shared cache, not one per deployment.
- [ ] Ensure concurrent deployments for the same artifact do not race-create duplicate caches.
- [ ] Ensure activation touches `lastUsedTime` on the shared cache.
- [ ] Ensure deactivation/deletion of one deployment does not remove shared cache needed by another.

## Scheduling And Capacity

- [ ] Reserve cache capacity once per shared artifact, not once per deployment.
- [ ] Update capacity planner tests for multiple deployments sharing one cache.
- [ ] Ensure cache placement still respects node selectors and tolerations.
- [ ] Decide behavior when two deployments with same model request incompatible cache placement constraints.
- [ ] Surface a clear condition when deployments cannot share because placement contracts differ.

## Credentials And Tenancy

- [ ] Treat namespace as a hard sharing boundary by default.
- [ ] Treat `secretRef` as part of the cache identity by default.
- [ ] Document why public same-repo models share, while different secrets/private boundaries do not.
- [ ] Avoid logging secret names in error messages where that could leak policy details.
- [ ] Consider an explicit future opt-in for cross-namespace shared caches.

## Migration

- [ ] Keep existing deployment-scoped caches valid after upgrade.
- [ ] Decide whether new reconciles create artifact-scoped caches immediately or only for new deployments.
- [ ] Add a migration note: old per-deployment caches can be deleted after deployments move to shared cache.
- [ ] Add CLI output that distinguishes legacy deployment-scoped caches from artifact-scoped caches.
- [ ] Avoid automatic deletion of old node-local files during migration.

## CLI And UX

- [ ] Update `inferops cache list` to show artifact identity, references, phase, node, path, size, and legacy/shared mode.
- [ ] Update `inferops diagnose` to report the shared cache name and all referencing deployments.
- [ ] Update `inferops deploy` docs to explain same model/revision reuses cache.
- [ ] Add a command or output hint for cache capacity pressure caused by non-shared legacy caches.
- [ ] Update decorator and YAML examples to demonstrate two deployments sharing one cache.

## Observability

- [ ] Add metrics for shared cache reference count computed by the operator.
- [ ] Add metrics for reserved bytes by shared artifact cache.
- [ ] Add events when a deployment reuses an existing ready cache.
- [ ] Add events when a deployment joins a cache that is still downloading.
- [ ] Update Grafana dashboard panels for cache count, reference count, reserved bytes, and download reuse.

## Tests

- [ ] Unit-test cache identity and path generation.
- [ ] Unit-test lifecycle reconcile for two deployments sharing one cache.
- [ ] Unit-test deletion of one deployment preserving shared cache.
- [ ] Unit-test cache delete refusal with multiple references.
- [ ] Unit-test capacity planner counts shared cache once.
- [ ] Integration-test decorator app with two same-model deployments.
- [ ] Integration-test same repo with different revision creates two caches.
- [ ] Integration-test same repo with different secretRef creates two caches.
- [ ] Regression-test CLI stale local deploy state still reapplies missing resources.

## Documentation

- [ ] Document cache identity rules.
- [ ] Document sharing boundaries: namespace and secretRef.
- [ ] Document cache deletion safety.
- [ ] Document migration from deployment-scoped caches.
- [ ] Document expected behavior for same public model across multiple deployments.

## Acceptance Criteria

- [ ] Deploying `cpu-smollm-a` and `cpu-smollm-b` with the same repo/revision creates one shared `ModelCache`.
- [ ] `inferops cache list` shows one cache referenced by both deployments.
- [ ] Cache capacity is reserved once, not once per deployment.
- [ ] Deleting one deployment keeps the shared cache.
- [ ] `inferops cache delete <cache>` refuses while either deployment references it.
- [ ] Changing model revision creates a separate cache.
- [ ] Changing secretRef creates a separate cache.
- [ ] Existing deployment-scoped caches do not break after upgrade.
