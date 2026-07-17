"""Helm integration for installing InferOps components."""

from __future__ import annotations

from collections.abc import Callable, Sequence
import importlib.resources
import json
import os
from pathlib import Path, PurePosixPath
import posixpath
import re
import subprocess
import tempfile
from typing import Any

from .cache_install import CacheNodePreparer, validate_cache_install_options
from .errors import CLIError
from .kube import (
    ClusterTarget,
    InstallRequest,
    ObservabilityEnableRequest,
    ObservabilityInstallRequest,
    ObservabilitySetupRequest,
    UninstallRequest,
    UpgradeRequest,
)

DEFAULT_CACHE_ROOT = "/var/lib/inferops/models"
DEFAULT_RELEASES = ("inferops-operator", "inferops-gateway")
CONTROL_PLANE_RELEASES = (
    "inferops-operator",
    "inferops-gateway",
    "inferops-dashboard",
)
OBSERVABILITY_RELEASES = ("inferops-operator", "inferops-gateway")
UNINSTALL_RELEASES = ("inferops-operator", "inferops-gateway", "inferops-dashboard")
DEFAULT_TIMEOUT = "5m"
CRD_FIELD_MANAGER = "inferops-cli"
CRD_NAMES = (
    "modelcaches.inference.inferops.dev",
    "modeldeployments.inference.inferops.dev",
    "modelruntimes.inference.inferops.dev",
)
COMPUTE_PROFILES = {"cpu", "nvidia-gpu"}
TAILSCALE_HOSTNAME = re.compile(r"^[a-z](?:[a-z0-9-]*[a-z])?$")
HELM_RELEASE_NAME = re.compile(r"^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$")
HELM_CHART_VERSION = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.+-]{0,127}$")
DNS_SUBDOMAIN = re.compile(
    r"^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)*$"
)
DNS_LABEL = re.compile(r"^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$")
QUALIFIED_NAME = re.compile(
    r"^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?"
    r"(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)*/)?"
    r"[A-Za-z0-9](?:[-_.A-Za-z0-9]*[A-Za-z0-9])?$"
)
LABEL_NAME = re.compile(r"^[A-Za-z0-9](?:[-_.A-Za-z0-9]{0,61}[A-Za-z0-9])?$")
LABEL_VALUE = LABEL_NAME
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
        validate_cache_install_options(request)
        if request.tailscale_hostname:
            _validate_tailscale_hostname(request.tailscale_hostname)
        exposure = _validate_exposure(request)
        charts_dir = _resolve_charts_dir(request.charts_dir)
        crds_dir = charts_dir / "inferops-operator" / "crds"
        if not crds_dir.is_dir() or not any(crds_dir.glob("*.yaml")):
            raise CLIError(f"operator CRDs not found: {crds_dir}")

        annotated_nodes: list[dict[str, str]] = []
        if request.cache_capacity or request.cache_node_capacities:
            annotated_nodes = CacheNodePreparer(self._runner).prepare(
                request,
                cache_root,
            )

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
                "cacheAnnotations": annotated_nodes,
                "resources": resources,
                "crds": {
                    "status": "applied",
                    "output": crd_result.stdout.strip(),
                },
                "releases": releases,
            },
        }

    def upgrade(self, request: UpgradeRequest) -> dict[str, Any]:
        """Upgrade installed control-plane image tags."""
        _validate_upgrade_request(request)
        release_tags = _upgrade_release_tags(request)
        releases_to_upgrade = tuple(release_tags)

        _validate_upgrade_image_repositories(request, release_tags)

        charts_dir = _resolve_charts_dir(
            request.charts_dir, releases=releases_to_upgrade
        )
        crds = {"status": "skipped", "output": ""}
        if "inferops-operator" in release_tags:
            crds_dir = charts_dir / "inferops-operator" / "crds"
            if not crds_dir.is_dir() or not any(crds_dir.glob("*.yaml")):
                raise CLIError(f"operator CRDs not found: {crds_dir}")

            crd_command = _build_crd_apply_command(crds_dir, request)
            try:
                crd_result = self._runner(crd_command)
            except FileNotFoundError as exc:
                raise CLIError(
                    "kubectl executable not found; install kubectl before "
                    "upgrading InferOps"
                ) from exc
            except subprocess.CalledProcessError as exc:
                detail = (exc.stderr or exc.stdout or "unknown kubectl error").strip()
                raise CLIError(f"InferOps CRD apply failed: {detail}") from exc
            crds = {"status": "applied", "output": crd_result.stdout.strip()}

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
                    "imageTag": release_tags[release_name],
                    "output": completed.stdout.strip(),
                }
            )

        resources = [
            f"deployment/{_release_deployment_name(name)}"
            for name in release_tags
        ]
        if "inferops-operator" in release_tags:
            resources = [
                "crd/modelcaches.inference.inferops.dev",
                "crd/modeldeployments.inference.inferops.dev",
                "crd/modelruntimes.inference.inferops.dev",
                *resources,
            ]
        if request.enable_observability:
            resources.extend(
                (
                    "servicemonitor/inferops-operator",
                    "servicemonitor/inferops-gateway",
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
                "component": request.component,
                "operatorImage": request.operator_image_repository,
                "gatewayImage": request.gateway_image_repository,
                "dashboardImage": (
                    request.dashboard_image_repository
                    if "inferops-dashboard" in release_tags
                    else None
                ),
                "operatorTag": release_tags.get("inferops-operator"),
                "gatewayTag": release_tags.get("inferops-gateway"),
                "dashboardTag": release_tags.get("inferops-dashboard"),
                "dashboardIncluded": request.include_dashboard,
                "observabilityEnabled": request.enable_observability,
                "resources": resources,
                "crds": crds,
                "releases": releases,
            },
        }

    def uninstall(self, request: UninstallRequest) -> dict[str, Any]:
        """Uninstall InferOps Helm releases while preserving data by default."""
        purge_result = None
        if request.purge_cache_files:
            purge_result = self._purge_cache_files(request)
        elif request.cache_path or request.cache_node_selector or request.confirm_cache_purge:
            raise CLIError(
                "--cache-path, --cache-node-selector, and "
                "--confirm-cache-purge require --purge-cache-files"
            )

        release_names = list(UNINSTALL_RELEASES)
        if not request.include_dashboard:
            release_names.remove("inferops-dashboard")

        releases = []
        for release_name in release_names:
            command = _build_uninstall_command(release_name, request)
            try:
                completed = self._runner(command)
            except FileNotFoundError as exc:
                raise CLIError(
                    "helm executable not found; install Helm 3.15 or newer"
                ) from exc
            except subprocess.CalledProcessError as exc:
                detail = (exc.stderr or exc.stdout or "unknown Helm error").strip()
                raise CLIError(f"Helm uninstall failed: {detail}") from exc
            releases.append(
                {
                    "name": release_name,
                    "status": "uninstalled",
                    "output": completed.stdout.strip(),
                }
            )

        crds = {"status": "preserved", "output": ""}
        if request.delete_crds:
            command = _build_crd_delete_command(request)
            try:
                completed = self._runner(command)
            except FileNotFoundError as exc:
                raise CLIError(
                    "kubectl executable not found; install kubectl before "
                    "deleting InferOps CRDs"
                ) from exc
            except subprocess.CalledProcessError as exc:
                detail = (exc.stderr or exc.stdout or "unknown kubectl error").strip()
                raise CLIError(f"InferOps CRD delete failed: {detail}") from exc
            crds = {"status": "deleted", "output": completed.stdout.strip()}

        resources = [f"helmrelease/{name}" for name in release_names]
        if request.delete_crds:
            resources.extend(f"crd/{name}" for name in CRD_NAMES)
        if purge_result is not None:
            resources.append("daemonset/inferops-cache-purge")

        return {
            "cluster": request.cluster.to_safe_dict(),
            "uninstall": {
                "namespace": request.cluster.namespace,
                "dashboardIncluded": request.include_dashboard,
                "crdsDeleted": request.delete_crds,
                "customResourcesPreserved": not request.delete_crds,
                "cacheFilesDeleted": request.purge_cache_files,
                "resources": resources,
                "releases": releases,
                "crds": crds,
                "cachePurge": purge_result,
            },
        }

    def _purge_cache_files(self, request: UninstallRequest) -> dict[str, Any]:
        _validate_cache_purge_request(request)
        assert request.cache_path is not None
        assert request.cache_node_selector is not None

        manifest = _cache_purge_daemonset(
            namespace=request.cluster.namespace,
            cache_path=request.cache_path,
            node_selector=_parse_node_selector(request.cache_node_selector),
        )
        probe_command = _build_cache_purge_node_probe_command(request)
        completed = self._run_cache_purge_command(probe_command)
        node_names = _cache_purge_node_names(completed.stdout)
        if not node_names:
            raise CLIError(
                "no nodes matched --cache-node-selector; cache files were not purged"
            )
        executed: list[dict[str, str]] = []
        executed.append(_summarize_cache_purge_step("nodeProbe", completed))
        with tempfile.TemporaryDirectory(prefix="inferops-cache-purge-") as tmpdir:
            manifest_path = Path(tmpdir) / "daemonset.json"
            manifest_path.write_text(json.dumps(manifest), encoding="utf-8")
            apply_command = _build_kubectl_apply_file_command(request, manifest_path)
            rollout_command = _build_cache_purge_rollout_command(request)
            delete_command = _build_kubectl_delete_file_command(request, manifest_path)
            applied = False
            cleanup_error: CLIError | None = None
            try:
                completed = self._run_cache_purge_command(apply_command)
                applied = True
                executed.append(_summarize_cache_purge_step("apply", completed))
                completed = self._run_cache_purge_command(rollout_command)
                executed.append(_summarize_cache_purge_step("rollout", completed))
            finally:
                if applied:
                    try:
                        completed = self._run_cache_purge_command(delete_command)
                        executed.append(
                            _summarize_cache_purge_step("cleanup", completed)
                        )
                    except CLIError as exc:
                        cleanup_error = exc
            if cleanup_error is not None:
                raise cleanup_error

        return {
            "status": "purged",
            "cachePath": request.cache_path,
            "nodeSelector": request.cache_node_selector,
            "commands": executed,
        }

    def _run_cache_purge_command(
        self, command: list[str]
    ) -> subprocess.CompletedProcess[str]:
        try:
            return self._runner(command)
        except FileNotFoundError as exc:
            raise CLIError(
                "kubectl executable not found; install kubectl before purging "
                "node-local cache files"
            ) from exc
        except subprocess.CalledProcessError as exc:
            detail = (exc.stderr or exc.stdout or "unknown kubectl error").strip()
            raise CLIError(f"InferOps cache purge failed: {detail}") from exc

    def observability_install(
        self, request: ObservabilityInstallRequest
    ) -> dict[str, Any]:
        """Install or upgrade kube-prometheus-stack."""
        _validate_helm_release("Prometheus/Grafana release", request.release)
        _validate_chart_reference(request.chart)
        if request.chart_version is not None:
            _validate_chart_version(request.chart_version)
        if request.grafana_admin_password is not None:
            _validate_grafana_password(request.grafana_admin_password)

        commands = _build_observability_stack_commands(request)
        executed: list[dict[str, Any]] = []
        for command in commands:
            try:
                completed = self._runner(command)
            except FileNotFoundError as exc:
                raise CLIError(
                    "helm executable not found; install Helm 3.15 or newer"
                ) from exc
            except subprocess.CalledProcessError as exc:
                detail = (exc.stderr or exc.stdout or "unknown Helm error").strip()
                raise CLIError(f"observability stack install failed: {detail}") from exc
            executed.append(
                {
                    "command": _summarize_observability_command(command),
                    "status": "completed",
                    "output": completed.stdout.strip(),
                }
            )

        resources = [
            f"namespace/{request.cluster.namespace}",
            f"helmrelease/{request.release}",
            "deployment/grafana",
            "statefulset/prometheus",
        ]
        return {
            "cluster": request.cluster.to_safe_dict(),
            "observability": {
                "operation": "install",
                "monitoringNamespace": request.cluster.namespace,
                "release": request.release,
                "chart": request.chart,
                "chartVersion": request.chart_version,
                "grafanaAdminPasswordConfigured": bool(
                    request.grafana_admin_password
                ),
                "resources": resources,
                "commands": executed,
            },
        }

    def observability_enable(
        self, request: ObservabilityEnableRequest
    ) -> dict[str, Any]:
        """Enable InferOps ServiceMonitor and Grafana dashboard resources."""
        charts_dir = _resolve_charts_dir(request.charts_dir, releases=OBSERVABILITY_RELEASES)
        releases = []
        for release_name in OBSERVABILITY_RELEASES:
            chart_dir = charts_dir / release_name
            command = _build_observability_enable_command(
                release_name=release_name,
                chart_dir=chart_dir,
                cluster=request.cluster,
            )
            try:
                completed = self._runner(command)
            except FileNotFoundError as exc:
                raise CLIError(
                    "helm executable not found; install Helm 3.15 or newer"
                ) from exc
            except subprocess.CalledProcessError as exc:
                detail = (exc.stderr or exc.stdout or "unknown Helm error").strip()
                raise CLIError(f"InferOps observability enable failed: {detail}") from exc
            releases.append(
                {
                    "name": release_name,
                    "chart": release_name,
                    "status": "upgraded",
                    "output": completed.stdout.strip(),
                }
            )

        resources = [
            "servicemonitor/inferops-operator",
            "servicemonitor/inferops-gateway",
            "servicemonitor/inferops-runtimes",
            "grafana-dashboard/inferops-platform",
            "grafana-dashboard/inferops-vllm",
            "grafana-dashboard/inferops-llama-cpp",
        ]
        return {
            "cluster": request.cluster.to_safe_dict(),
            "observability": {
                "operation": "enable",
                "namespace": request.cluster.namespace,
                "resources": resources,
                "releases": releases,
            },
        }

    def observability_setup(
        self, request: ObservabilitySetupRequest
    ) -> dict[str, Any]:
        """Install the monitoring stack and enable InferOps observability."""
        install = self.observability_install(
            ObservabilityInstallRequest(
                cluster=ClusterTarget(
                    namespace=request.monitoring_namespace,
                    context=request.cluster.context,
                    kubeconfig=request.cluster.kubeconfig,
                ),
                release=request.release,
                chart=request.chart,
                chart_version=request.chart_version,
                grafana_admin_password=request.grafana_admin_password,
                skip_repo_update=request.skip_repo_update,
            )
        )
        enable = self.observability_enable(
            ObservabilityEnableRequest(
                cluster=request.cluster,
                charts_dir=request.charts_dir,
            )
        )
        return {
            "cluster": request.cluster.to_safe_dict(),
            "observability": {
                "operation": "setup",
                "namespace": request.cluster.namespace,
                "monitoringNamespace": request.monitoring_namespace,
                "stack": install["observability"],
                "inferops": enable["observability"],
                "resources": (
                    install["observability"]["resources"]
                    + enable["observability"]["resources"]
                ),
            },
        }


def _build_crd_apply_command(
    crds_dir: Path,
    request: InstallRequest | UpgradeRequest,
) -> list[str]:
    command = _kubectl_command(request)
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


def _build_crd_delete_command(request: UninstallRequest) -> list[str]:
    command = _kubectl_command(request)
    command.extend(("delete", "crd", *CRD_NAMES, "--ignore-not-found"))
    return command


def _validate_upgrade_request(request: UpgradeRequest) -> None:
    _validate_image_tag(request.tag)
    if request.component == "dashboard" and not request.include_dashboard:
        raise CLIError("--component dashboard cannot be used with --skip-dashboard")
    release_tags = _upgrade_release_tags(request)
    if request.enable_observability and not (
        {"inferops-operator", "inferops-gateway"} & set(release_tags)
    ):
        raise CLIError(
            "--enable-observability requires upgrading operator or gateway"
        )


def _validate_upgrade_image_repositories(
    request: UpgradeRequest, release_tags: dict[str, str]
) -> None:
    if "inferops-operator" in release_tags:
        _validate_image_repository(
            "operator image repository",
            request.operator_image_repository,
        )
    if "inferops-gateway" in release_tags:
        _validate_image_repository(
            "gateway image repository",
            request.gateway_image_repository,
        )
    if "inferops-dashboard" in release_tags:
        _validate_image_repository(
            "dashboard image repository",
            request.dashboard_image_repository,
        )


def _upgrade_release_tags(request: UpgradeRequest) -> dict[str, str]:
    if request.component is not None:
        return {_component_release_name(request.component): request.tag}
    release_tags = {
        "inferops-operator": request.tag,
        "inferops-gateway": request.tag,
    }
    if request.include_dashboard:
        release_tags["inferops-dashboard"] = request.tag
    return release_tags


def _component_release_name(component: str) -> str:
    if component == "operator":
        return "inferops-operator"
    if component == "gateway":
        return "inferops-gateway"
    if component == "dashboard":
        return "inferops-dashboard"
    raise CLIError(f"unsupported upgrade component: {component}")


def _release_deployment_name(release_name: str) -> str:
    if release_name == "inferops-operator":
        return "inferops-operator"
    if release_name == "inferops-gateway":
        return "inferops-gateway"
    if release_name == "inferops-dashboard":
        return "inferops-dashboard"
    raise CLIError(f"unsupported control-plane release: {release_name}")


def _kubectl_command(
    request: InstallRequest | UpgradeRequest | UninstallRequest,
) -> list[str]:
    command = ["kubectl"]
    if request.cluster.kubeconfig:
        command.extend(("--kubeconfig", request.cluster.kubeconfig))
    if request.cluster.context:
        command.extend(("--context", request.cluster.context))
    return command


def _helm_cluster_options(command: list[str], cluster: ClusterTarget) -> None:
    if cluster.kubeconfig:
        command.extend(("--kubeconfig", cluster.kubeconfig))
    if cluster.context:
        command.extend(("--kube-context", cluster.context))


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
    _helm_cluster_options(command, request.cluster)

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
    _helm_cluster_options(command, request.cluster)

    if release_name == "inferops-operator":
        command.extend(
            (
                "--set-string",
                "image.repository="
                + _escape_helm_string(request.operator_image_repository),
                "--set-string",
                "image.tag="
                + _escape_helm_string(
                    _upgrade_release_tags(request)["inferops-operator"]
                ),
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
    elif release_name == "inferops-gateway":
        command.extend(
            (
                "--set-string",
                "image.repository="
                + _escape_helm_string(request.gateway_image_repository),
                "--set-string",
                "image.tag="
                + _escape_helm_string(
                    _upgrade_release_tags(request)["inferops-gateway"]
                ),
            )
        )
        if request.enable_observability:
            command.extend(("--set", "serviceMonitor.enabled=true"))
    elif release_name == "inferops-dashboard":
        command.extend(
            (
                "--set-string",
                "image.repository="
                + _escape_helm_string(request.dashboard_image_repository),
                "--set-string",
                "image.tag="
                + _escape_helm_string(
                    _upgrade_release_tags(request)["inferops-dashboard"]
                ),
            )
        )
    else:
        raise CLIError(f"unsupported control-plane release: {release_name}")
    return command


def _build_uninstall_command(
    release_name: str,
    request: UninstallRequest,
) -> list[str]:
    command = [
        "helm",
        "uninstall",
        release_name,
        "--namespace",
        request.cluster.namespace,
        "--ignore-not-found",
        "--wait",
        "--timeout",
        DEFAULT_TIMEOUT,
    ]
    _helm_cluster_options(command, request.cluster)
    return command


def _build_observability_stack_commands(
    request: ObservabilityInstallRequest,
) -> list[list[str]]:
    repo_name = request.chart.split("/", 1)[0]
    commands: list[list[str]] = []
    if repo_name == "prometheus-community":
        repo_command = [
            "helm",
            "repo",
            "add",
            "prometheus-community",
            "https://prometheus-community.github.io/helm-charts",
            "--force-update",
        ]
        commands.append(repo_command)
        if not request.skip_repo_update:
            update_command = ["helm", "repo", "update", "prometheus-community"]
            commands.append(update_command)

    upgrade_command = [
        "helm",
        "upgrade",
        "--install",
        request.release,
        request.chart,
        "--atomic",
        "--namespace",
        request.cluster.namespace,
        "--create-namespace",
        "--wait",
        "--timeout",
        DEFAULT_TIMEOUT,
        "--set",
        "grafana.sidecar.dashboards.enabled=true",
        "--set-string",
        "grafana.sidecar.dashboards.label=grafana_dashboard",
        "--set-string",
        "grafana.sidecar.dashboards.searchNamespace=ALL",
        "--set",
        "prometheus.prometheusSpec.serviceMonitorSelectorNilUsesHelmValues=false",
        "--set",
        "prometheus.prometheusSpec.podMonitorSelectorNilUsesHelmValues=false",
        "--set-json",
        "prometheus.prometheusSpec.serviceMonitorNamespaceSelector={}",
    ]
    _helm_cluster_options(upgrade_command, request.cluster)
    if request.chart_version:
        upgrade_command.extend(("--version", request.chart_version))
    if request.grafana_admin_password:
        upgrade_command.extend(
            (
                "--set-string",
                "grafana.adminUser=admin",
                "--set-string",
                "grafana.adminPassword="
                + _escape_helm_string(request.grafana_admin_password),
            )
        )
    commands.append(upgrade_command)
    return commands


def _build_observability_enable_command(
    *,
    release_name: str,
    chart_dir: Path,
    cluster: ClusterTarget,
) -> list[str]:
    command = [
        "helm",
        "upgrade",
        release_name,
        str(chart_dir),
        "--namespace",
        cluster.namespace,
        "--reuse-values",
        "--wait",
        "--timeout",
        DEFAULT_TIMEOUT,
        "--set",
        "serviceMonitor.enabled=true",
    ]
    _helm_cluster_options(command, cluster)
    if release_name == "inferops-operator":
        command.extend(("--set", "dashboards.enabled=true"))
    elif release_name != "inferops-gateway":
        raise CLIError(f"unsupported InferOps observability release: {release_name}")
    return command


def _summarize_observability_command(command: list[str]) -> str:
    if command[:3] == ["helm", "repo", "add"]:
        return "helm repo add prometheus-community"
    if command[:3] == ["helm", "repo", "update"]:
        return "helm repo update prometheus-community"
    if command[:3] == ["helm", "upgrade", "--install"]:
        return f"helm upgrade --install {command[3]}"
    return " ".join(command[:3])


def _validate_cache_purge_request(request: UninstallRequest) -> None:
    if not request.cache_path:
        raise CLIError("--purge-cache-files requires --cache-path")
    _validate_cache_root(request.cache_path)
    if not request.cache_node_selector:
        raise CLIError("--purge-cache-files requires --cache-node-selector")
    _parse_node_selector(request.cache_node_selector)
    if request.confirm_cache_purge != "DELETE-CACHE-FILES":
        raise CLIError(
            "--purge-cache-files requires "
            "--confirm-cache-purge DELETE-CACHE-FILES"
        )


def _parse_node_selector(selector: str) -> dict[str, str]:
    labels: dict[str, str] = {}
    for raw_part in selector.split(","):
        part = raw_part.strip()
        if not part:
            raise CLIError("cache purge node selector contains an empty label")
        if "=" not in part or "==" in part:
            raise CLIError(
                "cache purge node selector must use comma-separated key=value labels"
            )
        key, value = part.split("=", 1)
        key = key.strip()
        value = value.strip()
        if not key or not value:
            raise CLIError(
                "cache purge node selector must use comma-separated key=value labels"
            )
        _validate_label_key("cache purge node selector key", key)
        _validate_label_value("cache purge node selector value", value)
        labels[key] = value
    return labels


def _validate_label_key(label: str, value: str) -> None:
    parts = value.split("/", 1)
    if len(parts) == 2:
        _validate_dns_subdomain(label, parts[0])
        name = parts[1]
    else:
        name = value
    if not LABEL_NAME.fullmatch(name):
        raise CLIError(f"{label} is invalid: {value}")


def _validate_label_value(label: str, value: str) -> None:
    if not LABEL_VALUE.fullmatch(value):
        raise CLIError(f"{label} is invalid: {value}")


def _cache_purge_daemonset(
    *,
    namespace: str,
    cache_path: str,
    node_selector: dict[str, str],
) -> dict[str, Any]:
    labels = {
        "app.kubernetes.io/name": "inferops-cache-purge",
        "app.kubernetes.io/component": "maintenance",
    }
    return {
        "apiVersion": "apps/v1",
        "kind": "DaemonSet",
        "metadata": {
            "name": "inferops-cache-purge",
            "namespace": namespace,
            "labels": labels,
        },
        "spec": {
            "selector": {"matchLabels": labels},
            "template": {
                "metadata": {"labels": labels},
                "spec": {
                    "nodeSelector": node_selector,
                    "tolerations": [{"operator": "Exists"}],
                    "containers": [
                        {
                            "name": "purge",
                            "image": "busybox:1.36",
                            "command": [
                                "sh",
                                "-c",
                                (
                                    "find /inferops-cache -mindepth 1 "
                                    "-maxdepth 1 -exec rm -rf -- {} + && "
                                    "sleep 3600"
                                ),
                            ],
                            "securityContext": {
                                "runAsUser": 0,
                                "allowPrivilegeEscalation": False,
                            },
                            "volumeMounts": [
                                {"name": "cache", "mountPath": "/inferops-cache"}
                            ],
                        }
                    ],
                    "volumes": [
                        {
                            "name": "cache",
                            "hostPath": {"path": cache_path, "type": "Directory"},
                        }
                    ],
                },
            },
        },
    }


def _build_kubectl_apply_file_command(
    request: UninstallRequest, manifest_path: Path
) -> list[str]:
    command = _kubectl_command(request)
    command.extend(("apply", "--filename", str(manifest_path)))
    return command


def _build_cache_purge_node_probe_command(request: UninstallRequest) -> list[str]:
    assert request.cache_node_selector is not None
    command = _kubectl_command(request)
    command.extend(
        (
            "get",
            "nodes",
            "--selector",
            request.cache_node_selector,
            "--output",
            "json",
        )
    )
    return command


def _cache_purge_node_names(stdout: str) -> list[str]:
    try:
        payload = json.loads(stdout or "{}")
    except json.JSONDecodeError as exc:
        raise CLIError("failed to parse cache purge node selector response") from exc
    items = payload.get("items", [])
    if not isinstance(items, list):
        raise CLIError("cache purge node selector response is invalid")
    names = []
    for item in items:
        metadata = item.get("metadata", {}) if isinstance(item, dict) else {}
        name = metadata.get("name") if isinstance(metadata, dict) else None
        if isinstance(name, str) and name:
            names.append(name)
    return names


def _build_cache_purge_rollout_command(request: UninstallRequest) -> list[str]:
    command = _kubectl_command(request)
    command.extend(
        (
            "--namespace",
            request.cluster.namespace,
            "rollout",
            "status",
            "daemonset/inferops-cache-purge",
            "--timeout",
            DEFAULT_TIMEOUT,
        )
    )
    return command


def _build_kubectl_delete_file_command(
    request: UninstallRequest, manifest_path: Path
) -> list[str]:
    command = _kubectl_command(request)
    command.extend(
        (
            "delete",
            "--filename",
            str(manifest_path),
            "--ignore-not-found",
            "--wait=true",
        )
    )
    return command


def _summarize_cache_purge_step(
    step: str,
    completed: subprocess.CompletedProcess[str],
) -> dict[str, str]:
    return {
        "step": step,
        "status": "completed",
        "output": completed.stdout.strip(),
    }


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


def _validate_helm_release(label: str, value: str) -> None:
    if len(value) > 53 or not HELM_RELEASE_NAME.fullmatch(value):
        raise CLIError(
            f"{label} must be a valid Helm release name: lowercase DNS label up to 53 characters"
        )


def _validate_chart_reference(chart: str) -> None:
    if (
        not chart
        or len(chart) > 255
        or chart.startswith("-")
        or ".." in chart.split("/")
        or any(ord(character) < 33 for character in chart)
    ):
        raise CLIError("observability chart reference is invalid")


def _validate_chart_version(version: str) -> None:
    if not HELM_CHART_VERSION.fullmatch(version):
        raise CLIError(
            "observability chart version must be printable and must not contain whitespace"
        )


def _validate_grafana_password(password: str) -> None:
    if len(password) < 5 or len(password) > 256 or any(
        ord(character) < 32 for character in password
    ):
        raise CLIError(
            "Grafana admin password must be 5-256 printable characters"
        )


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
