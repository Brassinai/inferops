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
from .kube import InstallRequest, UpgradeRequest

DEFAULT_CACHE_ROOT = "/var/lib/inferops/models"
DEFAULT_RELEASES = ("inferops-operator", "inferops-gateway")
CONTROL_PLANE_RELEASES = ("inferops-operator", "inferops-dashboard")
DEFAULT_TIMEOUT = "5m"
CRD_FIELD_MANAGER = "inferops-cli"
COMPUTE_PROFILES = {"cpu", "nvidia-gpu"}
TAILSCALE_HOSTNAME = re.compile(r"^[a-z](?:[a-z0-9-]*[a-z])?$")
DNS_SUBDOMAIN = re.compile(
    r"^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)*$"
)
DNS_LABEL = re.compile(r"^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$")
QUALIFIED_NAME = re.compile(
    r"^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?"
    r"(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)*/)?"
    r"[A-Za-z0-9](?:[-_.A-Za-z0-9]*[A-Za-z0-9])?$"
)
IMAGE_TAG = re.compile(r"^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$")
EXPOSURE_METHODS = {"cluster-ip", "load-balancer", "ingress", "gateway-api"}

CommandRunner = Callable[[Sequence[str]], subprocess.CompletedProcess[str]]


class HelmInstaller:
    """Install or upgrade InferOps Helm releases."""

    def __init__(self, runner: CommandRunner | None = None) -> None:
        self._runner = runner or _run_command

    def install(self, request: InstallRequest) -> dict[str, Any]:
        """Install or upgrade the charts selected by an install request."""
        if request.profile not in {"default", "homelab"}:
            raise CLIError(f"unsupported install profile: {request.profile}")
        if request.compute_profile not in COMPUTE_PROFILES:
            raise CLIError(f"unsupported compute profile: {request.compute_profile}")

        cache_root = request.cache_path or DEFAULT_CACHE_ROOT
        _validate_cache_root(cache_root)
        if request.tailscale_hostname:
            _validate_tailscale_hostname(request.tailscale_hostname)
        exposure = _validate_exposure(request)
        charts_dir = _resolve_charts_dir(request.charts_dir)
        crds_dir = charts_dir / "inferops-operator" / "crds"
        if not crds_dir.is_dir() or not any(crds_dir.glob("*.yaml")):
            raise CLIError(f"operator CRDs not found: {crds_dir}")

        crd_command = _build_crd_apply_command(crds_dir, request)
        try:
            crd_result = self._runner(crd_command)
        except FileNotFoundError as exc:
            raise CLIError(
                "kubectl executable not found; install kubectl before installing InferOps"
            ) from exc
        except subprocess.CalledProcessError as exc:
            detail = (exc.stderr or exc.stdout or "unknown kubectl error").strip()
            raise CLIError(f"InferOps CRD apply failed: {detail}") from exc

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
        elif exposure == "ingress":
            resources.append("ingress/inferops-gateway")
        elif exposure == "gateway-api":
            resources.append("httproute/inferops-gateway")
        elif exposure == "load-balancer":
            resources.append("service/inferops-gateway")

        return {
            "cluster": request.cluster.to_safe_dict(),
            "install": {
                "profile": request.profile,
                "computeProfile": request.compute_profile,
                "namespace": request.cluster.namespace,
                "cachePath": cache_root,
                "tailscaleHostname": request.tailscale_hostname,
                "exposure": exposure,
                "authEnabled": bool(request.gateway_auth_secret),
                "resources": resources,
                "crds": {
                    "status": "applied",
                    "output": crd_result.stdout.strip(),
                },
                "releases": releases,
            },
        }

    def upgrade(self, request: UpgradeRequest) -> dict[str, Any]:
        """Upgrade installed operator and dashboard releases to a new image tag."""
        _validate_image_tag(request.tag)
        _validate_image_repository("operator image repository", request.operator_image_repository)
        _validate_image_repository("dashboard image repository", request.dashboard_image_repository)

        releases_to_upgrade = ["inferops-operator"]
        if request.include_dashboard:
            releases_to_upgrade.append("inferops-dashboard")

        charts_dir = _resolve_charts_dir(request.charts_dir, releases=tuple(releases_to_upgrade))
        crds_dir = charts_dir / "inferops-operator" / "crds"
        if not crds_dir.is_dir() or not any(crds_dir.glob("*.yaml")):
            raise CLIError(f"operator CRDs not found: {crds_dir}")

        crd_command = _build_crd_apply_command(crds_dir, request)
        try:
            crd_result = self._runner(crd_command)
        except FileNotFoundError as exc:
            raise CLIError(
                "kubectl executable not found; install kubectl before upgrading InferOps"
            ) from exc
        except subprocess.CalledProcessError as exc:
            detail = (exc.stderr or exc.stdout or "unknown kubectl error").strip()
            raise CLIError(f"InferOps CRD apply failed: {detail}") from exc

        releases = []
        for release_name in releases_to_upgrade:
            chart_dir = charts_dir / release_name
            if not (chart_dir / "Chart.yaml").is_file():
                raise CLIError(f"Helm chart not found: {chart_dir}")
            command = _build_control_plane_upgrade_command(
                release_name=release_name,
                chart_dir=chart_dir,
                request=request,
            )
            try:
                completed = self._runner(command)
            except FileNotFoundError as exc:
                raise CLIError(
                    "helm executable not found; install Helm 3.15 or newer"
                ) from exc
            except subprocess.CalledProcessError as exc:
                detail = (exc.stderr or exc.stdout or "unknown Helm error").strip()
                raise CLIError(f"Helm upgrade failed: {detail}") from exc
            releases.append(
                {
                    "name": release_name,
                    "chart": release_name,
                    "status": "upgraded",
                    "imageTag": request.tag,
                    "output": completed.stdout.strip(),
                }
            )

        resources = [
            "crd/modelcaches.inference.inferops.dev",
            "crd/modeldeployments.inference.inferops.dev",
            "crd/modelruntimes.inference.inferops.dev",
            "deployment/inferops-operator",
        ]
        if request.include_dashboard:
            resources.append("deployment/inferops-dashboard")
        if request.enable_observability:
            resources.extend(
                (
                    "servicemonitor/inferops-operator",
                    "grafana-dashboard/inferops-platform",
                    "grafana-dashboard/inferops-vllm",
                    "grafana-dashboard/inferops-llama-cpp",
                )
            )

        return {
            "cluster": request.cluster.to_safe_dict(),
            "upgrade": {
                "namespace": request.cluster.namespace,
                "tag": request.tag,
                "operatorImage": request.operator_image_repository,
                "dashboardImage": request.dashboard_image_repository if request.include_dashboard else None,
                "dashboardIncluded": request.include_dashboard,
                "observabilityEnabled": request.enable_observability,
                "resources": resources,
                "crds": {
                    "status": "applied",
                    "output": crd_result.stdout.strip(),
                },
                "releases": releases,
            },
        }


def _build_crd_apply_command(
    crds_dir: Path,
    request: InstallRequest,
) -> list[str]:
    command = ["kubectl"]
    if request.cluster.kubeconfig:
        command.extend(("--kubeconfig", request.cluster.kubeconfig))
    if request.cluster.context:
        command.extend(("--context", request.cluster.context))
    command.extend(
        (
            "apply",
            "--server-side",
            f"--field-manager={CRD_FIELD_MANAGER}",
            "--filename",
            str(crds_dir),
        )
    )
    return command


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
        command.extend(_operator_compute_profile_values(request))
    else:
        command.extend(_gateway_exposure_values(request))
        command.extend(_gateway_auth_values(request))
    return command


def _build_control_plane_upgrade_command(
    release_name: str,
    chart_dir: Path,
    request: UpgradeRequest,
) -> list[str]:
    command = [
        "helm",
        "upgrade",
        release_name,
        str(chart_dir),
        "--namespace",
        request.cluster.namespace,
        "--reuse-values",
        "--wait",
        "--timeout",
        DEFAULT_TIMEOUT,
    ]
    if request.cluster.kubeconfig:
        command.extend(("--kubeconfig", request.cluster.kubeconfig))
    if request.cluster.context:
        command.extend(("--kube-context", request.cluster.context))

    if release_name == "inferops-operator":
        command.extend(
            (
                "--set-string",
                "image.repository=" + _escape_helm_string(request.operator_image_repository),
                "--set-string",
                "image.tag=" + _escape_helm_string(request.tag),
            )
        )
        if request.enable_observability:
            command.extend(
                (
                    "--set",
                    "serviceMonitor.enabled=true",
                    "--set",
                    "dashboards.enabled=true",
                )
            )
    elif release_name == "inferops-dashboard":
        command.extend(
            (
                "--set-string",
                "image.repository=" + _escape_helm_string(request.dashboard_image_repository),
                "--set-string",
                "image.tag=" + _escape_helm_string(request.tag),
            )
        )
    else:
        raise CLIError(f"unsupported control-plane release: {release_name}")
    return command


def _operator_compute_profile_values(request: InstallRequest) -> list[str]:
    if request.compute_profile == "cpu":
        return [
            "--set-string",
            "gpu.required=false",
            "--set-json",
            "cache.requiredNodeResources=[]",
        ]
    if request.compute_profile == "nvidia-gpu":
        return [
            "--set-string",
            "gpu.required=true",
            "--set-json",
            'cache.requiredNodeResources=["nvidia.com/gpu"]',
        ]
    raise CLIError(f"unsupported compute profile: {request.compute_profile}")


def _gateway_exposure_values(request: InstallRequest) -> list[str]:
    if request.tailscale_hostname:
        return [
            "--set",
            "tailscale.enabled=true",
            "--set-string",
            f"tailscale.hostname={_escape_helm_string(request.tailscale_hostname)}",
        ]

    exposure = request.exposure or "cluster-ip"
    if exposure == "cluster-ip":
        return []
    if exposure == "load-balancer":
        values = ["--set-string", "service.type=LoadBalancer"]
        if request.load_balancer_class:
            values.extend(
                (
                    "--set-string",
                    "service.loadBalancerClass="
                    + _escape_helm_string(request.load_balancer_class),
                )
            )
        return values
    if exposure == "ingress":
        values = [
            "--set",
            "ingress.enabled=true",
            "--set-string",
            "ingress.className=" + _escape_helm_string(request.ingress_class or ""),
        ]
        if request.ingress_hostname:
            values.extend(
                (
                    "--set-string",
                    "ingress.hostname="
                    + _escape_helm_string(request.ingress_hostname),
                )
            )
        return values

    values = [
        "--set",
        "gatewayAPI.enabled=true",
        "--set-string",
        "gatewayAPI.parentRefs[0].name="
        + _escape_helm_string(request.gateway_name or ""),
    ]
    if request.gateway_namespace:
        values.extend(
            (
                "--set-string",
                "gatewayAPI.parentRefs[0].namespace="
                + _escape_helm_string(request.gateway_namespace),
            )
        )
    if request.gateway_section_name:
        values.extend(
            (
                "--set-string",
                "gatewayAPI.parentRefs[0].sectionName="
                + _escape_helm_string(request.gateway_section_name),
            )
        )
    if request.gateway_hostname:
        values.extend(
            (
                "--set-string",
                "gatewayAPI.hostnames[0]="
                + _escape_helm_string(request.gateway_hostname),
            )
        )
    return values


def _gateway_auth_values(request: InstallRequest) -> list[str]:
    if not request.gateway_auth_secret:
        return []
    return [
        "--set",
        "auth.enabled=true",
        "--set-string",
        "auth.secretName=" + _escape_helm_string(request.gateway_auth_secret),
    ]


def _resolve_charts_dir(
    explicit_path: str | None,
    releases: tuple[str, ...] = DEFAULT_RELEASES,
) -> Path:
    if explicit_path:
        return _require_charts_dir(Path(explicit_path), "--charts-dir", releases)

    environment_path = os.environ.get("INFEROPS_CHARTS_DIR")
    if environment_path:
        return _require_charts_dir(Path(environment_path), "INFEROPS_CHARTS_DIR", releases)

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
            for release in releases
        ):
            return resolved

    searched = ", ".join(str(path) for path in candidates)
    raise CLIError(
        "InferOps Helm charts were not found. "
        f"Set --charts-dir or INFEROPS_CHARTS_DIR (searched: {searched})."
    )


def _require_charts_dir(
    path: Path,
    source: str,
    releases: tuple[str, ...] = DEFAULT_RELEASES,
) -> Path:
    resolved = path.expanduser().resolve()
    missing = [
        release
        for release in releases
        if not (resolved / release / "Chart.yaml").is_file()
    ]
    if missing:
        raise CLIError(
            f"{source} does not contain the required InferOps Helm charts "
            f"({', '.join(missing)}): {resolved}"
        )
    return resolved


def _validate_image_tag(tag: str) -> None:
    if not IMAGE_TAG.fullmatch(tag):
        raise CLIError(
            "image tag must be a valid Docker tag: letters, digits, underscores, dots, and hyphens"
        )


def _validate_image_repository(label: str, repository: str) -> None:
    if (
        not repository
        or len(repository) > 255
        or any(ord(character) < 33 for character in repository)
        or ":" in repository.rsplit("/", 1)[-1]
        or "@" in repository
    ):
        raise CLIError(f"{label} must be a repository without a tag")


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


def _validate_exposure(request: InstallRequest) -> str:
    exposure = request.exposure or (
        "tailscale" if request.tailscale_hostname else "cluster-ip"
    )
    if request.exposure is not None and request.exposure not in EXPOSURE_METHODS:
        raise CLIError(f"unsupported gateway exposure: {request.exposure}")
    if request.tailscale_hostname and request.exposure is not None:
        raise CLIError(
            "--tailscale-hostname cannot be combined with --exposure; choose one exposure method"
        )

    ingress_values = (request.ingress_class, request.ingress_hostname)
    gateway_values = (
        request.gateway_name,
        request.gateway_namespace,
        request.gateway_section_name,
        request.gateway_hostname,
    )
    if exposure == "ingress":
        if not request.ingress_class:
            raise CLIError("--ingress-class is required with --exposure ingress")
    elif any(ingress_values):
        raise CLIError(
            "--ingress-class and --ingress-hostname require --exposure ingress"
        )

    if exposure == "gateway-api":
        if not request.gateway_name:
            raise CLIError("--gateway-name is required with --exposure gateway-api")
    elif any(gateway_values):
        raise CLIError("Gateway options require --exposure gateway-api")

    if exposure != "load-balancer" and request.load_balancer_class:
        raise CLIError(
            "--load-balancer-class requires --exposure load-balancer"
        )
    external_exposures = {"ingress", "gateway-api", "load-balancer"}
    if (
        exposure in external_exposures
        and not request.gateway_auth_secret
        and not request.allow_unauthenticated_exposure
    ):
        raise CLIError(
            "external gateway exposure requires --gateway-auth-secret or an "
            "explicit --allow-unauthenticated-exposure acknowledgement"
        )
    if request.allow_unauthenticated_exposure and exposure not in external_exposures:
        raise CLIError(
            "--allow-unauthenticated-exposure requires an external --exposure method"
        )

    for label, value in (
        ("IngressClass", request.ingress_class),
        ("ingress hostname", request.ingress_hostname),
        ("Gateway name", request.gateway_name),
        ("Gateway hostname", request.gateway_hostname),
        ("gateway auth Secret", request.gateway_auth_secret),
    ):
        if value:
            _validate_dns_subdomain(label, value)
    for label, value in (
        ("Gateway namespace", request.gateway_namespace),
        ("Gateway listener", request.gateway_section_name),
    ):
        if value:
            _validate_dns_label(label, value)
    if request.load_balancer_class and (
        len(request.load_balancer_class) > 253
        or not QUALIFIED_NAME.fullmatch(request.load_balancer_class)
    ):
        raise CLIError(
            "load balancer class must be a valid Kubernetes qualified name"
        )
    return exposure


def _validate_dns_subdomain(label: str, value: str) -> None:
    candidate = value[2:] if value.startswith("*.") else value
    if len(value) > 253 or not DNS_SUBDOMAIN.fullmatch(candidate):
        raise CLIError(
            f"{label} must be a valid lowercase Kubernetes DNS name"
        )


def _validate_dns_label(label: str, value: str) -> None:
    if len(value) > 63 or not DNS_LABEL.fullmatch(value):
        raise CLIError(
            f"{label} must be a valid lowercase Kubernetes DNS label"
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
