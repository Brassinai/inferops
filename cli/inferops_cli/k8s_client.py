"""Live Kubernetes client using the official kubernetes Python client."""

from __future__ import annotations

from collections.abc import Callable
from concurrent.futures import ThreadPoolExecutor, as_completed
import re
import time
import uuid
from typing import Any

from .cache_safety import (
    cache_references,
    deployment_cache_relationship,
    pod_uses_cache_path,
)
from .cluster_resources import gpu_inventory, gpu_resource_names, is_gpu_resource_name
from .contracts import CacheEntry, CheckStatus, DoctorCheck
from .diagnostic_helpers import (
    cache_root_error as _cache_root_error,
    https_get as _https_get,
    is_device_plugin_daemonset as _is_device_plugin_daemonset,
    node_is_ready as _node_is_ready,
    parse_df_output as _parse_df_output,
    pod_waiting_reason as _pod_waiting_reason,
    with_remediation as _with_remediation,
)
from .errors import CLIError, NotFoundError
from .kube import (
    ActivationRequest,
    CacheDeleteRequest,
    ClusterTarget,
    DeactivationRequest,
    DeployRequest,
    DoctorRequest,
    EndpointAppDeployRequest,
    InstallRequest,
    KubernetesClient,
    LogsRequest,
    NamedRequest,
    StatusRequest,
    UpgradeRequest,
)

CLI_FIELD_MANAGER = "inferops-cli"


def _load_config(cluster: ClusterTarget) -> None:
    """Load kubeconfig for the selected cluster target."""
    from kubernetes import config

    try:
        config.load_kube_config(
            config_file=cluster.kubeconfig,
            context=cluster.context,
        )
    except Exception as exc:
        raise CLIError(f"failed to load kubeconfig: {exc}")


def _is_not_found(exc: Exception) -> bool:
    """Return True if the exception is a Kubernetes 404."""
    return getattr(exc, "status", None) == 404


def _raise_cli_error(exc: Exception) -> None:
    """Wrap a non-404 Kubernetes API exception as a CLIError."""
    status = getattr(exc, "status", None)
    reason = getattr(exc, "reason", str(exc))
    raise CLIError(f"Kubernetes API error {status}: {reason}")


def _raise_modeldeployment_apply_error(exc: Exception, cluster: ClusterTarget) -> None:
    """Render actionable ModelDeployment apply failures."""
    status = getattr(exc, "status", None)
    if status == 404:
        target = f"context '{cluster.context}'" if cluster.context else "the current kube context"
        raise CLIError(
            "failed to apply ModelDeployment: InferOps CRDs are not available in "
            f"{target} namespace '{cluster.namespace}'. Run 'inferops install' for "
            "that context, or check that --context and --namespace point to the "
            "cluster where InferOps is installed."
        )
    _raise_cli_error(exc)


class LiveKubernetesClient(KubernetesClient):
    """Live Kubernetes client backed by the official Python client."""

    def __init__(self, cluster: ClusterTarget) -> None:
        self._cluster = cluster
        self._custom_api: Any = None
        self._core_api: Any = None
        self._apps_api: Any = None
        self._batch_api: Any = None
        self._node_api: Any = None
        self._networking_api: Any = None
        self._discovery_api: Any = None
        _load_config(cluster)

    @property
    def _custom_objects_api(self) -> Any:
        if self._custom_api is None:
            from kubernetes import client

            self._custom_api = client.CustomObjectsApi()
        return self._custom_api

    @property
    def _core_v1_api(self) -> Any:
        if self._core_api is None:
            from kubernetes import client

            self._core_api = client.CoreV1Api()
        return self._core_api

    @property
    def _apps_v1_api(self) -> Any:
        if self._apps_api is None:
            from kubernetes import client

            self._apps_api = client.AppsV1Api()
        return self._apps_api

    @property
    def _node_v1_api(self) -> Any:
        if self._node_api is None:
            from kubernetes import client

            self._node_api = client.NodeV1Api()
        return self._node_api

    @property
    def _batch_v1_api(self) -> Any:
        if self._batch_api is None:
            from kubernetes import client

            self._batch_api = client.BatchV1Api()
        return self._batch_api

    @property
    def _networking_v1_api(self) -> Any:
        if self._networking_api is None:
            from kubernetes import client

            self._networking_api = client.NetworkingV1Api()
        return self._networking_api

    @property
    def _discovery_v1_api(self) -> Any:
        if self._discovery_api is None:
            from kubernetes import client

            self._discovery_api = client.DiscoveryV1Api()
        return self._discovery_api

    def deploy(self, request: DeployRequest) -> dict[str, Any]:
        """Create or replace ModelDeployment resources.

        Uses create for new resources and replace (PUT) for existing ones.
        Replace is chosen over patch so the full spec is authoritative and
        stale fields are removed.
        """
        from kubernetes.client.rest import ApiException

        api = self._custom_objects_api
        deployments: list[dict[str, Any]] = []
        for manifest in request.manifests:
            name = manifest["metadata"]["name"]
            namespace = self._cluster.namespace
            body = _clean_manifest(manifest)

            try:
                exists = self._modeldeployment_exists(name, namespace)
                if exists:
                    api.replace_namespaced_custom_object(
                        group="inference.inferops.dev",
                        version="v1alpha1",
                        namespace=namespace,
                        plural="modeldeployments",
                        name=name,
                        body=body,
                    )
                    action = "replaced"
                else:
                    api.create_namespaced_custom_object(
                        group="inference.inferops.dev",
                        version="v1alpha1",
                        namespace=namespace,
                        plural="modeldeployments",
                        body=body,
                    )
                    action = "created"
            except ApiException as exc:
                _raise_modeldeployment_apply_error(exc, self._cluster)

            deployments.append(
                {
                    "name": name,
                    "namespace": namespace,
                    "action": action,
                    "phase": body["spec"]["activation"]["desiredState"],
                }
            )
        return {"deployments": deployments}

    def deploy_endpoint_app(self, request: EndpointAppDeployRequest) -> dict[str, Any]:
        """Create or replace an SDK endpoint app Deployment and Service."""
        from kubernetes.client.rest import ApiException

        namespace = request.cluster.namespace
        labels = _endpoint_app_labels(request.name)
        deployment = _endpoint_app_deployment_body(request, labels)
        service = _endpoint_app_service_body(request, labels)

        existing_deployment = None
        existing_service = None
        try:
            existing_deployment = self._apps_v1_api.read_namespaced_deployment(
                request.name,
                namespace,
            )
            _ensure_endpoint_app_owned("Deployment", existing_deployment)
            _ensure_endpoint_selector_compatible(existing_deployment, request.name)
        except ApiException as exc:
            if not _is_not_found(exc):
                _raise_cli_error(exc)

        try:
            existing_service = self._core_v1_api.read_namespaced_service(
                request.name,
                namespace,
            )
            _ensure_endpoint_app_owned("Service", existing_service)
        except ApiException as exc:
            if not _is_not_found(exc):
                _raise_cli_error(exc)

        try:
            if existing_service is None:
                self._core_v1_api.create_namespaced_service(
                    namespace=namespace,
                    body=service,
                )
                service_action = "created"
            else:
                service["metadata"]["resourceVersion"] = _metadata_resource_version(
                    _resource_metadata(existing_service)
                )
                _preserve_service_allocated_fields(service, existing_service)
                self._core_v1_api.replace_namespaced_service(
                    name=request.name,
                    namespace=namespace,
                    body=service,
                )
                service_action = "configured"
        except ApiException as exc:
            _raise_cli_error(exc)

        try:
            if existing_deployment is None:
                self._apps_v1_api.create_namespaced_deployment(
                    namespace=namespace,
                    body=deployment,
                )
                deployment_action = "created"
            else:
                _ensure_endpoint_selector_compatible(deployment, request.name)
                deployment["metadata"]["resourceVersion"] = _metadata_resource_version(
                    _resource_metadata(existing_deployment)
                )
                self._apps_v1_api.replace_namespaced_deployment(
                    name=request.name,
                    namespace=namespace,
                    body=deployment,
                )
                deployment_action = "configured"
        except ApiException as exc:
            _raise_cli_error(exc)

        return {
            "endpointApp": {
                "name": request.name,
                "namespace": namespace,
                "image": request.image,
                "replicas": request.replicas,
                "port": request.port,
                "serviceName": request.name,
                "gatewayURL": request.gateway_url,
                "deploymentAction": deployment_action,
                "serviceAction": service_action,
            },
            "cluster": request.cluster.to_safe_dict(),
        }

    def activate(self, request: ActivationRequest) -> dict[str, Any]:
        """Set desiredState to Active and observe the resulting status."""
        deployment = self._patch_activation(
            request.name,
            request.cluster.namespace,
            "Active",
            when_full=request.when_full,
        )
        return self._lifecycle_response(
            deployment,
            operation="activate",
            wait=request.wait,
            timeout_seconds=request.timeout_seconds,
            poll_interval_seconds=request.poll_interval_seconds,
            on_transition=request.on_transition,
        )

    def deactivate(self, request: DeactivationRequest) -> dict[str, Any]:
        """Set desiredState to Inactive and observe the resulting status."""
        deployment = self._patch_activation(
            request.name, request.cluster.namespace, "Inactive"
        )
        return self._lifecycle_response(
            deployment,
            operation="deactivate",
            wait=request.wait,
            timeout_seconds=request.timeout_seconds,
            poll_interval_seconds=request.poll_interval_seconds,
            on_transition=request.on_transition,
        )

    def status(self, request: StatusRequest) -> dict[str, Any]:
        """Fetch one ModelDeployment status."""
        deployment = self._get_modeldeployment(
            request.name, request.cluster.namespace
        )
        if request.watch:
            summary = _summarize_deployment(deployment)
            operation = (
                "activate"
                if summary["desiredState"] == "Active"
                else "deactivate"
            )
            response = self._lifecycle_response(
                deployment,
                operation=operation,
                wait=True,
                timeout_seconds=request.timeout_seconds,
                poll_interval_seconds=request.poll_interval_seconds,
                on_transition=request.on_transition,
            )
            response["operation"] = "status"
            return response
        return {
            "deployment": _summarize_deployment(deployment),
        }

    def models(self, cluster: ClusterTarget) -> dict[str, Any]:
        """List ModelDeployments without returning Secret references."""
        deployments = self._list_modeldeployments(cluster.namespace)
        return {
            "models": sorted(
                (_summarize_deployment(item) for item in deployments),
                key=lambda item: item["name"],
            )
        }

    def endpoints(self, cluster: ClusterTarget) -> dict[str, Any]:
        """List routing-enabled ModelDeployment endpoints."""
        deployments = self._list_modeldeployments(cluster.namespace)
        endpoints: list[dict[str, Any]] = []
        for deployment in deployments:
            spec = deployment.get("spec", {})
            routing = spec.get("routing", {})
            if routing.get("enabled", True) is False:
                continue
            summary = _summarize_deployment(deployment)
            endpoints.append(
                {
                    "name": summary["name"],
                    "namespace": summary["namespace"],
                    "phase": summary["phase"],
                    "ready": _ready_condition(_current_conditions(summary)),
                    "endpoint": summary["endpoint"]
                    or _routing_endpoint(summary["name"], routing),
                    "serviceName": summary["serviceName"]
                    or f"{summary['name']}-runtime",
                }
            )
        return {"endpoints": sorted(endpoints, key=lambda item: item["name"])}

    def logs(self, request: LogsRequest) -> dict[str, Any]:
        """Fetch logs from the first runtime pod for a deployment."""
        from kubernetes.client.rest import ApiException

        namespace = request.cluster.namespace
        deployment = self._get_modeldeployment(request.name, namespace)
        deployment_name = deployment["metadata"]["name"]
        label_selector = f"inferops.dev/modeldeployment={deployment_name}"
        try:
            pods = self._core_v1_api.list_namespaced_pod(
                namespace=namespace,
                label_selector=label_selector,
            )
        except ApiException as exc:
            if _is_not_found(exc):
                raise NotFoundError(f"no runtime pods found for {request.name}")
            _raise_cli_error(exc)

        if not pods or not pods.items:
            raise NotFoundError(f"no runtime pods found for {request.name}")

        pod_name = _select_runtime_pod(pods.items).metadata.name
        try:
            log_response = self._core_v1_api.read_namespaced_pod_log(
                name=pod_name,
                namespace=namespace,
                container="runtime",
                tail_lines=request.tail,
            )
        except ApiException as exc:
            if _is_not_found(exc):
                raise NotFoundError(f"pod {pod_name} not found")
            _raise_cli_error(exc)

        lines = log_response.splitlines() if log_response else []
        return {
            "deployment": {"name": deployment_name, "namespace": namespace},
            "pod": pod_name,
            "tail": request.tail,
            "lines": lines,
        }

    def gpu_list(self, cluster: ClusterTarget) -> dict[str, Any]:
        """List nodes and their allocatable GPUs with occupancy."""
        from kubernetes.client.rest import ApiException

        try:
            nodes = self._core_v1_api.list_node()
        except ApiException as exc:
            _raise_cli_error(exc)

        pods_or_none: list[Any] | None
        try:
            pods_or_none = list(self._core_v1_api.list_pod_for_all_namespaces().items)
        except ApiException as exc:
            if getattr(exc, "status", None) != 403:
                _raise_cli_error(exc)
            pods_or_none = None

        return {
            "gpus": gpu_inventory(list(nodes.items), pods_or_none),
            "occupancyKnown": pods_or_none is not None,
        }

    def cache_list(self, cluster: ClusterTarget) -> dict[str, Any]:
        """List ModelCache objects with referencing deployments."""
        api = self._custom_objects_api
        try:
            resp = api.list_namespaced_custom_object(
                group="inference.inferops.dev",
                version="v1alpha1",
                namespace=cluster.namespace,
                plural="modelcaches",
            )
        except Exception as exc:
            raise CLIError(f"failed to list caches: {exc}")

        deployments: list[dict[str, Any]] = []
        references_known = True
        try:
            dep_resp = api.list_namespaced_custom_object(
                group="inference.inferops.dev",
                version="v1alpha1",
                namespace=cluster.namespace,
                plural="modeldeployments",
            )
            deployments = dep_resp.get("items", [])
        except Exception:
            references_known = False

        items = resp.get("items", [])
        caches: list[dict[str, Any]] = []
        for item in items:
            status = item.get("status", {})
            spec = item.get("spec", {})
            storage = spec.get("storage", {})
            cache_name = item["metadata"]["name"]
            cache_path = status.get("path", storage.get("path", ""))
            cache_size = status.get("size", storage.get("size", ""))

            refs = cache_references(item, deployments, cache_path)

            conditions = status.get("conditions", [])
            issues: list[str] = []
            for cond in conditions:
                if cond.get("status") == "False" and cond.get("type") in (
                    "CacheReady",
                    "DownloadComplete",
                ):
                    reason = cond.get("reason", "")
                    message = cond.get("message", "")
                    # Redact any secret reference from messages
                    if "secret" in message.lower() or "token" in message.lower():
                        message = "credential or storage failure"
                    issues.append(f"{reason}: {message}" if reason else message)

            caches.append(
                CacheEntry(
                    name=cache_name,
                    phase=status.get("phase", "Unknown"),
                    repository=spec.get("modelRepo", ""),
                    revision=status.get("revision", spec.get("revision", "main")),
                    node=status.get("nodeName", storage.get("nodeName", "")),
                    path=cache_path,
                    size=cache_size,
                    last_used=status.get("lastUsedTime", ""),
                    referenced_by=sorted(set(refs)),
                    references_known=references_known,
                    issues=issues,
                ).to_dict()
            )
        return {
            "caches": sorted(caches, key=lambda cache: cache["name"]),
            "referencesKnown": references_known,
        }

    def cache_delete(self, request: CacheDeleteRequest) -> dict[str, Any]:
        """Delete one ModelCache object with reference safety."""
        api = self._custom_objects_api
        namespace = request.cluster.namespace

        try:
            cache_obj = api.get_namespaced_custom_object(
                group="inference.inferops.dev",
                version="v1alpha1",
                namespace=namespace,
                plural="modelcaches",
                name=request.name,
            )
        except Exception as exc:
            if _is_not_found(exc):
                raise NotFoundError(f"cache not found: {request.name}")
            raise CLIError(f"failed to read cache before deletion: {exc}")

        cache_path = cache_obj.get("status", {}).get(
            "path",
            cache_obj.get("spec", {}).get("storage", {}).get("path", ""),
        )
        refs: list[str] = []
        ambiguous_refs: list[str] = []
        deployment_snapshot: dict[str, str] = {}
        try:
            dep_resp = api.list_namespaced_custom_object(
                group="inference.inferops.dev",
                version="v1alpha1",
                namespace=namespace,
                plural="modeldeployments",
            )
            for dep in dep_resp.get("items", []):
                dep_name = dep["metadata"]["name"]
                deployment_snapshot[dep["metadata"].get("uid", dep_name)] = dep[
                    "metadata"
                ].get("resourceVersion", "")
                relationship = deployment_cache_relationship(
                    cache_obj,
                    dep,
                    cache_path,
                )
                if relationship == "referenced":
                    refs.append(dep_name)
                elif relationship == "ambiguous":
                    ambiguous_refs.append(dep_name)
        except Exception as exc:
            raise CLIError(f"cannot verify cache references: {exc}")

        pod_refs: list[str] = []
        pod_snapshot: dict[str, str] = {}
        try:
            pods = self._core_v1_api.list_namespaced_pod(namespace=namespace)
            for pod in pods.items:
                pod_id = pod.metadata.uid or pod.metadata.name
                pod_snapshot[pod_id] = pod.metadata.resource_version or ""
                if pod_uses_cache_path(pod, cache_path):
                    pod_refs.append(pod.metadata.name)
        except Exception as exc:
            raise CLIError(f"cannot verify live cache mounts: {exc}")

        refs = sorted(set(refs))
        ambiguous_refs = sorted(set(ambiguous_refs))
        pod_refs = sorted(set(pod_refs))
        if (refs or ambiguous_refs or pod_refs) and not request.force:
            detail = []
            if refs:
                detail.append(f"referenced by deployments: {', '.join(refs)}")
            if ambiguous_refs:
                detail.append(
                    "cache identity is unavailable for deployments: "
                    + ", ".join(ambiguous_refs)
                )
            if pod_refs:
                detail.append(f"mounted by live Pods: {', '.join(pod_refs)}")
            raise CLIError(
                f"cannot safely delete cache '{request.name}': {'; '.join(detail)}. "
                "Use --force only after confirming those deployments can lose the cache registration."
            )

        if request.force and (refs or ambiguous_refs or pod_refs):
            try:
                api.patch_namespaced_custom_object(
                    group="inference.inferops.dev",
                    version="v1alpha1",
                    namespace=namespace,
                    plural="modelcaches",
                    name=request.name,
                    body={
                        "metadata": {
                            "annotations": {
                                "inferops.dev/cache-delete-requested": "true",
                                "inferops.dev/cache-delete-mode": "forced",
                            }
                        }
                    },
                )
            except Exception as exc:
                if _is_not_found(exc):
                    raise NotFoundError(f"cache not found: {request.name}")
                raise CLIError(f"failed to annotate cache for deletion: {exc}")

        if not request.force:
            self._assert_cache_safety_snapshot(
                namespace,
                deployment_snapshot,
                pod_snapshot,
            )

        try:
            resource_version = cache_obj.get("metadata", {}).get("resourceVersion")
            delete_options = (
                {"preconditions": {"resourceVersion": resource_version}}
                if resource_version
                else {}
            )
            api.delete_namespaced_custom_object(
                group="inference.inferops.dev",
                version="v1alpha1",
                namespace=namespace,
                plural="modelcaches",
                name=request.name,
                body=delete_options,
            )
        except Exception as exc:
            if _is_not_found(exc):
                raise NotFoundError(f"cache not found: {request.name}")
            raise CLIError(f"failed to delete cache: {exc}")
        return {
            "cache": {
                "name": request.name,
                "namespace": namespace,
                "referencedBy": refs,
                "ambiguousReferences": ambiguous_refs,
                "mountedByPods": pod_refs,
            },
            "force": request.force,
            "annotated": request.force and bool(refs or ambiguous_refs or pod_refs),
            "deleted": True,
            "nodeFilesModified": False,
        }

    def _assert_cache_safety_snapshot(
        self,
        namespace: str,
        deployment_snapshot: dict[str, str],
        pod_snapshot: dict[str, str],
    ) -> None:
        deployments = self._custom_objects_api.list_namespaced_custom_object(
            group="inference.inferops.dev",
            version="v1alpha1",
            namespace=namespace,
            plural="modeldeployments",
        )
        current_deployments = {
            item["metadata"].get("uid", item["metadata"]["name"]): item["metadata"].get(
                "resourceVersion", ""
            )
            for item in deployments.get("items", [])
        }
        pods = self._core_v1_api.list_namespaced_pod(namespace=namespace)
        current_pods = {
            (pod.metadata.uid or pod.metadata.name): (
                pod.metadata.resource_version or ""
            )
            for pod in pods.items
        }
        if current_deployments != deployment_snapshot or current_pods != pod_snapshot:
            raise CLIError(
                "cluster state changed while cache deletion was being checked; "
                "retry the command"
            )

    def install(self, request: InstallRequest) -> dict[str, Any]:
        """Install or upgrade InferOps with the packaged Helm charts."""
        from .helm import HelmInstaller

        return HelmInstaller().install(request)

    def upgrade(self, request: UpgradeRequest) -> dict[str, Any]:
        """Upgrade installed InferOps control-plane images."""
        from .helm import HelmInstaller

        return HelmInstaller().upgrade(request)

    def delete(self, request: NamedRequest) -> dict[str, Any]:
        """Delete one ModelDeployment."""
        api = self._custom_objects_api
        deployment = self._get_modeldeployment(request.name, request.cluster.namespace)
        try:
            api.delete_namespaced_custom_object(
                group="inference.inferops.dev",
                version="v1alpha1",
                namespace=request.cluster.namespace,
                plural="modeldeployments",
                name=request.name,
            )
        except Exception as exc:
            if _is_not_found(exc):
                raise NotFoundError(f"deployment not found: {request.name}")
            raise CLIError(f"failed to delete deployment: {exc}")
        return {
            "deployment": _summarize_deployment(deployment),
            "deleted": True,
            "cachePreserved": True,
        }

    def doctor(self, request: DoctorRequest) -> dict[str, Any]:
        """Run diagnostic checks."""
        check_set = set(request.checks) if request.checks else set()
        check_functions = (
            ("kubernetes-api", self._check_kubernetes_api),
            ("device-plugin", self._check_device_plugin),
            ("gpu-capacity", self._check_gpu_capacity),
            ("cache", self._check_cache),
            ("runtime-class", self._check_runtime_class),
            ("gateway", self._check_gateway),
            ("tailscale", self._check_tailscale),
        )
        checks: list[DoctorCheck] = []
        for check_id, check_function in check_functions:
            if check_set and check_id not in check_set:
                continue
            try:
                checks.append(_with_remediation(check_function()))
            except Exception as exc:
                checks.append(
                    DoctorCheck(
                        id=check_id,
                        status=CheckStatus.FAIL,
                        message=f"diagnostic check failed unexpectedly: {exc}",
                        remediation="retry with --check and inspect Kubernetes API permissions",
                    )
                )
        return {"checks": [check.to_dict() for check in checks]}

    def _check_kubernetes_api(self) -> DoctorCheck:
        """Check Kubernetes API access and namespace existence."""
        from kubernetes.client.rest import ApiException

        try:
            self._core_v1_api.read_namespace(self._cluster.namespace)
        except ApiException as exc:
            if getattr(exc, "status", None) == 404:
                return DoctorCheck(
                    id="kubernetes-api",
                    status=CheckStatus.FAIL,
                    message=f"namespace '{self._cluster.namespace}' not found",
                    remediation="create the namespace or select an existing one with --namespace",
                )
            return DoctorCheck(
                id="kubernetes-api",
                status=CheckStatus.FAIL,
                message=f"cannot access Kubernetes API: {exc.reason}",
                remediation="verify kubeconfig, context, and cluster connectivity",
            )
        except Exception as exc:
            return DoctorCheck(
                id="kubernetes-api",
                status=CheckStatus.FAIL,
                message=f"cannot access Kubernetes API: {exc}",
                remediation="verify kubeconfig, context, and cluster connectivity",
            )
        return DoctorCheck(
            id="kubernetes-api",
            status=CheckStatus.PASS,
            message=f"Kubernetes API accessible; namespace '{self._cluster.namespace}' exists",
        )

    def _check_device_plugin(self) -> DoctorCheck:
        """Inspect GPU extended resources and plugin DaemonSet/Pod readiness."""
        from kubernetes.client.rest import ApiException

        try:
            nodes = list(self._core_v1_api.list_node().items)
        except ApiException as exc:
            return DoctorCheck(
                id="device-plugin",
                status=CheckStatus.FAIL,
                message=f"cannot list nodes: {exc.reason}",
                remediation="grant permission to list nodes and verify cluster connectivity",
            )

        resources = gpu_resource_names(nodes)
        gpu_nodes = [
            node
            for node in nodes
            if any(name in (node.status.capacity or {}) for name in resources)
        ]
        try:
            config = self._diagnostics_config(required=False) or {}
        except ApiException:
            config = {}
        gpu_required = config.get("gpu.required", "false").lower() == "true"
        if not gpu_nodes:
            return DoctorCheck(
                id="device-plugin",
                status=CheckStatus.FAIL if gpu_required else CheckStatus.WARN,
                message="no nodes advertise GPU extended resources",
                remediation="install the NVIDIA k8s-device-plugin or your GPU vendor's device plugin",
            )

        not_ready_nodes = [
            node.metadata.name for node in gpu_nodes if not _node_is_ready(node)
        ]
        if not_ready_nodes:
            return DoctorCheck(
                id="device-plugin",
                status=CheckStatus.FAIL,
                message="GPU nodes are not Ready: "
                + ", ".join(sorted(not_ready_nodes)),
                remediation="inspect node conditions and device-plugin events",
            )

        plugin_sets = []
        try:
            daemon_sets = self._apps_v1_api.list_daemon_set_for_all_namespaces()
            for daemon_set in daemon_sets.items:
                if _is_device_plugin_daemonset(daemon_set):
                    desired = daemon_set.status.desired_number_scheduled or 0
                    ready = daemon_set.status.number_ready or 0
                    plugin_sets.append(
                        {
                            "namespace": daemon_set.metadata.namespace,
                            "name": daemon_set.metadata.name,
                            "desired": desired,
                            "ready": ready,
                        }
                    )
        except ApiException as exc:
            return DoctorCheck(
                id="device-plugin",
                status=CheckStatus.WARN,
                message=(
                    "GPU resources are advertised but device-plugin "
                    f"DaemonSets cannot be inspected: {exc.reason}"
                ),
                remediation="grant permission to list DaemonSets for a full health check",
            )

        degraded = [
            item
            for item in plugin_sets
            if item["desired"] == 0 or item["ready"] < item["desired"]
        ]
        if degraded:
            names = [
                f"{item['namespace']}/{item['name']} ({item['ready']}/{item['desired']} ready)"
                for item in degraded
            ]
            return DoctorCheck(
                id="device-plugin",
                status=CheckStatus.FAIL,
                message="device-plugin DaemonSet is degraded: " + ", ".join(names),
                remediation="inspect the DaemonSet pods, host drivers, and container runtime",
            )

        if not plugin_sets:
            return DoctorCheck(
                id="device-plugin",
                status=CheckStatus.WARN,
                message=(
                    "GPU resources are advertised, but no device-plugin "
                    "DaemonSet could be identified"
                ),
                remediation=(
                    "verify the vendor device-plugin workload and confirm its "
                    "DaemonSet labels or image include device-plugin"
                ),
            )

        return DoctorCheck(
            id="device-plugin",
            status=CheckStatus.PASS,
            message=f"{len(gpu_nodes)} Ready GPU node(s) advertise {', '.join(sorted(resources))}",
            details={"daemonSets": plugin_sets},
        )

    def _check_gpu_capacity(self) -> DoctorCheck:
        """Sum effective GPU requests from scheduled, non-terminal Pods."""
        from kubernetes.client.rest import ApiException

        try:
            nodes = list(self._core_v1_api.list_node().items)
        except ApiException as exc:
            return DoctorCheck(
                id="gpu-capacity",
                status=CheckStatus.FAIL,
                message=f"cannot list nodes: {exc.reason}",
            )

        if not gpu_resource_names(nodes):
            return DoctorCheck(
                id="gpu-capacity",
                status=CheckStatus.WARN,
                message="no GPU resources advertised by nodes",
            )

        try:
            pods = list(self._core_v1_api.list_pod_for_all_namespaces().items)
        except ApiException as exc:
            if getattr(exc, "status", None) == 403:
                inventory = gpu_inventory(nodes, None)
                return DoctorCheck(
                    id="gpu-capacity",
                    status=CheckStatus.WARN,
                    message="cannot list pods across namespaces; occupied GPU capacity is unknown",
                    details={"inventory": inventory},
                    remediation="grant broader Pod list permissions or run with elevated RBAC",
                )
            return DoctorCheck(
                id="gpu-capacity",
                status=CheckStatus.FAIL,
                message=f"cannot list pods: {exc.reason}",
            )

        inventory = gpu_inventory(nodes, pods)
        total_cap = sum(item["capacity"] for item in inventory)
        total_alloc = sum(item["allocatable"] for item in inventory)
        occupied = sum(item["occupied"] or 0 for item in inventory)
        available = sum(item["available"] or 0 for item in inventory)
        return DoctorCheck(
            id="gpu-capacity",
            status=CheckStatus.PASS,
            message=f"GPU capacity: {total_cap} total, {total_alloc} allocatable, {occupied} occupied, {available} available",
            details={
                "totalCapacity": total_cap,
                "totalAllocatable": total_alloc,
                "occupied": occupied,
                "available": available,
                "inventory": inventory,
            },
        )

    def _check_cache(self) -> DoctorCheck:
        """Read installation config and verify cache path via node probes."""
        from kubernetes.client.rest import ApiException

        try:
            config_map = self._diagnostics_config(required=True)
        except ApiException as exc:
            return DoctorCheck(
                id="cache",
                status=CheckStatus.FAIL,
                message=f"cannot read diagnostics ConfigMap: {exc.reason}",
                remediation="grant permission to list ConfigMaps in the InferOps namespace",
            )

        if config_map is None:
            return DoctorCheck(
                id="cache",
                status=CheckStatus.FAIL,
                message="diagnostics ConfigMap not found; operator may not be installed",
                remediation="run 'inferops install' to install the operator",
            )

        cache_root = config_map.get("cache.root", "")
        validation_error = _cache_root_error(cache_root)
        if validation_error:
            return DoctorCheck(
                id="cache",
                status=CheckStatus.FAIL,
                message=f"invalid cache root in diagnostics ConfigMap: {validation_error}",
            )
        probe_image = config_map.get("cache.probeImage", "")
        if not re.search(r"@sha256:[0-9a-f]{64}$", probe_image):
            return DoctorCheck(
                id="cache",
                status=CheckStatus.FAIL,
                message="cache.probeImage must be pinned by sha256 digest",
            )

        # Identify GPU nodes and nodes referenced by ModelCache objects
        probe_nodes: set[str] = set()
        ready_nodes: set[str] = set()
        try:
            nodes = self._core_v1_api.list_node()
            for node in nodes.items:
                if _node_is_ready(node) and not getattr(
                    getattr(node, "spec", None),
                    "unschedulable",
                    False,
                ):
                    ready_nodes.add(node.metadata.name)
                capacity = node.status.capacity or {}
                for name in capacity:
                    if is_gpu_resource_name(name):
                        probe_nodes.add(node.metadata.name)
                        break
        except ApiException as exc:
            return DoctorCheck(
                id="cache",
                status=CheckStatus.FAIL,
                message=f"cannot discover cache probe nodes: {exc.reason}",
                remediation="grant permission to list nodes",
            )

        try:
            caches = self._custom_objects_api.list_namespaced_custom_object(
                group="inference.inferops.dev",
                version="v1alpha1",
                namespace=self._cluster.namespace,
                plural="modelcaches",
            )
            for item in caches.get("items", []):
                node_name = item.get("status", {}).get(
                    "nodeName",
                    item.get("spec", {}).get("storage", {}).get("nodeName", ""),
                )
                if node_name:
                    probe_nodes.add(node_name)
        except ApiException as exc:
            return DoctorCheck(
                id="cache",
                status=CheckStatus.FAIL,
                message=f"cannot list ModelCache placements: {exc.reason}",
                remediation="grant permission to list ModelCache resources",
            )

        if not probe_nodes:
            probe_nodes.update(ready_nodes)
        if not probe_nodes:
            return DoctorCheck(
                id="cache",
                status=CheckStatus.WARN,
                message=f"cache root is '{cache_root}' but no Ready nodes are available to probe",
                remediation="restore at least one schedulable node and rerun doctor",
            )

        probe_results: dict[str, dict[str, Any]] = {}
        with ThreadPoolExecutor(max_workers=min(4, len(probe_nodes))) as executor:
            futures = {
                executor.submit(
                    self._run_cache_probe,
                    node_name,
                    cache_root,
                    probe_image,
                ): node_name
                for node_name in sorted(probe_nodes)
            }
            for future in as_completed(futures):
                node_name = futures[future]
                try:
                    probe_results[node_name] = future.result()
                except Exception as exc:
                    probe_results[node_name] = {
                        "status": "error",
                        "message": f"probe failed unexpectedly: {exc}",
                    }

        probe_failures = [
            f"{node_name}: {result.get('message', 'probe failed')}"
            for node_name, result in sorted(probe_results.items())
            if result.get("status") != "ok"
        ]

        if probe_failures:
            return DoctorCheck(
                id="cache",
                status=CheckStatus.FAIL,
                message=f"cache root '{cache_root}' probe issues on {len(probe_failures)} node(s)",
                details={"failures": probe_failures, "probes": probe_results},
                remediation="verify the path exists, permissions, and disk space on the reported nodes",
            )

        return DoctorCheck(
            id="cache",
            status=CheckStatus.PASS,
            message=f"cache root '{cache_root}' reachable on {len(probe_nodes)} probed node(s)",
            details={"probes": probe_results},
        )

    def _diagnostics_config(self, required: bool) -> dict[str, str] | None:
        config_maps = self._core_v1_api.list_namespaced_config_map(
            namespace=self._cluster.namespace,
            label_selector="inferops.dev/component=diagnostics",
        )
        if not config_maps.items:
            return None
        if len(config_maps.items) > 1 and required:
            names = sorted(item.metadata.name for item in config_maps.items)
            raise CLIError(
                "multiple InferOps diagnostics ConfigMaps found: " + ", ".join(names)
            )
        return config_maps.items[0].data or {}

    def _run_cache_probe(
        self,
        node_name: str,
        cache_root: str,
        probe_image: str,
    ) -> dict[str, Any]:
        """Create a deadline- and TTL-bound cache probe Job."""
        from kubernetes.client.rest import ApiException

        job_name = f"inferops-cache-probe-{uuid.uuid4().hex[:12]}"
        labels = {
            "app.kubernetes.io/part-of": "inferops",
            "inferops.dev/component": "cache-probe",
        }
        body = {
            "apiVersion": "batch/v1",
            "kind": "Job",
            "metadata": {
                "name": job_name,
                "namespace": self._cluster.namespace,
                "labels": labels,
            },
            "spec": {
                "activeDeadlineSeconds": 45,
                "backoffLimit": 0,
                "ttlSecondsAfterFinished": 60,
                "template": {
                    "metadata": {"labels": labels},
                    "spec": {
                        "restartPolicy": "Never",
                        "nodeName": node_name,
                        "automountServiceAccountToken": False,
                        "containers": [
                            {
                                "name": "probe",
                                "image": probe_image,
                                "command": ["df", "-Pk", "/cache"],
                                "volumeMounts": [
                                    {
                                        "name": "cache",
                                        "mountPath": "/cache",
                                        "readOnly": True,
                                    }
                                ],
                                "securityContext": {
                                    "runAsNonRoot": True,
                                    "runAsUser": 65534,
                                    "allowPrivilegeEscalation": False,
                                    "readOnlyRootFilesystem": True,
                                    "capabilities": {"drop": ["ALL"]},
                                },
                            }
                        ],
                        "volumes": [
                            {
                                "name": "cache",
                                "hostPath": {
                                    "path": cache_root,
                                    "type": "Directory",
                                },
                            }
                        ],
                        "securityContext": {
                            "runAsNonRoot": True,
                            "seccompProfile": {"type": "RuntimeDefault"},
                        },
                    },
                },
            },
        }

        created = False
        result: dict[str, Any] = {"status": "error", "message": "probe did not run"}
        try:
            self._batch_v1_api.create_namespaced_job(
                namespace=self._cluster.namespace,
                body=body,
            )
            created = True
        except ApiException as exc:
            return {
                "status": "error",
                "message": f"cannot create probe Job: {exc.reason}",
            }

        try:
            pod = None
            for _ in range(45):
                pods = self._core_v1_api.list_namespaced_pod(
                    namespace=self._cluster.namespace,
                    label_selector=f"job-name={job_name}",
                )
                if not pods.items:
                    time.sleep(1)
                    continue
                pod = pods.items[0]
                if pod.status.phase in ("Succeeded", "Failed"):
                    break
                waiting_reason = _pod_waiting_reason(pod)
                if waiting_reason:
                    result = {
                        "status": "error",
                        "message": f"probe pod cannot start: {waiting_reason}",
                    }
                    break
                time.sleep(1)
            else:
                result = {"status": "error", "message": "probe Job timed out"}

            if pod is None:
                result = {"status": "error", "message": "probe Job created no Pod"}
            elif pod.status.phase == "Failed":
                result = {"status": "error", "message": "probe Job failed"}
            elif pod.status.phase == "Succeeded":
                logs = self._core_v1_api.read_namespaced_pod_log(
                    name=pod.metadata.name,
                    namespace=self._cluster.namespace,
                )
                result = _parse_df_output(logs or "")
                result["node"] = node_name
                result["path"] = cache_root
        except ApiException as exc:
            result = {"status": "error", "message": str(exc.reason)}
        finally:
            if created:
                cleanup_error = self._delete_probe_job(job_name)
                if cleanup_error:
                    result["status"] = "error"
                    result["message"] = (
                        f"{result.get('message', 'probe completed')}; {cleanup_error}"
                    )
        return result

    def _delete_probe_job(self, job_name: str) -> str:
        """Best-effort deletion of a probe Job and its dependent Pod."""
        from kubernetes.client.rest import ApiException

        try:
            self._batch_v1_api.delete_namespaced_job(
                name=job_name,
                namespace=self._cluster.namespace,
                body={},
                propagation_policy="Background",
            )
        except ApiException as exc:
            if getattr(exc, "status", None) != 404:
                return f"cannot delete probe Job: {exc.reason}"
        return ""

    def _check_runtime_class(self) -> DoctorCheck:
        """Validate RuntimeClasses referenced by relevant workloads."""
        from kubernetes.client.rest import ApiException

        referenced_classes: set[str] = set()
        partial_inspection = False
        try:
            pods = self._core_v1_api.list_namespaced_pod(
                namespace=self._cluster.namespace,
            )
            for pod in pods.items:
                runtime_class = getattr(pod.spec, "runtime_class_name", None)
                if runtime_class:
                    referenced_classes.add(runtime_class)
        except ApiException as exc:
            return DoctorCheck(
                id="runtime-class",
                status=CheckStatus.FAIL,
                message=f"cannot inspect workload RuntimeClass references: {exc.reason}",
                remediation="grant permission to list Pods in the InferOps namespace",
            )

        try:
            deployments = self._apps_v1_api.list_namespaced_deployment(
                namespace=self._cluster.namespace,
            )
            for deployment in deployments.items:
                runtime_class = deployment.spec.template.spec.runtime_class_name
                if runtime_class:
                    referenced_classes.add(runtime_class)
        except ApiException as exc:
            return DoctorCheck(
                id="runtime-class",
                status=CheckStatus.FAIL,
                message=f"cannot inspect Deployment RuntimeClass references: {exc.reason}",
                remediation="grant permission to list Deployments in the InferOps namespace",
            )

        try:
            daemon_sets = self._apps_v1_api.list_daemon_set_for_all_namespaces()
            for daemon_set in daemon_sets.items:
                if not _is_device_plugin_daemonset(daemon_set):
                    continue
                runtime_class = daemon_set.spec.template.spec.runtime_class_name
                if runtime_class:
                    referenced_classes.add(runtime_class)
        except ApiException:
            partial_inspection = True

        try:
            rcs = self._node_v1_api.list_runtime_class()
            available = {rc.metadata.name for rc in rcs.items}
        except ApiException as exc:
            if getattr(exc, "status", None) == 404:
                return DoctorCheck(
                    id="runtime-class",
                    status=CheckStatus.FAIL if referenced_classes else CheckStatus.PASS,
                    message=(
                        "RuntimeClass API is unavailable but workloads reference it"
                        if referenced_classes
                        else "RuntimeClass API is unavailable and no workload references it"
                    ),
                    remediation=(
                        "enable the RuntimeClass API or remove runtimeClassName "
                        "from affected workloads"
                        if referenced_classes
                        else ""
                    ),
                )
            return DoctorCheck(
                id="runtime-class",
                status=CheckStatus.FAIL,
                message=f"cannot list RuntimeClasses: {exc.reason}",
                remediation="grant permission to list node.k8s.io RuntimeClasses",
            )

        missing = sorted(referenced_classes - available)
        if missing:
            return DoctorCheck(
                id="runtime-class",
                status=CheckStatus.FAIL,
                message="referenced RuntimeClasses are missing: " + ", ".join(missing),
                remediation="create the referenced RuntimeClass or remove runtimeClassName from the workload",
            )

        if partial_inspection:
            return DoctorCheck(
                id="runtime-class",
                status=CheckStatus.WARN,
                message=(
                    "InferOps RuntimeClass references are valid, but "
                    "device-plugin DaemonSets could not be inspected"
                ),
                remediation="grant permission to list DaemonSets across namespaces",
            )

        if not referenced_classes:
            return DoctorCheck(
                id="runtime-class",
                status=CheckStatus.PASS,
                message="no InferOps workload references a RuntimeClass",
            )

        return DoctorCheck(
            id="runtime-class",
            status=CheckStatus.PASS,
            message="referenced RuntimeClasses exist: "
            + ", ".join(sorted(referenced_classes)),
        )

    def _check_gateway(self) -> DoctorCheck:
        """Check gateway Deployment readiness and /readyz endpoint."""
        from kubernetes.client.rest import ApiException

        namespace = self._cluster.namespace
        try:
            deployments = self._apps_v1_api.list_namespaced_deployment(
                namespace=namespace,
                label_selector="app.kubernetes.io/instance=inferops-gateway",
            )
        except ApiException as exc:
            return DoctorCheck(
                id="gateway",
                status=CheckStatus.FAIL,
                message=f"cannot list gateway Deployments: {exc.reason}",
            )

        if len(deployments.items) != 1:
            return DoctorCheck(
                id="gateway",
                status=CheckStatus.FAIL,
                message=(
                    f"expected one gateway Deployment in namespace '{namespace}', "
                    f"found {len(deployments.items)}"
                ),
                remediation="install one inferops-gateway release or remove stale releases",
            )
        deploy = deployments.items[0]
        ready_replicas = deploy.status.ready_replicas or 0
        desired_replicas = deploy.spec.replicas or 0
        if desired_replicas == 0 or ready_replicas < desired_replicas:
            return DoctorCheck(
                id="gateway",
                status=CheckStatus.FAIL,
                message=f"gateway Deployment has {ready_replicas}/{desired_replicas} ready replicas",
                remediation="check gateway pod logs and events for crash loops or image pull failures",
            )

        try:
            services = self._core_v1_api.list_namespaced_service(
                namespace=namespace,
                label_selector="app.kubernetes.io/instance=inferops-gateway",
            )
        except ApiException as exc:
            return DoctorCheck(
                id="gateway",
                status=CheckStatus.FAIL,
                message=f"cannot list gateway Services: {exc.reason}",
            )

        if len(services.items) != 1:
            return DoctorCheck(
                id="gateway",
                status=CheckStatus.FAIL,
                message=f"expected one gateway Service, found {len(services.items)}",
            )
        service = services.items[0]
        service_name = service.metadata.name
        try:
            slices = self._discovery_v1_api.list_namespaced_endpoint_slice(
                namespace=namespace,
                label_selector=f"kubernetes.io/service-name={service_name}",
            )
        except ApiException as exc:
            return DoctorCheck(
                id="gateway",
                status=CheckStatus.FAIL,
                message=f"cannot inspect gateway endpoints: {exc.reason}",
            )
        ready_endpoints = sum(
            1
            for endpoint_slice in slices.items
            for endpoint in (endpoint_slice.endpoints or [])
            if getattr(endpoint.conditions, "ready", None) is True
        )
        if ready_endpoints == 0:
            return DoctorCheck(
                id="gateway",
                status=CheckStatus.FAIL,
                message="gateway Service has no ready endpoints",
                remediation="check the Service selector and gateway Pod readiness",
            )

        port = service.spec.ports[0]
        proxy_port = port.name or str(port.port)
        proxy_name = f"http:{service_name}:{proxy_port}"
        try:
            self._core_v1_api.connect_get_namespaced_service_proxy_with_path(
                name=proxy_name,
                namespace=namespace,
                path="readyz",
                _request_timeout=(5, 10),
            )
        except ApiException as exc:
            if getattr(exc, "status", None) == 403:
                return DoctorCheck(
                    id="gateway",
                    status=CheckStatus.WARN,
                    message=(
                        f"gateway has {ready_endpoints} ready endpoint(s), "
                        "but service proxy access is forbidden"
                    ),
                    remediation="grant get access to services/proxy to verify /readyz",
                )
            return DoctorCheck(
                id="gateway",
                status=CheckStatus.FAIL,
                message=f"gateway /readyz is unavailable: {exc.reason}",
                remediation="check gateway logs and the Service port mapping",
            )
        return DoctorCheck(
            id="gateway",
            status=CheckStatus.PASS,
            message=(
                f"gateway ready with {ready_replicas} replica(s), "
                f"{ready_endpoints} endpoint(s), and /readyz responding"
            ),
        )

    def _check_tailscale(self) -> DoctorCheck:
        """If enabled, validate Tailscale IngressClass, Ingress, and reachability."""
        from kubernetes.client.rest import ApiException

        namespace = self._cluster.namespace
        try:
            ingresses = self._networking_v1_api.list_namespaced_ingress(
                namespace=namespace,
                label_selector="app.kubernetes.io/instance=inferops-gateway",
            )
        except ApiException as exc:
            return DoctorCheck(
                id="tailscale",
                status=CheckStatus.FAIL,
                message=f"cannot read Tailscale ingress: {exc.reason}",
            )

        tailscale_ingresses = [
            ingress
            for ingress in ingresses.items
            if ingress.spec.ingress_class_name == "tailscale"
        ]
        if not tailscale_ingresses:
            return DoctorCheck(
                id="tailscale",
                status=CheckStatus.PASS,
                message="Tailscale ingress not configured; check skipped",
            )
        if len(tailscale_ingresses) > 1:
            return DoctorCheck(
                id="tailscale",
                status=CheckStatus.FAIL,
                message=f"multiple Tailscale ingresses found: {len(tailscale_ingresses)}",
                remediation="remove stale gateway Tailscale ingresses",
            )
        ingress = tailscale_ingresses[0]

        try:
            self._networking_v1_api.read_ingress_class(name="tailscale")
        except ApiException as exc:
            if getattr(exc, "status", None) == 404:
                return DoctorCheck(
                    id="tailscale",
                    status=CheckStatus.FAIL,
                    message="Tailscale IngressClass not found",
                    remediation="install the Tailscale Kubernetes Operator",
                )
            return DoctorCheck(
                id="tailscale",
                status=CheckStatus.WARN,
                message=f"cannot verify Tailscale IngressClass: {exc.reason}",
            )

        assigned = (
            ingress.status.load_balancer.ingress
            if ingress.status and ingress.status.load_balancer
            else None
        ) or []
        if not assigned:
            return DoctorCheck(
                id="tailscale",
                status=CheckStatus.WARN,
                message="Tailscale ingress has not received a load balancer status yet",
                remediation="wait for the Tailscale operator to assign a hostname",
            )

        hostname = next(
            (item.hostname or item.ip for item in assigned if item.hostname or item.ip),
            "",
        )
        if not hostname and ingress.spec.tls:
            hostname = next(
                (
                    host
                    for tls in ingress.spec.tls
                    for host in (tls.hosts or [])
                    if host
                ),
                "",
            )
        if not hostname:
            return DoctorCheck(
                id="tailscale",
                status=CheckStatus.FAIL,
                message="Tailscale ingress status does not contain a reachable hostname",
            )

        try:
            _https_get(f"https://{hostname}/readyz", timeout=5)
        except Exception as exc:
            return DoctorCheck(
                id="tailscale",
                status=CheckStatus.FAIL,
                message=f"Tailscale endpoint '{hostname}' is not reachable: {exc}",
                remediation="join this machine to the tailnet and inspect the Tailscale operator",
            )

        return DoctorCheck(
            id="tailscale",
            status=CheckStatus.PASS,
            message=f"Tailscale ingress and /readyz reachable at '{hostname}'",
        )

    def _modeldeployment_exists(self, name: str, namespace: str) -> bool:
        from kubernetes.client.rest import ApiException

        try:
            self._custom_objects_api.get_namespaced_custom_object(
                group="inference.inferops.dev",
                version="v1alpha1",
                namespace=namespace,
                plural="modeldeployments",
                name=name,
            )
            return True
        except ApiException as exc:
            if _is_not_found(exc):
                return False
            _raise_cli_error(exc)

    def _list_modeldeployments(self, namespace: str) -> list[dict[str, Any]]:
        from kubernetes.client.rest import ApiException

        try:
            response = self._custom_objects_api.list_namespaced_custom_object(
                group="inference.inferops.dev",
                version="v1alpha1",
                namespace=namespace,
                plural="modeldeployments",
            )
        except ApiException as exc:
            _raise_cli_error(exc)
        return list(response.get("items", []))

    def _get_modeldeployment(self, name: str, namespace: str) -> dict[str, Any]:
        from kubernetes.client.rest import ApiException

        try:
            return self._custom_objects_api.get_namespaced_custom_object(
                group="inference.inferops.dev",
                version="v1alpha1",
                namespace=namespace,
                plural="modeldeployments",
                name=name,
            )
        except ApiException as exc:
            if _is_not_found(exc):
                raise NotFoundError(f"deployment not found: {name}")
            _raise_cli_error(exc)

    def _patch_activation(
        self,
        name: str,
        namespace: str,
        desired_state: str,
        when_full: str | None = None,
    ) -> dict[str, Any]:
        from kubernetes.client.rest import ApiException

        activation = {"desiredState": desired_state}
        if when_full is not None:
            activation["whenFull"] = when_full
        body = {"spec": {"activation": activation}}
        try:
            return self._custom_objects_api.patch_namespaced_custom_object(
                group="inference.inferops.dev",
                version="v1alpha1",
                namespace=namespace,
                plural="modeldeployments",
                name=name,
                body=body,
                field_manager=CLI_FIELD_MANAGER,
            )
        except ApiException as exc:
            if _is_not_found(exc):
                raise NotFoundError(f"deployment not found: {name}")
            _raise_cli_error(exc)

    def _lifecycle_response(
        self,
        deployment: dict[str, Any],
        *,
        operation: str,
        wait: bool,
        timeout_seconds: float,
        poll_interval_seconds: float,
        on_transition: Callable[[dict[str, Any]], None] | None = None,
    ) -> dict[str, Any]:
        summary = _summarize_deployment(deployment)
        response: dict[str, Any] = {
            "deployment": summary,
            "operation": operation,
            "outcome": "requested",
            "transitions": [],
        }
        if not wait:
            return response

        name = summary["name"]
        namespace = summary["namespace"]
        target_generation = deployment.get("metadata", {}).get("generation", 0)
        deadline = time.monotonic() + timeout_seconds
        current = deployment
        transitions: list[dict[str, Any]] = []
        previous_transition: tuple[str, str, str] | None = None

        while True:
            summary = _summarize_deployment(current)
            fresh = _status_is_fresh(current, target_generation)
            if fresh:
                transition = _status_transition(summary)
                transition_key = (
                    transition["phase"],
                    transition.get("reason", ""),
                    transition.get("message", ""),
                )
                if transition_key != previous_transition:
                    transitions.append(transition)
                    previous_transition = transition_key
                    if on_transition is not None:
                        on_transition(transition)

                outcome = _lifecycle_outcome(operation, summary)
                if outcome is not None:
                    return {
                        "deployment": summary,
                        "operation": operation,
                        "outcome": outcome,
                        "transitions": transitions,
                    }

            if time.monotonic() >= deadline:
                return {
                    "deployment": summary,
                    "operation": operation,
                    "outcome": "timeout",
                    "transitions": transitions,
                }

            time.sleep(poll_interval_seconds)
            current = self._get_modeldeployment(name, namespace)


def _clean_manifest(manifest: dict[str, Any]) -> dict[str, Any]:
    """Return a manifest suitable for create or replace.

    Removes status fields and read-only metadata that should not be sent.
    """
    metadata = dict(manifest.get("metadata", {}))
    for key in (
        "resourceVersion",
        "uid",
        "creationTimestamp",
        "generation",
        "managedFields",
    ):
        metadata.pop(key, None)
    cleaned: dict[str, Any] = {
        "apiVersion": manifest["apiVersion"],
        "kind": manifest["kind"],
        "metadata": metadata,
        "spec": manifest["spec"],
    }
    return cleaned


def _summarize_deployment(deployment: dict[str, Any]) -> dict[str, Any]:
    """Build a CLI-safe summary of a ModelDeployment."""
    status = deployment.get("status", {})
    spec = deployment.get("spec", {})
    metadata = deployment.get("metadata", {})
    activation = spec.get("activation", {})
    conditions = [
        {
            key: condition[key]
            for key in (
                "type",
                "status",
                "observedGeneration",
                "lastTransitionTime",
                "reason",
                "message",
            )
            if key in condition
        }
        for condition in status.get("conditions", [])
    ]
    summary = {
        "name": metadata["name"],
        "namespace": metadata.get("namespace", "default"),
        "phase": status.get("phase", "Unknown"),
        "desiredState": activation.get("desiredState", "Inactive"),
        "whenFull": activation.get("whenFull", "Queue"),
        "runtime": spec.get("runtime", {}).get("ref", ""),
        "model": spec.get("model", {}).get("repo", ""),
        "endpoint": status.get("endpoint", ""),
        "serviceName": status.get("serviceName", ""),
        "assignedNode": status.get("assignedNode", ""),
        "assignedGPUs": list(status.get("assignedGPUs", [])),
        "cache": {
            key: value
            for key, value in status.get("cache", {}).items()
            if key in {"state", "nodeName", "path"}
        },
        "replicas": {
            key: value
            for key, value in status.get("replicas", {}).items()
            if key in {"desired", "ready"}
        },
        "scaling": {
            key: value
            for key, value in status.get("scaling", {}).items()
            if key
            in {
                "desiredReplicas",
                "pendingRequests",
                "runningRequests",
                "lastActivityTime",
                "capacityLimited",
                "reason",
                "message",
            }
        },
        "modelLoaded": bool(status.get("model", {}).get("loaded", False)),
        "observedGeneration": status.get("observedGeneration", 0),
        "generation": metadata.get("generation", 0),
        "conditions": conditions,
    }
    if status.get("drainStartedAt"):
        summary["drainStartedAt"] = status["drainStartedAt"]
    replacement = status.get("replacement")
    if isinstance(replacement, dict) and replacement:
        replacement_summary = {
            key: value
            for key, value in replacement.items()
            if key in {"phase", "requestGeneration", "startedAt", "message"}
        }
        for reference_key in ("target", "requestedBy"):
            reference = replacement.get(reference_key)
            if isinstance(reference, dict):
                replacement_summary[reference_key] = {
                    key: value
                    for key, value in reference.items()
                    if key in {"namespace", "name", "uid"}
                }
        summary["replacement"] = replacement_summary
    return summary


def _status_is_fresh(
    deployment: dict[str, Any], target_generation: int | str
) -> bool:
    """Return whether status reflects the activation patch generation."""
    metadata_generation = deployment.get("metadata", {}).get(
        "generation", target_generation
    )
    observed_generation = deployment.get("status", {}).get(
        "observedGeneration", 0
    )
    try:
        required = max(int(target_generation or 0), int(metadata_generation or 0))
        return required == 0 or int(observed_generation or 0) >= required
    except (TypeError, ValueError):
        return False


def _lifecycle_outcome(
    operation: str, summary: dict[str, Any]
) -> str | None:
    phase = summary["phase"]
    expected_state = "Active" if operation == "activate" else "Inactive"
    if summary["desiredState"] != expected_state:
        return "superseded"
    if phase == "Failed":
        return (
            "rejected"
            if _is_rejected(_current_conditions(summary))
            else "failed"
        )

    if operation == "activate":
        if phase == "Active":
            return "active"
        if (
            phase in {"WaitingForCapacity", "WaitingForGPU"}
            and summary["whenFull"] == "Queue"
        ):
            return "waiting"
        return None

    if (
        summary["desiredState"] == "Inactive"
        and phase in {"Pending", "Cached"}
    ):
        return "inactive"
    return None


def _status_transition(summary: dict[str, Any]) -> dict[str, Any]:
    condition = _actionable_condition(_current_conditions(summary))
    transition: dict[str, Any] = {
        "phase": summary["phase"],
        "observedGeneration": summary["observedGeneration"],
    }
    if condition:
        if condition.get("reason"):
            transition["reason"] = condition["reason"]
        if condition.get("message"):
            transition["message"] = condition["message"]
    return transition


def _current_conditions(summary: dict[str, Any]) -> list[dict[str, Any]]:
    generation = summary.get("generation", 0)
    current: list[dict[str, Any]] = []
    for condition in summary["conditions"]:
        observed = condition.get("observedGeneration")
        if observed is None:
            current.append(condition)
            continue
        try:
            if int(observed) >= int(generation or 0):
                current.append(condition)
        except (TypeError, ValueError):
            continue
    return current


def _actionable_condition(conditions: list[dict[str, Any]]) -> dict[str, Any]:
    for condition in reversed(conditions):
        if condition.get("status") == "False" and (
            condition.get("reason") or condition.get("message")
        ):
            return condition
    for condition in reversed(conditions):
        if condition.get("type") == "Ready":
            return condition
    return {}


def _is_rejected(conditions: list[dict[str, Any]]) -> bool:
    for condition in conditions:
        reason_and_message = " ".join(
            str(condition.get(key, "")) for key in ("reason", "message")
        ).lower()
        if "reject" in reason_and_message:
            return True
    return False


def _ready_condition(conditions: list[dict[str, Any]]) -> bool | None:
    for condition in conditions:
        if condition.get("type") != "Ready":
            continue
        if condition.get("status") == "True":
            return True
        if condition.get("status") == "False":
            return False
    return None


def _routing_endpoint(name: str, routing: dict[str, Any]) -> str:
    path = (routing.get("path") or f"/models/{name}").rstrip("/")
    if routing.get("openAICompatible", True) and not path.endswith("/v1"):
        return f"{path}/v1"
    return path


def _endpoint_app_labels(name: str) -> dict[str, str]:
    return {
        "app.kubernetes.io/name": name,
        "app.kubernetes.io/part-of": "inferops",
        "app.kubernetes.io/managed-by": "inferops-cli",
        "app.kubernetes.io/component": "endpoint-app",
        "inferops.dev/endpoint-app": "true",
    }


def _ensure_endpoint_app_owned(kind: str, resource: Any) -> None:
    metadata = _resource_metadata(resource)
    labels = _metadata_labels(metadata)
    if (
        labels.get("app.kubernetes.io/managed-by") != "inferops-cli"
        or labels.get("inferops.dev/endpoint-app") != "true"
    ):
        name = _metadata_name(metadata)
        raise CLIError(
            f"{kind} {name!r} already exists but is not managed by inferops "
            "endpoint deployment; choose --name or delete/rename the existing resource"
        )


def _ensure_endpoint_selector_compatible(resource: Any, name: str) -> None:
    expected = _endpoint_app_selector(name)
    if _deployment_selector(resource) != expected:
        raise CLIError(
            f"Deployment {name!r} has an incompatible selector and cannot be updated in place"
        )


def _resource_metadata(resource: Any) -> Any:
    if isinstance(resource, dict):
        return resource.get("metadata", {})
    return getattr(resource, "metadata", None)


def _metadata_labels(metadata: Any) -> dict[str, str]:
    if isinstance(metadata, dict):
        return dict(metadata.get("labels") or {})
    return dict(getattr(metadata, "labels", None) or {})


def _metadata_name(metadata: Any) -> str:
    if isinstance(metadata, dict):
        return str(metadata.get("name", ""))
    return str(getattr(metadata, "name", "") or "")


def _metadata_resource_version(metadata: Any) -> str:
    if isinstance(metadata, dict):
        return str(metadata.get("resourceVersion", ""))
    return str(getattr(metadata, "resource_version", "") or "")


def _deployment_selector(resource: Any) -> dict[str, str]:
    if isinstance(resource, dict):
        return dict(
            resource.get("spec", {})
            .get("selector", {})
            .get("matchLabels", {})
        )
    spec = getattr(resource, "spec", None)
    selector = getattr(spec, "selector", None)
    return dict(getattr(selector, "match_labels", None) or {})


def _preserve_service_allocated_fields(service: dict[str, Any], existing: Any) -> None:
    existing_spec = _service_spec(existing)
    spec = service.setdefault("spec", {})
    for key in (
        "clusterIP",
        "clusterIPs",
        "ipFamilies",
        "ipFamilyPolicy",
        "healthCheckNodePort",
    ):
        value = existing_spec.get(key)
        if value not in (None, "", []):
            spec[key] = value


def _service_spec(resource: Any) -> dict[str, Any]:
    if isinstance(resource, dict):
        return dict(resource.get("spec") or {})
    spec = getattr(resource, "spec", None)
    if spec is None:
        return {}
    data: dict[str, Any] = {}
    for attr, key in (
        ("cluster_ip", "clusterIP"),
        ("cluster_ips", "clusterIPs"),
        ("ip_families", "ipFamilies"),
        ("ip_family_policy", "ipFamilyPolicy"),
        ("health_check_node_port", "healthCheckNodePort"),
    ):
        value = getattr(spec, attr, None)
        if value not in (None, "", []):
            data[key] = value
    return data


def _endpoint_app_deployment_body(
    request: EndpointAppDeployRequest,
    labels: dict[str, str],
) -> dict[str, Any]:
    env = [
        {"name": "INFEROPS_GATEWAY_URL", "value": request.gateway_url},
        {"name": "PORT", "value": str(request.port)},
    ]
    env.extend(
        {"name": name, "value": value}
        for name, value in sorted(request.env.items())
    )
    return {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "metadata": {
            "name": request.name,
            "namespace": request.cluster.namespace,
            "labels": labels,
        },
        "spec": {
            "replicas": request.replicas,
            "selector": {"matchLabels": _endpoint_app_selector(request.name)},
            "template": {
                "metadata": {"labels": labels},
                "spec": {
                    "automountServiceAccountToken": False,
                    "enableServiceLinks": False,
                    "securityContext": {
                        "runAsNonRoot": True,
                        "runAsUser": 10001,
                        "runAsGroup": 10001,
                        "fsGroup": 10001,
                    },
                    "containers": [
                        {
                            "name": "endpoint",
                            "image": request.image,
                            "imagePullPolicy": _endpoint_image_pull_policy(request.image),
                            "command": ["inferops"],
                            "args": [
                                "serve",
                                request.container_app_path,
                                "--host",
                                "0.0.0.0",
                                "--port",
                                str(request.port),
                            ],
                            "ports": [
                                {
                                    "name": "http",
                                    "containerPort": request.port,
                                    "protocol": "TCP",
                                }
                            ],
                            "env": env,
                            "readinessProbe": {
                                "httpGet": {"path": "/health", "port": "http"},
                                "periodSeconds": 10,
                                "timeoutSeconds": 5,
                                "failureThreshold": 3,
                            },
                            "livenessProbe": {
                                "httpGet": {"path": "/health", "port": "http"},
                                "periodSeconds": 10,
                                "timeoutSeconds": 5,
                                "failureThreshold": 3,
                            },
                            "resources": {
                                "requests": {
                                    "cpu": "100m",
                                    "memory": "128Mi",
                                },
                                "limits": {
                                    "cpu": "500m",
                                    "memory": "512Mi",
                                },
                            },
                            "securityContext": {
                                "allowPrivilegeEscalation": False,
                                "capabilities": {"drop": ["ALL"]},
                                "readOnlyRootFilesystem": False,
                                "seccompProfile": {"type": "RuntimeDefault"},
                            },
                        }
                    ],
                },
            },
        },
    }


def _endpoint_app_service_body(
    request: EndpointAppDeployRequest,
    labels: dict[str, str],
) -> dict[str, Any]:
    return {
        "apiVersion": "v1",
        "kind": "Service",
        "metadata": {
            "name": request.name,
            "namespace": request.cluster.namespace,
            "labels": labels,
        },
        "spec": {
            "type": "ClusterIP",
            "selector": _endpoint_app_selector(request.name),
            "ports": [
                {
                    "name": "http",
                    "port": request.port,
                    "targetPort": "http",
                    "protocol": "TCP",
                }
            ],
        },
    }


def _endpoint_app_selector(name: str) -> dict[str, str]:
    return {
        "app.kubernetes.io/name": name,
        "app.kubernetes.io/component": "endpoint-app",
    }


def _endpoint_image_pull_policy(image: str) -> str:
    lowered = image.lower()
    if lowered.endswith(":latest") or ":" not in image.rsplit("/", 1)[-1]:
        return "Always"
    return "IfNotPresent"


def _select_runtime_pod(pods: list[Any]) -> Any:
    """Choose a live runtime Pod deterministically during rollouts."""
    non_terminating = [
        pod
        for pod in pods
        if getattr(pod.metadata, "deletion_timestamp", None) is None
    ]
    candidates = non_terminating or pods
    return max(
        candidates,
        key=lambda pod: (
            getattr(getattr(pod, "status", None), "phase", "") == "Running",
            str(getattr(pod.metadata, "creation_timestamp", "") or ""),
            pod.metadata.name,
        ),
    )
