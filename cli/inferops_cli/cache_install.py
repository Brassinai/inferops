"""Install-time cache node preparation."""

from __future__ import annotations

from collections.abc import Callable, Sequence
import json
from pathlib import Path
import re
import subprocess
import tempfile
from typing import Any
import uuid

from .errors import CLIError
from .kube import InstallRequest

CACHE_CAPACITY_ANNOTATION = "inferops.dev/cache-capacity"
CACHE_PROBE_IMAGE = (
    "busybox:1.36.1@sha256:"
    "73aaf090f3d85aa34ee199857f03fa3a95c8ede2ffd4cc2cdb5b94e566b11662"
)
CACHE_CAPACITY = re.compile(
    r"^[1-9][0-9]*(?:m|Ki|Mi|Gi|Ti|Pi|Ei|k|M|G|T|P|E)?$"
)
NODE_NAME = re.compile(
    r"^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?(?:\.[a-z0-9](?:[-a-z0-9]*[a-z0-9])?)*$"
)

CommandRunner = Callable[[Sequence[str]], subprocess.CompletedProcess[str]]


class CacheNodePreparer:
    """Prepare node-local cache annotations before Helm installation."""

    def __init__(self, runner: CommandRunner) -> None:
        self._runner = runner

    def prepare(
        self,
        request: InstallRequest,
        cache_root: str,
    ) -> list[dict[str, str]]:
        """Validate selected cache nodes, probe the host path, and annotate nodes."""
        annotations = _resolve_cache_annotations(request)
        if not request.cache_capacity and not annotations:
            return []

        nodes = _load_nodes(self._runner, request)
        targets = _select_cache_annotation_targets(request, nodes, annotations)
        _ensure_namespace(self._runner, request)

        for node_name in targets:
            _validate_cache_path_on_node(
                self._runner,
                request,
                node_name,
                cache_root,
            )
        return [
            _annotate_cache_node(self._runner, request, node_name, capacity)
            for node_name, capacity in targets.items()
        ]


def validate_cache_install_options(request: InstallRequest) -> None:
    """Validate install-time cache annotation options without touching the cluster."""
    _resolve_cache_annotations(request)


def _resolve_cache_annotations(request: InstallRequest) -> dict[str, str]:
    if request.cache_node and request.cache_node_selector:
        raise CLIError("--cache-node cannot be combined with --cache-node-selector")
    if request.cache_node and not request.cache_capacity:
        raise CLIError("--cache-node requires --cache-capacity")
    if request.cache_node_selector and not request.cache_capacity:
        raise CLIError("--cache-node-selector requires --cache-capacity")
    if request.cache_capacity and request.cache_node_capacities:
        raise CLIError(
            "--cache-capacity cannot be combined with --cache-node-capacity"
        )

    annotations: dict[str, str] = {}
    if request.cache_capacity:
        _validate_cache_capacity("--cache-capacity", request.cache_capacity)
        if request.cache_node:
            _validate_node_name("--cache-node", request.cache_node)
            annotations[request.cache_node] = request.cache_capacity
        return annotations

    for item in request.cache_node_capacities:
        node_name, separator, capacity = item.partition("=")
        if not separator or not node_name or not capacity:
            raise CLIError(
                "--cache-node-capacity must use NODE=CAPACITY, for example node-a=100Gi"
            )
        _validate_node_name("--cache-node-capacity node", node_name)
        _validate_cache_capacity("--cache-node-capacity capacity", capacity)
        if node_name in annotations:
            raise CLIError(f"duplicate --cache-node-capacity for node '{node_name}'")
        annotations[node_name] = capacity
    return annotations


def _load_nodes(
    runner: CommandRunner,
    request: InstallRequest,
) -> list[dict[str, Any]]:
    command = _kubectl_command(request)
    command.extend(("get", "nodes", "--output", "json"))
    if request.cache_node_selector:
        command.extend(("--selector", request.cache_node_selector))
    try:
        completed = runner(command)
    except FileNotFoundError as exc:
        raise CLIError(
            "kubectl executable not found; install kubectl before annotating cache nodes"
        ) from exc
    except subprocess.CalledProcessError as exc:
        detail = (exc.stderr or exc.stdout or "unknown kubectl error").strip()
        raise CLIError(f"failed to discover cache nodes: {detail}") from exc

    try:
        payload = json.loads(completed.stdout or "{}")
    except json.JSONDecodeError as exc:
        raise CLIError("failed to parse 'kubectl get nodes' JSON output") from exc
    items = payload.get("items", [])
    if not isinstance(items, list):
        raise CLIError("failed to parse 'kubectl get nodes' JSON output")
    return [item for item in items if isinstance(item, dict)]


def _select_cache_annotation_targets(
    request: InstallRequest,
    nodes: list[dict[str, Any]],
    annotations: dict[str, str],
) -> dict[str, str]:
    by_name = {
        node.get("metadata", {}).get("name", ""): node
        for node in nodes
        if node.get("metadata", {}).get("name")
    }
    if annotations:
        for node_name in annotations:
            node = by_name.get(node_name)
            if node is None:
                raise CLIError(f"cache node '{node_name}' was not found")
            _require_cache_eligible_node(request, node)
        return annotations

    if not request.cache_capacity:
        return {}
    matching = [
        node
        for node in nodes
        if _cache_eligibility_error(request, node) is None
    ]
    if request.cache_node_selector:
        ineligible = [
            node.get("metadata", {}).get("name", "<unknown>")
            for node in nodes
            if _cache_eligibility_error(request, node) is not None
        ]
        if ineligible:
            raise CLIError(
                "--cache-node-selector matched node(s) that are not eligible "
                "cache nodes: " + ", ".join(sorted(ineligible))
            )
        if not matching:
            raise CLIError(
                "--cache-node-selector matched no Ready schedulable cache nodes: "
                f"{request.cache_node_selector}"
            )
        return {
            node["metadata"]["name"]: request.cache_capacity
            for node in matching
        }

    if not matching:
        rejected = _cache_rejection_details(request, nodes)
        if rejected:
            raise CLIError(
                "--cache-capacity found no Ready schedulable cache nodes. "
                "Rejected nodes: " + "; ".join(rejected)
            )
        raise CLIError(
            "--cache-capacity found no Ready schedulable cache nodes. "
            "Check node readiness or pass --cache-node after choosing a node."
        )
    if len(matching) > 1:
        names = ", ".join(sorted(node["metadata"]["name"] for node in matching))
        raise CLIError(
            "--cache-capacity is ambiguous because multiple Ready schedulable "
            f"cache nodes are eligible: {names}. Pass --cache-node, "
            "--cache-node-selector, or repeated --cache-node-capacity values."
        )
    return {matching[0]["metadata"]["name"]: request.cache_capacity}


def _require_cache_eligible_node(request: InstallRequest, node: dict[str, Any]) -> None:
    error = _cache_eligibility_error(request, node)
    if error:
        node_name = node.get("metadata", {}).get("name", "<unknown>")
        raise CLIError(f"cache node '{node_name}' is not eligible: {error}")


def _cache_rejection_details(
    request: InstallRequest,
    nodes: list[dict[str, Any]],
) -> list[str]:
    details: list[str] = []
    for node in nodes:
        node_name = node.get("metadata", {}).get("name", "<unknown>")
        error = _cache_eligibility_error(request, node)
        if error:
            details.append(f"{node_name}: {error}")
    return sorted(details)


def _cache_eligibility_error(
    request: InstallRequest,
    node: dict[str, Any],
) -> str | None:
    if node.get("spec", {}).get("unschedulable", False):
        return "node is unschedulable"
    conditions = node.get("status", {}).get("conditions", [])
    ready = any(
        condition.get("type") == "Ready" and condition.get("status") == "True"
        for condition in conditions
        if isinstance(condition, dict)
    )
    if not ready:
        return "node is not Ready"
    if request.compute_profile == "nvidia-gpu" and not _has_positive_resource(
        node,
        "nvidia.com/gpu",
    ):
        return "node does not advertise nvidia.com/gpu"
    return None


def _has_positive_resource(node: dict[str, Any], resource_name: str) -> bool:
    for field in ("allocatable", "capacity"):
        value = node.get("status", {}).get(field, {}).get(resource_name)
        if value and not str(value).startswith("0"):
            return True
    return False


def _ensure_namespace(runner: CommandRunner, request: InstallRequest) -> None:
    get_command = _kubectl_command(request)
    get_command.extend(("get", "namespace", request.cluster.namespace))
    try:
        runner(get_command)
        return
    except FileNotFoundError as exc:
        raise CLIError(
            "kubectl executable not found; install kubectl before probing cache nodes"
        ) from exc
    except subprocess.CalledProcessError as exc:
        detail = (exc.stderr or exc.stdout or "").strip()
        if "not found" not in detail.lower():
            raise CLIError(
                f"failed to inspect namespace '{request.cluster.namespace}': "
                f"{detail or 'unknown kubectl error'}"
            ) from exc

    create_command = _kubectl_command(request)
    create_command.extend(("create", "namespace", request.cluster.namespace))
    try:
        runner(create_command)
    except subprocess.CalledProcessError as exc:
        detail = (exc.stderr or exc.stdout or "").strip()
        if (
            "alreadyexists" not in detail.lower()
            and "already exists" not in detail.lower()
        ):
            raise CLIError(
                f"failed to create namespace '{request.cluster.namespace}' "
                f"for cache probes: {detail or 'unknown kubectl error'}"
            ) from exc


def _validate_cache_path_on_node(
    runner: CommandRunner,
    request: InstallRequest,
    node_name: str,
    cache_root: str,
) -> None:
    pod_name = _cache_probe_pod_name(node_name)
    manifest_path = _write_cache_probe_manifest(
        request=request,
        pod_name=pod_name,
        node_name=node_name,
        cache_root=cache_root,
    )
    create_command = _kubectl_command(request)
    create_command.extend(("create", "--filename", str(manifest_path)))
    wait_command = _kubectl_command(request)
    wait_command.extend(
        (
            "wait",
            "--namespace",
            request.cluster.namespace,
            "--for=jsonpath={.status.phase}=Succeeded",
            f"pod/{pod_name}",
            "--timeout=60s",
        )
    )
    delete_command = _kubectl_command(request)
    delete_command.extend(
        (
            "delete",
            "pod",
            pod_name,
            "--namespace",
            request.cluster.namespace,
            "--ignore-not-found=true",
        )
    )
    try:
        runner(create_command)
        runner(wait_command)
    except FileNotFoundError as exc:
        raise CLIError(
            "kubectl executable not found; install kubectl before probing cache nodes"
        ) from exc
    except subprocess.CalledProcessError as exc:
        detail = (exc.stderr or exc.stdout or "unknown kubectl error").strip()
        raise CLIError(
            f"cache path '{cache_root}' is not reachable on node '{node_name}': "
            f"{detail}. Create the directory on that node or choose a different "
            "--cache-path."
        ) from exc
    finally:
        try:
            runner(delete_command)
        except (FileNotFoundError, subprocess.CalledProcessError):
            pass
        manifest_path.unlink(missing_ok=True)


def _write_cache_probe_manifest(
    request: InstallRequest,
    pod_name: str,
    node_name: str,
    cache_root: str,
) -> Path:
    manifest = {
        "apiVersion": "v1",
        "kind": "Pod",
        "metadata": {
            "name": pod_name,
            "namespace": request.cluster.namespace,
            "labels": {
                "app.kubernetes.io/part-of": "inferops",
                "inferops.dev/component": "cache-install-probe",
            },
        },
        "spec": {
            "nodeName": node_name,
            "restartPolicy": "Never",
            "automountServiceAccountToken": False,
            "containers": [
                {
                    "name": "probe",
                    "image": CACHE_PROBE_IMAGE,
                    "command": ["sh", "-c", "df -Pk /cache >/dev/null"],
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
    }
    with tempfile.NamedTemporaryFile(
        "w",
        encoding="utf-8",
        prefix="inferops-cache-probe-",
        suffix=".json",
        delete=False,
    ) as handle:
        json.dump(manifest, handle, separators=(",", ":"))
        handle.write("\n")
        return Path(handle.name)


def _annotate_cache_node(
    runner: CommandRunner,
    request: InstallRequest,
    node_name: str,
    capacity: str,
) -> dict[str, str]:
    command = _kubectl_command(request)
    command.extend(
        (
            "annotate",
            "node",
            node_name,
            f"{CACHE_CAPACITY_ANNOTATION}={capacity}",
            "--overwrite",
        )
    )
    try:
        completed = runner(command)
    except FileNotFoundError as exc:
        raise CLIError(
            "kubectl executable not found; install kubectl before annotating cache nodes"
        ) from exc
    except subprocess.CalledProcessError as exc:
        detail = (exc.stderr or exc.stdout or "unknown kubectl error").strip()
        raise CLIError(f"failed to annotate cache node '{node_name}': {detail}") from exc
    return {
        "node": node_name,
        "annotation": CACHE_CAPACITY_ANNOTATION,
        "capacity": capacity,
        "output": completed.stdout.strip(),
    }


def _kubectl_command(request: InstallRequest) -> list[str]:
    command = ["kubectl"]
    if request.cluster.kubeconfig:
        command.extend(("--kubeconfig", request.cluster.kubeconfig))
    if request.cluster.context:
        command.extend(("--context", request.cluster.context))
    return command


def _cache_probe_pod_name(node_name: str) -> str:
    safe_node = re.sub(r"[^a-z0-9-]+", "-", node_name.lower()).strip("-")
    safe_node = safe_node[:24] or "node"
    return f"inferops-cache-probe-{safe_node}-{uuid.uuid4().hex[:8]}"


def _validate_cache_capacity(label: str, capacity: str) -> None:
    if not CACHE_CAPACITY.fullmatch(capacity):
        raise CLIError(
            f"{label} must be an explicit Kubernetes storage quantity such as 100Gi"
        )


def _validate_node_name(label: str, value: str) -> None:
    if len(value) > 253 or not NODE_NAME.fullmatch(value):
        raise CLIError(f"{label} must be a valid Kubernetes node name")
