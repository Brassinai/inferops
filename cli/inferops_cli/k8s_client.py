"""Live Kubernetes client using the official kubernetes Python client."""

from __future__ import annotations

from typing import Any

from .errors import CLIError, NotFoundError
from .kube import (
    CacheDeleteRequest,
    ClusterTarget,
    DeployRequest,
    InstallRequest,
    KubernetesClient,
    LogsRequest,
    NamedRequest,
)


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


class LiveKubernetesClient(KubernetesClient):
    """Live Kubernetes client backed by the official Python client."""

    def __init__(self, cluster: ClusterTarget) -> None:
        self._cluster = cluster
        self._custom_api: Any = None
        self._core_api: Any = None
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

    def deploy(self, request: DeployRequest) -> dict[str, Any]:
        """Create or replace ModelDeployment resources.

        Uses create for new resources and replace (PUT) for existing ones.
        Replace is chosen over patch so the full spec is authoritative and
        stale fields are removed.
        """
        api = self._custom_objects_api
        deployments: list[dict[str, Any]] = []
        for manifest in request.manifests:
            name = manifest["metadata"]["name"]
            namespace = self._cluster.namespace
            body = _clean_manifest(manifest)

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

            deployments.append(
                {
                    "name": name,
                    "namespace": namespace,
                    "action": action,
                    "phase": body["spec"]["activation"]["desiredState"],
                }
            )
        return {"deployments": deployments}

    def activate(self, request: NamedRequest) -> dict[str, Any]:
        """Set desiredState to Active."""
        deployment = self._patch_activation(request.name, self._cluster.namespace, "Active")
        return {
            "deployment": _summarize_deployment(deployment),
        }

    def deactivate(self, request: NamedRequest) -> dict[str, Any]:
        """Set desiredState to Inactive."""
        deployment = self._patch_activation(request.name, self._cluster.namespace, "Inactive")
        return {
            "deployment": _summarize_deployment(deployment),
        }

    def status(self, request: NamedRequest) -> dict[str, Any]:
        """Fetch one ModelDeployment status."""
        deployment = self._get_modeldeployment(request.name, self._cluster.namespace)
        return {
            "deployment": _summarize_deployment(deployment),
        }

    def logs(self, request: LogsRequest) -> dict[str, Any]:
        """Fetch logs from the first runtime pod for a deployment."""
        from kubernetes.client.rest import ApiException

        namespace = self._cluster.namespace
        label_selector = f"app.kubernetes.io/name={request.name}-runtime"
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

        pod_name = pods.items[0].metadata.name
        try:
            log_response = self._core_v1_api.read_namespaced_pod_log(
                name=pod_name,
                namespace=namespace,
                tail_lines=request.tail,
            )
        except ApiException as exc:
            if _is_not_found(exc):
                raise NotFoundError(f"pod {pod_name} not found")
            _raise_cli_error(exc)

        lines = log_response.splitlines() if log_response else []
        return {
            "deployment": {"name": request.name, "namespace": namespace},
            "tail": request.tail,
            "lines": lines,
        }

    def gpu_list(self, cluster: ClusterTarget) -> dict[str, Any]:
        """List nodes and their allocatable GPUs."""
        from kubernetes.client.rest import ApiException

        gpus: list[dict[str, Any]] = []
        try:
            nodes = self._core_v1_api.list_node()
        except ApiException as exc:
            _raise_cli_error(exc)

        for node in nodes.items:
            allocatable = node.status.allocatable or {}
            capacity = node.status.capacity or {}
            gpu_cap = capacity.get("nvidia.com/gpu", "0")
            try:
                gpu_cap_int = int(gpu_cap)
            except (ValueError, TypeError):
                continue
            if gpu_cap_int > 0:
                gpu_alloc = allocatable.get("nvidia.com/gpu", "0")
                gpus.append(
                    {
                        "name": node.metadata.name,
                        "vendor": "nvidia",
                        "product": _gpu_product_from_labels(node.metadata.labels or {}),
                        "status": f"{gpu_alloc}/{gpu_cap} allocatable",
                    }
                )
        return {"gpus": gpus}

    def cache_list(self, cluster: ClusterTarget) -> dict[str, Any]:
        """List ModelCache objects."""
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

        items = resp.get("items", [])
        caches = []
        for item in items:
            status = item.get("status", {})
            spec = item.get("spec", {})
            storage = spec.get("storage", {})
            caches.append(
                {
                    "name": item["metadata"]["name"],
                    "namespace": cluster.namespace,
                    "status": status.get("phase", "Unknown"),
                    "path": storage.get("path", ""),
                    "size": storage.get("size", ""),
                }
            )
        return {"caches": caches}

    def cache_delete(self, request: CacheDeleteRequest) -> dict[str, Any]:
        """Delete one ModelCache object."""
        api = self._custom_objects_api
        # TODO: check if any ModelDeployment references this cache before
        # deleting; refuse unless --force is set.
        try:
            api.delete_namespaced_custom_object(
                group="inference.inferops.dev",
                version="v1alpha1",
                namespace=request.cluster.namespace,
                plural="modelcaches",
                name=request.name,
            )
        except Exception as exc:
            if _is_not_found(exc):
                raise NotFoundError(f"cache not found: {request.name}")
            raise CLIError(f"failed to delete cache: {exc}")
        return {
            "cache": {"name": request.name, "namespace": request.cluster.namespace},
            "force": request.force,
            "deleted": True,
        }

    def install(self, request: InstallRequest) -> dict[str, Any]:
        """Install or upgrade InferOps with the packaged Helm charts."""
        from .helm import HelmInstaller

        return HelmInstaller().install(request)

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
        }

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

    def _patch_activation(self, name: str, namespace: str, desired_state: str) -> dict[str, Any]:
        from kubernetes.client.rest import ApiException

        body = {"spec": {"activation": {"desiredState": desired_state}}}
        try:
            return self._custom_objects_api.patch_namespaced_custom_object(
                group="inference.inferops.dev",
                version="v1alpha1",
                namespace=namespace,
                plural="modeldeployments",
                name=name,
                body=body,
            )
        except ApiException as exc:
            if _is_not_found(exc):
                raise NotFoundError(f"deployment not found: {name}")
            _raise_cli_error(exc)


def _clean_manifest(manifest: dict[str, Any]) -> dict[str, Any]:
    """Return a manifest suitable for create or replace.

    Removes status fields and read-only metadata that should not be sent.
    """
    metadata = dict(manifest.get("metadata", {}))
    for key in ("resourceVersion", "uid", "creationTimestamp", "generation", "managedFields"):
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
    return {
        "name": deployment["metadata"]["name"],
        "namespace": deployment["metadata"]["namespace"],
        "phase": status.get("phase", "Unknown"),
        "runtime": spec.get("runtime", {}).get("ref", ""),
        "model": spec.get("model", {}).get("repo", ""),
    }


def _gpu_product_from_labels(labels: dict[str, str]) -> str:
    """Extract a GPU product name from node labels."""
    for key in ("nvidia.com/gpu.product", "beta.kubernetes.io/instance-type"):
        if key in labels:
            return labels[key]
    return "unknown"
