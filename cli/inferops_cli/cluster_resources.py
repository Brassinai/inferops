"""Kubernetes resource accounting helpers used by operational commands."""

from __future__ import annotations

from typing import Any

from .contracts import GPUInventory


def is_gpu_resource_name(resource_name: str) -> bool:
    """Return whether an extended resource name represents a whole GPU."""
    normalized = resource_name.lower()
    return (
        normalized.endswith("/gpu")
        or normalized.startswith("gpu.")
        or normalized.endswith("/i915")
        or normalized.endswith("/gaudi")
    )


def gpu_resource_names(nodes: list[Any]) -> set[str]:
    """Collect GPU extended resource names advertised by nodes."""
    names: set[str] = set()
    for node in nodes:
        for resource_name in node.status.capacity or {}:
            if is_gpu_resource_name(resource_name):
                names.add(resource_name)
    return names


def gpu_inventory(nodes: list[Any], pods: list[Any] | None) -> list[dict[str, Any]]:
    """Build deterministic per-node GPU inventory.

    A ``None`` pod list means occupancy could not be observed. In that case
    occupied and available remain unknown rather than being reported as zero.
    """
    resource_names = gpu_resource_names(nodes)
    occupancy = _gpu_occupancy(pods or [], resource_names) if pods is not None else {}
    occupancy_known = pods is not None
    result: list[dict[str, Any]] = []

    for node in nodes:
        capacity = node.status.capacity or {}
        allocatable = node.status.allocatable or {}
        for resource_name in sorted(resource_names):
            cap = _quantity_to_int(capacity.get(resource_name))
            if cap <= 0:
                continue
            alloc = _quantity_to_int(allocatable.get(resource_name))
            occupied = (
                occupancy.get((node.metadata.name, resource_name), 0)
                if occupancy_known
                else None
            )
            available = max(0, alloc - occupied) if occupied is not None else None
            result.append(
                GPUInventory(
                    node=node.metadata.name,
                    resource_name=resource_name,
                    vendor=gpu_vendor(resource_name),
                    product=gpu_product(node.metadata.labels or {}, resource_name),
                    capacity=cap,
                    allocatable=alloc,
                    occupied=occupied,
                    available=available,
                ).to_dict()
            )

    return sorted(result, key=lambda item: (item["node"], item["resourceName"]))


def gpu_vendor(resource_name: str) -> str:
    """Infer the common vendor name from an extended resource name."""
    normalized = resource_name.lower()
    for vendor in ("nvidia", "amd", "intel", "habana"):
        if vendor in normalized:
            return vendor
    if "gaudi" in normalized:
        return "habana"
    return "unknown"


def gpu_product(labels: dict[str, str], resource_name: str) -> str:
    """Extract a vendor product label without assuming NVIDIA."""
    candidates = (
        "nvidia.com/gpu.product",
        "amd.com/gpu.product",
        "gpu.intel.com/product",
        "habana.ai/product",
        "beta.kubernetes.io/instance-type",
        "node.kubernetes.io/instance-type",
    )
    for key in candidates:
        if key in labels:
            return labels[key]
    return gpu_vendor(resource_name)


def _gpu_occupancy(
    pods: list[Any],
    resource_names: set[str],
) -> dict[tuple[str, str], int]:
    occupied: dict[tuple[str, str], int] = {}
    for pod in pods:
        if getattr(pod.status, "phase", None) in ("Succeeded", "Failed"):
            continue
        node_name = getattr(pod.spec, "node_name", None)
        if not node_name:
            continue
        effective = _effective_pod_gpu_requests(pod, resource_names)
        for resource_name, count in effective.items():
            key = (node_name, resource_name)
            occupied[key] = occupied.get(key, 0) + count
    return occupied


def _effective_pod_gpu_requests(
    pod: Any,
    resource_names: set[str],
) -> dict[str, int]:
    """Calculate effective requests including restartable init sidecars."""
    regular: dict[str, int] = {name: 0 for name in resource_names}
    for container in getattr(pod.spec, "containers", None) or []:
        requests = _container_requests(container)
        for name in resource_names:
            regular[name] += _quantity_to_int(requests.get(name))

    restartable_init: dict[str, int] = {name: 0 for name in resource_names}
    init_peak: dict[str, int] = {name: 0 for name in resource_names}
    for container in getattr(pod.spec, "init_containers", None) or []:
        requests = _container_requests(container)
        for name in resource_names:
            request = _quantity_to_int(requests.get(name))
            if getattr(container, "restart_policy", None) == "Always":
                restartable_init[name] += request
                init_peak[name] = max(init_peak[name], restartable_init[name])
            else:
                init_peak[name] = max(
                    init_peak[name],
                    restartable_init[name] + request,
                )

    return {
        name: max(regular[name] + restartable_init[name], init_peak[name])
        for name in resource_names
    }


def _container_requests(container: Any) -> dict[str, Any]:
    resources = getattr(container, "resources", None)
    if resources is None:
        return {}
    requests = getattr(resources, "requests", None) or {}
    limits = getattr(resources, "limits", None) or {}
    return {
        key: requests[key] if key in requests else limits[key]
        for key in set(requests) | set(limits)
    }


def _quantity_to_int(value: Any) -> int:
    if value is None:
        return 0
    try:
        return int(str(value))
    except (TypeError, ValueError):
        return 0
