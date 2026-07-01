"""Helm integration for installing InferOps components."""

from __future__ import annotations

from collections.abc import Callable, Sequence
import importlib.resources
import os
from pathlib import Path, PurePosixPath
import posixpath
import re
import subprocess
from typing import Any

from .errors import CLIError
from .kube import InstallRequest

DEFAULT_CACHE_ROOT = "/var/lib/inferops/models"
DEFAULT_RELEASES = ("inferops-operator", "inferops-gateway")
DEFAULT_TIMEOUT = "5m"
TAILSCALE_HOSTNAME = re.compile(r"^[a-z](?:[a-z0-9-]*[a-z])?$")

CommandRunner = Callable[[Sequence[str]], subprocess.CompletedProcess[str]]


class HelmInstaller:
    """Install or upgrade InferOps Helm releases."""

    def __init__(self, runner: CommandRunner | None = None) -> None:
        self._runner = runner or _run_command

    def install(self, request: InstallRequest) -> dict[str, Any]:
        """Install or upgrade the charts selected by an install request."""
        if request.profile not in {"default", "homelab"}:
            raise CLIError(f"unsupported install profile: {request.profile}")

        cache_root = request.cache_path or DEFAULT_CACHE_ROOT
        _validate_cache_root(cache_root)
        if request.tailscale_hostname:
            _validate_tailscale_hostname(request.tailscale_hostname)
        charts_dir = _resolve_charts_dir(request.charts_dir)

        releases = []
        for release_name in DEFAULT_RELEASES:
            chart_dir = charts_dir / release_name
            if not (chart_dir / "Chart.yaml").is_file():
                raise CLIError(f"Helm chart not found: {chart_dir}")

            command = _build_upgrade_command(
                release_name=release_name,
                chart_dir=chart_dir,
                request=request,
                cache_root=cache_root,
            )
            try:
                completed = self._runner(command)
            except FileNotFoundError as exc:
                raise CLIError(
                    "helm executable not found; install Helm 3.15 or newer"
                ) from exc
            except subprocess.CalledProcessError as exc:
                detail = (exc.stderr or exc.stdout or "unknown Helm error").strip()
                raise CLIError(f"Helm install failed: {detail}") from exc
            releases.append(
                {
                    "name": release_name,
                    "chart": release_name,
                    "status": "deployed",
                    "output": completed.stdout.strip(),
                }
            )

        resources = [
            f"namespace/{request.cluster.namespace}",
            "crd/modelcaches.inference.inferops.dev",
            "crd/modeldeployments.inference.inferops.dev",
            "crd/modelruntimes.inference.inferops.dev",
            "deployment/inferops-operator",
            "deployment/inferops-gateway",
            "modelruntime/nano-vllm",
            "modelruntime/vllm",
            "modelruntime/sglang",
            "modelruntime/llama-cpp",
        ]
        if request.tailscale_hostname:
            resources.append("ingress/inferops-gateway-tailscale")

        return {
            "cluster": request.cluster.to_safe_dict(),
            "install": {
                "profile": request.profile,
                "namespace": request.cluster.namespace,
                "cachePath": cache_root,
                "tailscaleHostname": request.tailscale_hostname,
                "resources": resources,
                "releases": releases,
            },
        }


def _build_upgrade_command(
    release_name: str,
    chart_dir: Path,
    request: InstallRequest,
    cache_root: str,
) -> list[str]:
    command = [
        "helm",
        "upgrade",
        "--install",
        release_name,
        str(chart_dir),
        "--namespace",
        request.cluster.namespace,
        "--create-namespace",
        "--atomic",
        "--wait",
        "--timeout",
        DEFAULT_TIMEOUT,
    ]
    if request.cluster.kubeconfig:
        command.extend(("--kubeconfig", request.cluster.kubeconfig))
    if request.cluster.context:
        command.extend(("--kube-context", request.cluster.context))

    profile_values = chart_dir / f"values-{request.profile}.yaml"
    if profile_values.is_file():
        command.extend(("--values", str(profile_values)))

    if release_name == "inferops-operator":
        command.extend(
            (
                "--set-string",
                f"cache.root={_escape_helm_string(cache_root)}",
                "--set-string",
                f"profile={request.profile}",
            )
        )
    elif request.tailscale_hostname:
        command.extend(
            (
                "--set",
                "tailscale.enabled=true",
                "--set-string",
                f"tailscale.hostname={_escape_helm_string(request.tailscale_hostname)}",
            )
        )
    return command


def _resolve_charts_dir(explicit_path: str | None) -> Path:
    if explicit_path:
        return _require_charts_dir(Path(explicit_path), "--charts-dir")

    environment_path = os.environ.get("INFEROPS_CHARTS_DIR")
    if environment_path:
        return _require_charts_dir(Path(environment_path), "INFEROPS_CHARTS_DIR")

    source_root = Path(__file__).resolve().parents[2]
    candidates = [source_root / "deploy" / "helm"]

    try:
        packaged = importlib.resources.files("inferops_cli").joinpath("charts")
        candidates.append(Path(str(packaged)))
    except (ModuleNotFoundError, TypeError):
        pass

    for candidate in candidates:
        resolved = candidate.expanduser().resolve()
        if all(
            (resolved / release / "Chart.yaml").is_file()
            for release in DEFAULT_RELEASES
        ):
            return resolved

    searched = ", ".join(str(path) for path in candidates)
    raise CLIError(
        "InferOps Helm charts were not found. "
        f"Set --charts-dir or INFEROPS_CHARTS_DIR (searched: {searched})."
    )


def _require_charts_dir(path: Path, source: str) -> Path:
    resolved = path.expanduser().resolve()
    missing = [
        release
        for release in DEFAULT_RELEASES
        if not (resolved / release / "Chart.yaml").is_file()
    ]
    if missing:
        raise CLIError(
            f"{source} does not contain the required InferOps Helm charts "
            f"({', '.join(missing)}): {resolved}"
        )
    return resolved


def _validate_cache_root(cache_root: str) -> None:
    if len(cache_root) > 4096 or any(ord(character) < 32 for character in cache_root):
        raise CLIError(
            "cache path must not contain control characters and must be at most 4096 characters"
        )
    path = PurePosixPath(cache_root)
    if not path.is_absolute():
        raise CLIError(f"cache path must be absolute: {cache_root}")
    if path == PurePosixPath("/"):
        raise CLIError("cache path must not be the filesystem root")
    if posixpath.normpath(cache_root) != cache_root:
        raise CLIError(f"cache path must be clean: {cache_root}")


def _validate_tailscale_hostname(hostname: str) -> None:
    if len(hostname) > 63 or not TAILSCALE_HOSTNAME.fullmatch(hostname):
        raise CLIError(
            "Tailscale hostname must be at most 63 characters, start and end "
            "with a lowercase letter, and contain only letters, digits, and hyphens"
        )


def _escape_helm_string(value: str) -> str:
    return value.replace("\\", "\\\\").replace(",", "\\,")


def _run_command(command: Sequence[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        list(command),
        check=True,
        capture_output=True,
        text=True,
    )
