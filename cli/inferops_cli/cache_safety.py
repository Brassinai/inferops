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
    cache_metadata = cache.get("metadata", {})
    cache_labels = cache_metadata.get("labels", {})
    cache_annotations = cache_metadata.get("annotations", {})
    deployment_name = deployment.get("metadata", {}).get("name", "")
    owner_name = (
        cache_labels.get("inferops.dev/modeldeployment", "")
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

    if owner_name or deployment_path:
        return "none"

    if _shared_cache_matches_deployment(
        cache,
        deployment,
        cache_path,
        cache_labels,
        cache_annotations,
    ):
        return "referenced"

    if _is_shared_cache(cache_labels, cache_annotations, cache_path):
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


def _shared_cache_matches_deployment(
    cache: dict[str, Any],
    deployment: dict[str, Any],
    cache_path: str,
    cache_labels: dict[str, Any],
    cache_annotations: dict[str, Any],
) -> bool:
    if not _is_shared_cache(cache_labels, cache_annotations, cache_path):
        return False
    deployment_spec = deployment.get("spec", {})
    if not deployment_spec.get("cache", {}).get("enabled", True):
        return False
    cache_spec = cache.get("spec", {})
    cache_storage = cache_spec.get("storage", {})
    model_spec = deployment_spec.get("model", {})
    if cache_spec.get("modelRepo", "") != model_spec.get("repo", ""):
        return False
    if (cache_spec.get("revision") or "main") != (model_spec.get("revision") or "main"):
        return False
    if (cache_storage.get("type") or "nodeLocal") != (
        deployment_spec.get("cache", {}).get("type") or "nodeLocal"
    ):
        return False
    cache_root = cache_annotations.get("inferops.dev/cache-root", "")
    deployment_root = deployment_spec.get("cache", {}).get("path", "")
    if cache_root and deployment_root and cache_root != deployment_root:
        return False
    cache_source = cache_annotations.get("inferops.dev/cache-source") or "huggingface"
    deployment_source = model_spec.get("source") or "huggingface"
    if cache_source != deployment_source:
        return False
    deployment_secret = (
        deployment_spec.get("secrets", {}).get("huggingFaceTokenSecretName", "")
    )
    if cache_spec.get("secretRef", "") != deployment_secret:
        return False
    cache_namespace = cache.get("metadata", {}).get("namespace", "")
    deployment_namespace = deployment.get("metadata", {}).get("namespace", cache_namespace)
    return not cache_namespace or cache_namespace == deployment_namespace


def _is_shared_cache(
    cache_labels: dict[str, Any],
    cache_annotations: dict[str, Any],
    cache_path: str,
) -> bool:
    return (
        cache_labels.get("inferops.dev/cache-mode") == "shared"
        or bool(cache_annotations.get("inferops.dev/cache-root"))
        or "/artifacts/" in cache_path
    )


def pod_uses_cache_path(pod: Any, cache_path: str) -> bool:
    """Return whether a non-terminal Pod mounts the exact cache host path."""
    if not cache_path or getattr(pod.status, "phase", None) in ("Succeeded", "Failed"):
        return False
    return any(
        getattr(getattr(volume, "host_path", None), "path", None) == cache_path
        for volume in (getattr(pod.spec, "volumes", None) or [])
    )
