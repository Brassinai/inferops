"""Test-only fake Kubernetes client for CLI handler tests."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from inferops_cli.errors import CLIError, NotFoundError
from inferops_cli.kube import (
    CacheDeleteRequest,
    ClusterTarget,
    DeployRequest,
    DoctorRequest,
    InstallRequest,
    LogsRequest,
    NamedRequest,
)


@dataclass(frozen=True)
class _ResourceKey:
    """Namespace-safe key for fake cluster resources."""

    namespace: str
    context: str | None
    name: str


class FakeKubernetesClient:
    """In-memory fake used only by tests."""

    def __init__(self) -> None:
        self._deployments: dict[_ResourceKey, dict[str, Any]] = {}
        self._caches: dict[_ResourceKey, dict[str, Any]] = {}
        self._installs: list[dict[str, Any]] = []
        self._gpus: list[dict[str, Any]] = []
        self._doctor_checks: list[dict[str, Any]] = []
        self._cache_deletable: bool = True

    def deploy(self, request: DeployRequest) -> dict[str, Any]:
        deployments = []
        for manifest in request.manifests:
            name = manifest["metadata"]["name"]
            key = self._resource_key(request.cluster, name)
            deployment = {
                "name": name,
                "namespace": request.cluster.namespace,
                "phase": "Active"
                if request.activate
                else manifest["spec"]["activation"]["desiredState"],
                "runtime": manifest["spec"]["runtime"]["ref"],
                "model": manifest["spec"]["model"]["repo"],
                "action": "created" if key not in self._deployments else "replaced",
            }
            self._deployments[key] = deployment

            cache_spec = manifest["spec"]["cache"]
            self._caches[key] = {
                "name": name,
                "namespace": request.cluster.namespace,
                "phase": "Prepared" if cache_spec["enabled"] else "Disabled",
                "repository": manifest["spec"]["model"]["repo"],
                "revision": manifest["spec"]["model"].get("revision", "main"),
                "node": "",
                "path": cache_spec["path"],
                "size": cache_spec["size"],
                "lastUsed": "",
                "referencedBy": [name],
                "referencesKnown": True,
                "issues": [],
            }
            deployments.append(deployment)

        return {
            "mode": "fake",
            "cluster": request.cluster.to_safe_dict(),
            "appPath": request.app_path,
            "activate": request.activate,
            "whenFull": request.when_full,
            "deployments": sorted(deployments, key=lambda item: item["name"]),
            "message": "Placeholder deploy executed against the fake Kubernetes client.",
        }

    def activate(self, request: NamedRequest) -> dict[str, Any]:
        deployment = self._require_deployment(request)
        deployment["phase"] = "Active"
        return {
            "mode": "fake",
            "cluster": request.cluster.to_safe_dict(),
            "deployment": deployment.copy(),
            "message": "Placeholder activation executed against the fake Kubernetes client.",
        }

    def deactivate(self, request: NamedRequest) -> dict[str, Any]:
        deployment = self._require_deployment(request)
        deployment["phase"] = "Inactive"
        return {
            "mode": "fake",
            "cluster": request.cluster.to_safe_dict(),
            "deployment": deployment.copy(),
            "message": "Placeholder deactivation executed against the fake Kubernetes client.",
        }

    def status(self, request: NamedRequest) -> dict[str, Any]:
        deployment = self._require_deployment(request)
        return {
            "mode": "fake",
            "cluster": request.cluster.to_safe_dict(),
            "deployment": deployment.copy(),
            "message": "Placeholder status fetched from the fake Kubernetes client.",
        }

    def logs(self, request: LogsRequest) -> dict[str, Any]:
        deployment = self._require_deployment(
            NamedRequest(cluster=request.cluster, name=request.name)
        )
        lines = [
            f"{deployment['name']}: placeholder log stream from fake Kubernetes client",
            f"{deployment['name']}: phase={deployment['phase']} namespace={deployment['namespace']}",
        ]
        return {
            "mode": "fake",
            "cluster": request.cluster.to_safe_dict(),
            "deployment": deployment.copy(),
            "tail": request.tail,
            "lines": lines[: request.tail],
            "message": "Placeholder logs fetched from the fake Kubernetes client.",
        }

    def gpu_list(self, cluster: ClusterTarget) -> dict[str, Any]:
        return {
            "mode": "fake",
            "cluster": cluster.to_safe_dict(),
            "gpus": list(self._gpus),
            "message": "GPU inventory from fake Kubernetes client.",
        }

    def cache_list(self, cluster: ClusterTarget) -> dict[str, Any]:
        caches = [
            cache.copy()
            for key, cache in self._caches.items()
            if key.namespace == cluster.namespace and key.context == cluster.context
        ]
        return {
            "mode": "fake",
            "cluster": cluster.to_safe_dict(),
            "caches": sorted(caches, key=lambda item: item["name"]),
            "message": "Cache inventory from fake Kubernetes client.",
        }

    def cache_delete(self, request: CacheDeleteRequest) -> dict[str, Any]:
        key = self._resource_key(request.cluster, request.name)
        cache = self._caches.get(key)
        if cache is None:
            raise NotFoundError(f"cache not found: {request.name}")
        refs = cache.get("referencedBy", [])
        if refs and not request.force:
            raise CLIError(
                f"cache '{request.name}' is referenced by deployments: {', '.join(refs)}. "
                "Use --force only after confirming the deployment can lose the cache registration."
            )
        if request.force and refs:
            cache["deleteAnnotated"] = True
        self._caches.pop(key, None)
        return {
            "mode": "fake",
            "cluster": request.cluster.to_safe_dict(),
            "cache": cache,
            "force": request.force,
            "annotated": request.force and bool(refs),
            "deleted": True,
            "nodeFilesModified": False,
        }

    def install(self, request: InstallRequest) -> dict[str, Any]:
        install = {
            "profile": request.profile,
            "namespace": request.cluster.namespace,
            "cachePath": request.cache_path,
            "tailscaleHostname": request.tailscale_hostname,
            "resources": [
                f"namespace/{request.cluster.namespace}",
                "crd/modeldeployments.inference.inferops.dev",
                "deployment/inferops-operator",
            ],
        }
        self._installs.append(install)
        return {
            "mode": "fake",
            "cluster": request.cluster.to_safe_dict(),
            "install": install,
            "message": "Placeholder install executed against the fake Kubernetes client.",
        }

    def delete(self, request: NamedRequest) -> dict[str, Any]:
        deployment = self._require_deployment(request).copy()
        key = self._resource_key(request.cluster, request.name)
        self._deployments.pop(key, None)
        return {
            "mode": "fake",
            "cluster": request.cluster.to_safe_dict(),
            "deployment": deployment,
            "deleted": True,
            "message": "Placeholder delete executed against the fake Kubernetes client.",
        }

    def doctor(self, request: DoctorRequest) -> dict[str, Any]:
        checks = [c.copy() for c in self._doctor_checks]
        if request.checks:
            checks = [c for c in checks if c["id"] in request.checks]
        return {
            "mode": "fake",
            "cluster": request.cluster.to_safe_dict(),
            "checks": checks,
            "message": "Placeholder doctor executed against the fake Kubernetes client.",
        }

    def _require_deployment(self, request: NamedRequest) -> dict[str, Any]:
        key = self._resource_key(request.cluster, request.name)
        deployment = self._deployments.get(key)
        if deployment is None:
            raise NotFoundError(f"deployment not found: {request.name}")
        return deployment

    def _resource_key(self, cluster: ClusterTarget, name: str) -> _ResourceKey:
        return _ResourceKey(
            namespace=cluster.namespace, context=cluster.context, name=name
        )
