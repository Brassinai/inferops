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
    DiagnoseRequest,
    DeployRequest,
    DoctorRequest,
    EndpointAppDeployRequest,
    InstallRequest,
    LogsRequest,
    NamedRequest,
    ObservabilityEnableRequest,
    ObservabilityInstallRequest,
    ObservabilitySetupRequest,
    StatusRequest,
    UninstallRequest,
    UpgradeRequest,
)
from inferops_cli.lifecycle import activation_diagnosis, deployment_diagnosis_report


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
        self._endpoint_apps: dict[_ResourceKey, dict[str, Any]] = {}
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

    def deploy_endpoint_app(self, request: EndpointAppDeployRequest) -> dict[str, Any]:
        key = self._resource_key(request.cluster, request.name)
        action = "created" if key not in self._endpoint_apps else "configured"
        endpoint_app = {
            "name": request.name,
            "namespace": request.cluster.namespace,
            "image": request.image,
            "replicas": request.replicas,
            "port": request.port,
            "serviceName": request.name,
            "gatewayURL": request.gateway_url,
            "containerAppPath": request.container_app_path,
            "env": dict(request.env),
            "deploymentAction": action,
            "serviceAction": action,
        }
        self._endpoint_apps[key] = endpoint_app
        return {
            "mode": "fake",
            "cluster": request.cluster.to_safe_dict(),
            "endpointApp": endpoint_app.copy(),
            "message": "Endpoint app applied to the fake Kubernetes client.",
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
        response = {
            "mode": "fake",
            "cluster": request.cluster.to_safe_dict(),
            "deployment": deployment.copy(),
            "operation": "activate",
            "outcome": outcome,
            "transitions": transitions,
        }
        if outcome in {"failed", "rejected", "timeout", "waiting"}:
            response["diagnosis"] = activation_diagnosis(
                deployment.copy(),
                outcome=outcome,
                event={
                    "reason": conditions[0]["reason"],
                    "message": conditions[0]["message"],
                    "involvedObject": {
                        "kind": "ModelDeployment",
                        "namespace": deployment["namespace"],
                        "name": deployment["name"],
                    },
                },
                log_tail={
                    "pod": f"{deployment['name']}-runtime-0",
                    "namespace": deployment["namespace"],
                    "container": "runtime",
                    "tail": 20,
                    "lines": [
                        "runtime starting",
                        conditions[0]["message"],
                    ],
                },
                checked_resources=[
                    {
                        "kind": "PodList",
                        "namespace": deployment["namespace"],
                        "name": "runtime Pod",
                        "status": "1 found",
                    }
                ],
                verbose=request.verbose,
            )
        return response

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
            (
                f"{deployment['name']}: phase={deployment['phase']} "
                f"namespace={deployment['namespace']}"
            ),
        ]
        return {
            "mode": "fake",
            "cluster": request.cluster.to_safe_dict(),
            "deployment": deployment.copy(),
            "tail": request.tail,
            "lines": lines[: request.tail],
            "message": "Runtime logs fetched from the fake Kubernetes client.",
        }

    def diagnose(self, request: DiagnoseRequest) -> dict[str, Any]:
        deployment = self._require_deployment(request).copy()
        reason = "RuntimeReady" if deployment["phase"] == "Active" else "RuntimePending"
        message = "runtime is ready"
        if deployment["desiredState"] == "Inactive" and deployment["phase"] != "Active":
            reason = "Inactive"
            message = "deployment is inactive; no runtime should be running yet"
        elif deployment["phase"] == "Failed":
            reason = "RuntimeFailed"
            message = "runtime failed to become ready"
        elif deployment["phase"] in {"WaitingForCapacity", "WaitingForGPU"}:
            reason = "InsufficientGPU"
            message = "waiting for a compatible GPU"
        event = {
            "reason": reason,
            "message": message,
            "involvedObject": {
                "kind": "ModelDeployment",
                "namespace": deployment["namespace"],
                "name": deployment["name"],
            },
        }
        checked_resources = [
            {
                "kind": "ModelDeployment",
                "namespace": deployment["namespace"],
                "name": deployment["name"],
                "status": deployment["phase"],
            },
            {
                "kind": "ModelCache",
                "namespace": deployment["namespace"],
                "name": deployment.get("cache", {}).get("name", deployment["name"]),
                "status": deployment.get("cache", {}).get("state", "missing"),
            },
            {
                "kind": "PodList",
                "namespace": deployment["namespace"],
                "name": "runtime Pod",
                "status": "1 found" if deployment["phase"] == "Active" else "0 found",
            },
        ]
        report = deployment_diagnosis_report(
            deployment,
            event=event,
            log_tail={
                "pod": f"{deployment['name']}-runtime-0",
                "namespace": deployment["namespace"],
                "container": "runtime",
                "tail": 20,
                "lines": [
                    "runtime probe from fake Kubernetes client",
                    message,
                ],
            },
            checked_resources=checked_resources,
            verbose=request.verbose,
        )
        return {
            "mode": "fake",
            "operation": "diagnose",
            "cluster": request.cluster.to_safe_dict(),
            "deployment": deployment,
            **report,
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
            "computeProfile": request.compute_profile,
            "namespace": request.cluster.namespace,
            "cachePath": request.cache_path,
            "cacheAnnotations": (
                [
                    {
                        "node": request.cache_node,
                        "annotation": "inferops.dev/cache-capacity",
                        "capacity": request.cache_capacity,
                    }
                ]
                if request.cache_node and request.cache_capacity
                else [
                    {
                        "node": item.split("=", 1)[0],
                        "annotation": "inferops.dev/cache-capacity",
                        "capacity": item.split("=", 1)[1],
                    }
                    for item in request.cache_node_capacities
                    if "=" in item
                ]
            ),
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

    def upgrade(self, request: UpgradeRequest) -> dict[str, Any]:
        if request.component == "operator":
            release_tags = {"inferops-operator": request.tag}
        elif request.component == "gateway":
            release_tags = {"inferops-gateway": request.tag}
        elif request.component == "dashboard":
            if not request.include_dashboard:
                raise CLIError(
                    "--component dashboard cannot be used with --skip-dashboard"
                )
            release_tags = {"inferops-dashboard": request.tag}
        elif request.component is None:
            release_tags = {
                "inferops-operator": request.tag,
                "inferops-gateway": request.tag,
            }
            if request.include_dashboard:
                release_tags["inferops-dashboard"] = request.tag
        else:
            raise CLIError(f"unsupported upgrade component: {request.component}")
        resources = [
            f"deployment/{release_name}" for release_name in release_tags
        ]
        if "inferops-operator" in release_tags:
            resources.insert(0, "crd/modeldeployments.inference.inferops.dev")
        upgrade = {
            "namespace": request.cluster.namespace,
            "tag": request.tag,
            "component": request.component,
            "operatorImage": request.operator_image_repository,
            "gatewayImage": request.gateway_image_repository,
            "dashboardImage": (
                request.dashboard_image_repository
                if "inferops-dashboard" in release_tags
                else None
            ),
            "operatorTag": release_tags.get("inferops-operator"),
            "gatewayTag": release_tags.get("inferops-gateway"),
            "dashboardTag": release_tags.get("inferops-dashboard"),
            "dashboardIncluded": request.include_dashboard,
            "observabilityEnabled": request.enable_observability,
            "resources": resources,
            "crds": {
                "status": (
                    "applied" if "inferops-operator" in release_tags else "skipped"
                ),
                "output": "",
            },
            "releases": [
                {
                    "name": release_name,
                    "chart": release_name,
                    "status": "upgraded",
                    "imageTag": tag,
                }
                for release_name, tag in release_tags.items()
            ],
        }
        return {
            "mode": "fake",
            "cluster": request.cluster.to_safe_dict(),
            "upgrade": upgrade,
            "message": "Placeholder upgrade executed against the fake Kubernetes client.",
        }

    def uninstall(self, request: UninstallRequest) -> dict[str, Any]:
        if request.purge_cache_files:
            if not request.cache_path:
                raise CLIError("--purge-cache-files requires --cache-path")
            if not request.cache_node_selector:
                raise CLIError("--purge-cache-files requires --cache-node-selector")
            if request.confirm_cache_purge != "DELETE-CACHE-FILES":
                raise CLIError(
                    "--purge-cache-files requires "
                    "--confirm-cache-purge DELETE-CACHE-FILES"
                )
        elif request.cache_path or request.cache_node_selector or request.confirm_cache_purge:
            raise CLIError(
                "--cache-path, --cache-node-selector, and "
                "--confirm-cache-purge require --purge-cache-files"
            )
        resources = [
            "helmrelease/inferops-operator",
            "helmrelease/inferops-gateway",
        ]
        if request.include_dashboard:
            resources.append("helmrelease/inferops-dashboard")
        if request.delete_crds:
            resources.extend(
                [
                    "crd/modelcaches.inference.inferops.dev",
                    "crd/modeldeployments.inference.inferops.dev",
                    "crd/modelruntimes.inference.inferops.dev",
                ]
            )
        if request.purge_cache_files:
            resources.append("daemonset/inferops-cache-purge")
        uninstall = {
            "namespace": request.cluster.namespace,
            "dashboardIncluded": request.include_dashboard,
            "crdsDeleted": request.delete_crds,
            "customResourcesPreserved": not request.delete_crds,
            "cacheFilesDeleted": request.purge_cache_files,
            "resources": resources,
            "releases": [
                {"name": resource.removeprefix("helmrelease/"), "status": "uninstalled"}
                for resource in resources
                if resource.startswith("helmrelease/")
            ],
            "crds": {
                "status": "deleted" if request.delete_crds else "preserved",
                "output": "",
            },
            "cachePurge": (
                {
                    "status": "purged",
                    "cachePath": request.cache_path,
                    "nodeSelector": request.cache_node_selector,
                    "commands": [],
                }
                if request.purge_cache_files
                else None
            ),
        }
        return {
            "mode": "fake",
            "cluster": request.cluster.to_safe_dict(),
            "uninstall": uninstall,
            "message": "Placeholder uninstall executed against the fake Kubernetes client.",
        }

    def observability_install(
        self, request: ObservabilityInstallRequest
    ) -> dict[str, Any]:
        observability = {
            "operation": "install",
            "monitoringNamespace": request.cluster.namespace,
            "release": request.release,
            "chart": request.chart,
            "chartVersion": request.chart_version,
            "grafanaAdminPasswordConfigured": bool(request.grafana_admin_password),
            "resources": [
                f"namespace/{request.cluster.namespace}",
                f"helmrelease/{request.release}",
                "deployment/grafana",
                "statefulset/prometheus",
            ],
        }
        return {
            "mode": "fake",
            "cluster": request.cluster.to_safe_dict(),
            "observability": observability,
            "message": (
                "Placeholder observability install executed against the fake "
                "Kubernetes client."
            ),
        }

    def observability_enable(
        self, request: ObservabilityEnableRequest
    ) -> dict[str, Any]:
        observability = {
            "operation": "enable",
            "namespace": request.cluster.namespace,
            "resources": [
                "servicemonitor/inferops-operator",
                "servicemonitor/inferops-gateway",
                "servicemonitor/inferops-runtimes",
                "grafana-dashboard/inferops-platform",
                "grafana-dashboard/inferops-vllm",
                "grafana-dashboard/inferops-llama-cpp",
            ],
        }
        return {
            "mode": "fake",
            "cluster": request.cluster.to_safe_dict(),
            "observability": observability,
            "message": (
                "Placeholder observability enable executed against the fake "
                "Kubernetes client."
            ),
        }

    def observability_setup(
        self, request: ObservabilitySetupRequest
    ) -> dict[str, Any]:
        stack = self.observability_install(
            ObservabilityInstallRequest(
                cluster=ClusterTarget(
                    namespace=request.monitoring_namespace,
                    context=request.cluster.context,
                    kubeconfig=request.cluster.kubeconfig,
                ),
                release=request.release,
                chart=request.chart,
                chart_version=request.chart_version,
                grafana_admin_password=request.grafana_admin_password,
                skip_repo_update=request.skip_repo_update,
            )
        )["observability"]
        inferops = self.observability_enable(
            ObservabilityEnableRequest(
                cluster=request.cluster,
                charts_dir=request.charts_dir,
            )
        )["observability"]
        observability = {
            "operation": "setup",
            "namespace": request.cluster.namespace,
            "monitoringNamespace": request.monitoring_namespace,
            "stack": stack,
            "inferops": inferops,
            "resources": stack["resources"] + inferops["resources"],
        }
        return {
            "mode": "fake",
            "cluster": request.cluster.to_safe_dict(),
            "observability": observability,
            "message": (
                "Placeholder observability setup executed against the fake "
                "Kubernetes client."
            ),
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
