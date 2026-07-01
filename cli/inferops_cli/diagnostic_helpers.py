"""Pure helpers shared by Kubernetes diagnostic checks."""

from __future__ import annotations

from pathlib import PurePosixPath
import posixpath
from typing import Any

from .contracts import CheckStatus, DoctorCheck
from .errors import CLIError


def node_is_ready(node: Any) -> bool:
    return any(
        condition.type == "Ready" and condition.status == "True"
        for condition in (node.status.conditions or [])
    )


def with_remediation(check: DoctorCheck) -> DoctorCheck:
    """Ensure every non-passing result contains actionable guidance."""
    if check.status == CheckStatus.PASS or check.remediation:
        return check
    defaults = {
        "kubernetes-api": "verify kubeconfig, context, namespace, and API permissions",
        "device-plugin": "inspect the vendor device-plugin workload and node conditions",
        "gpu-capacity": "inspect node GPU resources and cluster-wide Pod list permissions",
        "cache": "inspect the diagnostics ConfigMap, node cache path, and probe Job events",
        "runtime-class": "inspect referenced RuntimeClasses and node.k8s.io API permissions",
        "gateway": "inspect the gateway Deployment, Service, EndpointSlices, and logs",
        "tailscale": (
            "inspect the Tailscale operator, IngressClass, Ingress status, "
            "and tailnet access"
        ),
    }
    return DoctorCheck(
        id=check.id,
        status=check.status,
        message=check.message,
        details=check.details,
        remediation=defaults.get(check.id, "inspect the reported Kubernetes resources"),
    )


def is_device_plugin_daemonset(daemon_set: Any) -> bool:
    images = [
        (container.image or "").lower()
        for container in (daemon_set.spec.template.spec.containers or [])
    ]
    labels = getattr(daemon_set.metadata, "labels", None) or {}
    identity = " ".join(
        [
            (daemon_set.metadata.name or "").lower(),
            *(f"{key}={value}".lower() for key, value in labels.items()),
            *images,
        ]
    )
    return "device-plugin" in identity or "device_plugin" in identity


def cache_root_error(cache_root: str) -> str:
    if not cache_root:
        return "cache.root is empty"
    if len(cache_root) > 4096 or any(ord(character) < 32 for character in cache_root):
        return "path contains control characters or exceeds 4096 characters"
    path = PurePosixPath(cache_root)
    if not path.is_absolute():
        return "path must be absolute"
    if path == PurePosixPath("/"):
        return "path must not be the filesystem root"
    if posixpath.normpath(cache_root) != cache_root:
        return "path must be normalized"
    return ""


def pod_waiting_reason(pod: Any) -> str:
    terminal_reasons = {
        "CreateContainerConfigError",
        "CreateContainerError",
        "ErrImagePull",
        "ImagePullBackOff",
        "InvalidImageName",
        "RunContainerError",
    }
    for status in getattr(pod.status, "container_statuses", None) or []:
        waiting = getattr(getattr(status, "state", None), "waiting", None)
        if waiting and waiting.reason in terminal_reasons:
            return f"{waiting.reason}: {waiting.message or 'no detail'}"
    return ""


def parse_df_output(output: str) -> dict[str, Any]:
    lines = [line.strip() for line in output.splitlines() if line.strip()]
    if len(lines) < 2:
        return {"status": "error", "message": "df returned no filesystem data"}
    fields = lines[-1].split()
    if len(fields) < 6:
        return {"status": "error", "message": f"cannot parse df output: {lines[-1]}"}
    try:
        total_bytes = int(fields[1]) * 1024
        used_bytes = int(fields[2]) * 1024
        free_bytes = int(fields[3]) * 1024
    except ValueError:
        return {
            "status": "error",
            "message": f"cannot parse df quantities: {lines[-1]}",
        }
    return {
        "status": "ok",
        "message": f"{free_bytes} bytes free",
        "totalBytes": total_bytes,
        "usedBytes": used_bytes,
        "freeBytes": free_bytes,
        "capacity": fields[4],
    }


def https_get(url: str, timeout: int) -> None:
    from urllib.request import Request, urlopen

    request = Request(url, method="GET", headers={"User-Agent": "inferops-doctor"})
    with urlopen(request, timeout=timeout) as response:
        if response.status != 200:
            raise CLIError(f"HTTP {response.status}")
        response.read(1024)
