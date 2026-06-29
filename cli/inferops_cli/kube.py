"""Small Kubernetes client boundary for the CLI."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Protocol

from .errors import CLIError

DEFAULT_NAMESPACE = "default"


@dataclass(frozen=True)
class ClusterTarget:
    """User-selected Kubernetes target settings."""

    namespace: str = DEFAULT_NAMESPACE
    context: str | None = None
    kubeconfig: str | None = None

    def to_safe_dict(self) -> dict[str, str]:
        """Return a log-safe representation of the cluster target."""
        data = {"namespace": self.namespace}
        if self.context:
            data["context"] = self.context
        if self.kubeconfig:
            data["kubeconfigPath"] = self.kubeconfig
        return data


@dataclass(frozen=True)
class DeployRequest:
    """Inputs for a deployment request."""

    cluster: ClusterTarget
    app_path: str
    manifests: list[dict[str, Any]]
    activate: bool = False
    when_full: str | None = None


@dataclass(frozen=True)
class NamedRequest:
    """Inputs for commands that target one named deployment."""

    cluster: ClusterTarget
    name: str


@dataclass(frozen=True)
class LogsRequest:
    """Inputs for a log request."""

    cluster: ClusterTarget
    name: str
    tail: int = 20


@dataclass(frozen=True)
class InstallRequest:
    """Inputs for an installation request."""

    cluster: ClusterTarget
    profile: str
    cache_path: str | None = None


@dataclass(frozen=True)
class CacheDeleteRequest:
    """Inputs for a cache delete request."""

    cluster: ClusterTarget
    name: str
    force: bool = False


class KubernetesClient(Protocol):
    """Small boundary that can be replaced by a real or fake client."""

    def deploy(self, request: DeployRequest) -> dict[str, Any]:
        """Apply one application deployment request."""

    def activate(self, request: NamedRequest) -> dict[str, Any]:
        """Activate one deployment."""

    def deactivate(self, request: NamedRequest) -> dict[str, Any]:
        """Deactivate one deployment."""

    def status(self, request: NamedRequest) -> dict[str, Any]:
        """Fetch deployment status."""

    def logs(self, request: LogsRequest) -> dict[str, Any]:
        """Fetch deployment logs."""

    def gpu_list(self, cluster: ClusterTarget) -> dict[str, Any]:
        """List GPU inventory."""

    def cache_list(self, cluster: ClusterTarget) -> dict[str, Any]:
        """List cache entries."""

    def cache_delete(self, request: CacheDeleteRequest) -> dict[str, Any]:
        """Delete one cache entry."""

    def install(self, request: InstallRequest) -> dict[str, Any]:
        """Install the platform."""

    def delete(self, request: NamedRequest) -> dict[str, Any]:
        """Delete one deployment."""


def build_cluster_target(args: Any) -> ClusterTarget:
    """Build a cluster target from argparse arguments."""
    return ClusterTarget(
        namespace=getattr(args, "namespace", DEFAULT_NAMESPACE),
        context=getattr(args, "context", None),
        kubeconfig=getattr(args, "kubeconfig", None),
    )


def resolve_client(args: Any, client: KubernetesClient | None = None) -> KubernetesClient:
    """Resolve the client for one handler invocation."""
    if client is not None:
        return client
    client_factory = getattr(args, "_client_factory", None)
    if client_factory is not None:
        return client_factory(build_cluster_target(args))
    raise CLIError("real Kubernetes client not implemented yet")
