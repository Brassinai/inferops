"""Test-only fake Kubernetes client for CLI handler tests."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from inferops_cli.errors import CLIError, NotFoundError
from inferops_cli.kube import (
    ActivationRequest,
    CacheDeleteRequest,
    ClusterTarget,
    DeactivationRequest,
    DeployRequest,
    DoctorRequest,
    InstallRequest,
    LogsRequest,
    NamedRequest,
    StatusRequest,
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
        self._activation_outcomes: dict[_ResourceKey, str] = {}

    def deploy(self, request: DeployRequest) -> dict[str, Any]:
        deployments = []
        for manifest in request.manifests:
            name = manifest["metadata"]["name"]
            key = self._resource_key(request.cluster, name)
            deployment = {
                "name": name,
                "namespace": request.cluster.namespace,
                "phase": (
                    "Active"
                    if manifest["spec"]["activation"]["desiredState"] == "Active"
                    else "Cached"
                ),
                "desiredState": manifest["spec"]["activation"]["desiredState"],
                "whenFull": manifest["spec"]["activation"].get("whenFull", "Queue"),
                "runtime": manifest["spec"]["runtime"]["ref"],
                "model": manifest["spec"]["model"]["repo"],
                "endpoint": f"/models/{name}/v1",
                "serviceName": f"{name}-runtime",
                "assignedNode": "",
                "assignedGPUs": [],
                "cache": {},
                "replicas": {},
                "modelLoaded": False,
                "observedGeneration": 1,
                "generation": 1,
                "conditions": [],
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
            result = deployment.copy()
            result["phase"] = manifest["spec"]["activation"]["desiredState"]
            deployments.append(result)

        return {
            "mode": "fake",
            "cluster": request.cluster.to_safe_dict(),
            "appPath": request.app_path,
            "activate": request.activate,
            "whenFull": request.when_full,
            "deployments": sorted(deployments, key=lambda item: item["name"]),
            "message": "Deployment applied to the fake Kubernetes client.",
        }

    def activate(self, request: ActivationRequest) -> dict[str, Any]:
        deployment = self._require_deployment(request)
        deployment["desiredState"] = "Active"
        if request.when_full is not None:
            deployment["whenFull"] = request.when_full
        if not request.wait:
            return {
                "mode": "fake",
                "cluster": request.cluster.to_safe_dict(),
                "deployment": deployment.copy(),
                "operation": "activate",
                "outcome": "requested",
                "transitions": [],
            }

        key = self._resource_key(request.cluster, request.name)
        outcome = self._activation_outcomes.get(key, "active")
        if outcome == "waiting" and deployment["whenFull"] != "Queue":
            outcome = "timeout"
        conditions: list[dict[str, Any]] = []
        if outcome in {"waiting", "timeout"}:
            deployment["phase"] = "WaitingForGPU"
            conditions = [
                {
                    "type": "Ready",
                    "status": "False",
                    "reason": "InsufficientGPU",
                    "message": "waiting for a compatible GPU",
                }
            ]
        elif outcome == "rejected":
            deployment["phase"] = "Failed"
            conditions = [
                {
                    "type": "Ready",
                    "status": "False",
                    "reason": "CapacityRejected",
                    "message": "capacity is full and the policy is Reject",
                }
            ]
        elif outcome == "failed":
            deployment["phase"] = "Failed"
            conditions = [
                {
                    "type": "Ready",
                    "status": "False",
                    "reason": "RuntimeFailed",
                    "message": "runtime failed to become ready",
                }
            ]
        else:
            deployment["phase"] = "Active"
            deployment["modelLoaded"] = True
            deployment["replicas"] = {"desired": 1, "ready": 1}
            conditions = [
                {
                    "type": "Ready",
                    "status": "True",
                    "reason": "RuntimeReady",
                    "message": "runtime is ready",
                }
            ]
        deployment["conditions"] = conditions
        transition = {
            "phase": deployment["phase"],
            "observedGeneration": deployment["observedGeneration"],
            "reason": conditions[0]["reason"],
            "message": conditions[0]["message"],
        }
        transitions = [transition]
        if outcome == "active" and deployment["whenFull"] in {
            "ReplaceOldest",
            "ReplaceLowestPriority",
        }:
            deployment["replacement"] = {
                "phase": "Completed",
                "requestGeneration": deployment["generation"],
                "target": {
                    "namespace": deployment["namespace"],
                    "name": "previous-model",
                    "uid": "previous-model-uid",
                },
                "message": "replacement completed and runtime is ready",
            }
            transitions = [
                {
                    "phase": "WaitingForGPU",
                    "observedGeneration": deployment["observedGeneration"],
                    "reason": "ReplacementSelected",
                    "message": "selected previous-model for replacement",
                },
                {
                    "phase": "WaitingForGPU",
                    "observedGeneration": deployment["observedGeneration"],
                    "reason": "ReplacementDraining",
                    "message": "waiting for previous-model to drain",
                },
                {
                    "phase": "Activating",
                    "observedGeneration": deployment["observedGeneration"],
                    "reason": "RuntimeStarting",
                    "message": "replacement capacity released; runtime is starting",
                },
                transition,
            ]
        if request.on_transition is not None:
            for observed in transitions:
                request.on_transition(observed)
        return {
            "mode": "fake",
            "cluster": request.cluster.to_safe_dict(),
            "deployment": deployment.copy(),
            "operation": "activate",
            "outcome": outcome,
            "transitions": transitions,
        }

    def deactivate(self, request: DeactivationRequest) -> dict[str, Any]:
        deployment = self._require_deployment(request)
        deployment["desiredState"] = "Inactive"
        if not request.wait:
            return {
                "mode": "fake",
                "cluster": request.cluster.to_safe_dict(),
                "deployment": deployment.copy(),
                "operation": "deactivate",
                "outcome": "requested",
                "transitions": [],
            }
        deployment["phase"] = "Cached"
        deployment["modelLoaded"] = False
        deployment["replicas"] = {"desired": 0, "ready": 0}
        deployment["conditions"] = [
            {
                "type": "Ready",
                "status": "False",
                "reason": "Inactive",
                "message": "runtime is inactive and the cache is preserved",
            }
        ]
        transition = {
            "phase": "Cached",
            "observedGeneration": deployment["observedGeneration"],
            "reason": "Inactive",
            "message": "runtime is inactive and the cache is preserved",
        }
        if request.on_transition is not None:
            request.on_transition(transition)
        return {
            "mode": "fake",
            "cluster": request.cluster.to_safe_dict(),
            "deployment": deployment.copy(),
            "operation": "deactivate",
            "outcome": "inactive",
            "transitions": [transition],
        }

    def status(self, request: StatusRequest) -> dict[str, Any]:
        deployment = self._require_deployment(request)
        response = {
            "mode": "fake",
            "cluster": request.cluster.to_safe_dict(),
            "deployment": deployment.copy(),
            "message": "Deployment status fetched from the fake Kubernetes client.",
        }
        if request.watch:
            phase = deployment["phase"]
            if phase == "Active":
                outcome = "active"
            elif (
                phase in {"WaitingForCapacity", "WaitingForGPU"}
                and deployment["whenFull"] == "Queue"
            ):
                outcome = "waiting"
            elif phase == "Failed":
                outcome = "failed"
            elif phase in {"Cached", "Pending"}:
                outcome = "inactive"
            else:
                outcome = "timeout"
            transition = {
                "phase": phase,
                "observedGeneration": deployment["observedGeneration"],
            }
            if request.on_transition is not None:
                request.on_transition(transition)
            response.update(
                {
                    "operation": "status",
                    "outcome": outcome,
                    "transitions": [transition],
                }
            )
        return response

    def models(self, cluster: ClusterTarget) -> dict[str, Any]:
        return {
            "mode": "fake",
            "cluster": cluster.to_safe_dict(),
            "models": sorted(
                (
                    deployment.copy()
                    for key, deployment in self._deployments.items()
                    if key.namespace == cluster.namespace
                    and key.context == cluster.context
                ),
                key=lambda item: item["name"],
            ),
        }

    def endpoints(self, cluster: ClusterTarget) -> dict[str, Any]:
        models = self.models(cluster)["models"]
        return {
            "mode": "fake",
            "cluster": cluster.to_safe_dict(),
            "endpoints": [
                {
                    "name": model["name"],
                    "namespace": model["namespace"],
                    "phase": model["phase"],
                    "ready": model["phase"] == "Active",
                    "endpoint": model["endpoint"],
                    "serviceName": model["serviceName"],
                }
                for model in models
            ],
        }

    def logs(self, request: LogsRequest) -> dict[str, Any]:
        deployment = self._require_deployment(
            NamedRequest(cluster=request.cluster, name=request.name)
        )
        lines = [
            f"{deployment['name']}: runtime log stream from fake Kubernetes client",
            f"{deployment['name']}: phase={deployment['phase']} namespace={deployment['namespace']}",
        ]
        return {
            "mode": "fake",
            "cluster": request.cluster.to_safe_dict(),
            "deployment": deployment.copy(),
            "tail": request.tail,
            "lines": lines[: request.tail],
            "message": "Runtime logs fetched from the fake Kubernetes client.",
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
        exposure = request.exposure or (
            "tailscale" if request.tailscale_hostname else "cluster-ip"
        )
        resources = [
            f"namespace/{request.cluster.namespace}",
            "crd/modeldeployments.inference.inferops.dev",
            "deployment/inferops-operator",
        ]
        if exposure == "load-balancer":
            resources.append("service/inferops-gateway")
        elif exposure == "ingress":
            resources.append("ingress/inferops-gateway")
        elif exposure == "gateway-api":
            resources.append("httproute/inferops-gateway")
        elif exposure == "tailscale":
            resources.append("ingress/inferops-gateway-tailscale")
        install = {
            "profile": request.profile,
            "namespace": request.cluster.namespace,
            "cachePath": request.cache_path,
            "tailscaleHostname": request.tailscale_hostname,
            "exposure": exposure,
            "authEnabled": bool(request.gateway_auth_secret),
            "resources": resources,
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
        cache = self._caches.get(key)
        if cache is not None:
            cache["referencedBy"] = []
        return {
            "mode": "fake",
            "cluster": request.cluster.to_safe_dict(),
            "deployment": deployment,
            "deleted": True,
            "cachePreserved": True,
            "message": "Deployment deleted; model cache preserved.",
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
