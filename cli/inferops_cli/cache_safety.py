"""Pure cache-reference checks used by cache list and delete operations."""

from __future__ import annotations

from typing import Any


def cache_references(
    cache: dict[str, Any],
    deployments: list[dict[str, Any]],
    cache_path: str,
) -> list[str]:
    """Return deployments that can be tied to a ModelCache."""
    references = []
    for deployment in deployments:
        deployment_name = deployment.get("metadata", {}).get("name", "")
        if deployment_cache_relationship(cache, deployment, cache_path) == "referenced":
            references.append(deployment_name)
    return sorted(set(filter(None, references)))


def deployment_cache_relationship(
    cache: dict[str, Any],
    deployment: dict[str, Any],
    cache_path: str,
) -> str:
    """Classify a deployment as referenced, ambiguous, or unrelated."""
    cache_name = cache.get("metadata", {}).get("name", "")
    deployment_name = deployment.get("metadata", {}).get("name", "")
    owner_name = (
        cache.get("metadata", {})
        .get("labels", {})
        .get("inferops.dev/modeldeployment", "")
    )
    deployment_status = deployment.get("status", {}).get("cache", {})
    deployment_path = deployment_status.get("path", "")
    if (
        owner_name == deployment_name
        or cache_name in {deployment_name, f"{deployment_name}-cache"}
        or (cache_path and deployment_path == cache_path)
    ):
        return "referenced"

    deployment_spec = deployment.get("spec", {})
    cache_spec = deployment_spec.get("cache", {})
    cache_enabled = bool(cache_spec.get("enabled"))
    if not cache_enabled and not deployment_status.get("state"):
        return "none"

    cache_repo = cache.get("spec", {}).get("modelRepo", "")
    cache_revision = cache.get("spec", {}).get("revision") or "main"
    model_spec = deployment_spec.get("model", {})
    deployment_repo = model_spec.get("repo", "")
    deployment_revision = model_spec.get("revision") or "main"
    if (
        cache_repo
        and deployment_repo == cache_repo
        and deployment_revision == cache_revision
    ):
        return "referenced"
    if not deployment_path and (not cache_repo or not deployment_repo):
        return "ambiguous"
    return "none"


def pod_uses_cache_path(pod: Any, cache_path: str) -> bool:
    """Return whether a non-terminal Pod mounts the exact cache host path."""
    if not cache_path or getattr(pod.status, "phase", None) in ("Succeeded", "Failed"):
        return False
    return any(
        getattr(getattr(volume, "host_path", None), "path", None) == cache_path
        for volume in (getattr(pod.spec, "volumes", None) or [])
    )
