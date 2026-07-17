"""Prometheus and Grafana observability command group."""

from __future__ import annotations

import argparse
import json
import re
import socket
import subprocess
import time
from collections.abc import Sequence
from typing import Any

from .errors import CLIError, ExitCode, run_with_cli_errors
from .gateway import parse_port
from .kube import (
    ClusterTarget,
    ObservabilityEnableRequest,
    ObservabilityInstallRequest,
    ObservabilitySetupRequest,
    build_cluster_target,
    resolve_client,
)
from .options import add_cluster_options
from .output import CommandResult, emit_result

DEFAULT_MONITORING_NAMESPACE = "monitoring"
DEFAULT_STACK_RELEASE = "kube-prometheus-stack"
DEFAULT_STACK_CHART = "prometheus-community/kube-prometheus-stack"
DEFAULT_GRAFANA_SELECTOR = "app.kubernetes.io/name=grafana"
DEFAULT_GRAFANA_ADDRESS = "127.0.0.1"
DEFAULT_GRAFANA_LOCAL_PORT = 3000
DEFAULT_GRAFANA_REMOTE_PORT = 80
DNS_LABEL = re.compile(r"^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$")
KUBECTL_TIMEOUT = re.compile(r"^[1-9][0-9]*(?:s|m|h)$")


def register(subcommands) -> None:
    """Register the observability command group."""
    parser = subcommands.add_parser(
        "observability",
        help="Install and open Prometheus/Grafana observability.",
        description=(
            "Manage the optional Prometheus/Grafana stack and InferOps "
            "ServiceMonitor/dashboard resources."
        ),
    )
    commands = parser.add_subparsers(dest="observability_command", metavar="command")
    commands.required = True

    setup_parser = commands.add_parser(
        "setup",
        help="Install the stack and enable InferOps observability resources.",
        description=(
            "Install or upgrade kube-prometheus-stack, then enable InferOps "
            "ServiceMonitor and Grafana dashboard resources."
        ),
    )
    add_stack_options(setup_parser)
    setup_parser.add_argument(
        "--monitoring-namespace",
        default=DEFAULT_MONITORING_NAMESPACE,
        help=f"Prometheus/Grafana namespace. Defaults to {DEFAULT_MONITORING_NAMESPACE}.",
    )
    setup_parser.add_argument(
        "--charts-dir",
        help="Path to the InferOps Helm charts. Usually detected automatically.",
    )
    add_cluster_options(setup_parser)
    setup_parser.set_defaults(handler=run_setup)

    install_parser = commands.add_parser(
        "install",
        help="Install only Prometheus and Grafana.",
        description=(
            "Install or upgrade kube-prometheus-stack without changing InferOps "
            "chart settings."
        ),
    )
    add_stack_options(install_parser)
    add_cluster_options(install_parser)
    install_parser.set_defaults(
        handler=run_install,
        namespace=DEFAULT_MONITORING_NAMESPACE,
    )

    enable_parser = commands.add_parser(
        "enable",
        help="Enable only InferOps observability resources.",
        description=(
            "Enable InferOps ServiceMonitors and Grafana dashboard ConfigMaps "
            "against an existing monitoring stack."
        ),
    )
    enable_parser.add_argument(
        "--charts-dir",
        help="Path to the InferOps Helm charts. Usually detected automatically.",
    )
    add_cluster_options(enable_parser)
    enable_parser.set_defaults(handler=run_enable)

    open_parser = commands.add_parser(
        "open",
        help="Open a local port-forward to Grafana.",
        description="Discover Grafana and forward it to localhost.",
    )
    open_parser.add_argument(
        "--service",
        help="Grafana Service name. When omitted, the command discovers it by label.",
    )
    open_parser.add_argument(
        "--selector",
        default=DEFAULT_GRAFANA_SELECTOR,
        help=f"Label selector used to discover Grafana. Defaults to {DEFAULT_GRAFANA_SELECTOR}.",
    )
    open_parser.add_argument(
        "--address",
        default=DEFAULT_GRAFANA_ADDRESS,
        help=f"Local bind address. Defaults to {DEFAULT_GRAFANA_ADDRESS}.",
    )
    open_parser.add_argument(
        "--local-port",
        type=parse_port,
        default=DEFAULT_GRAFANA_LOCAL_PORT,
        help=f"Preferred local port. Defaults to {DEFAULT_GRAFANA_LOCAL_PORT}.",
    )
    open_parser.add_argument(
        "--remote-port",
        type=parse_port,
        default=DEFAULT_GRAFANA_REMOTE_PORT,
        help=f"Grafana Service port. Defaults to {DEFAULT_GRAFANA_REMOTE_PORT}.",
    )
    open_parser.add_argument(
        "--timeout",
        default="120s",
        help="How long to wait for a Ready Grafana Pod. Defaults to 120s.",
    )
    open_parser.add_argument(
        "--no-reconnect",
        action="store_true",
        help="Exit instead of reconnecting if kubectl port-forward exits cleanly.",
    )
    add_cluster_options(open_parser)
    open_parser.set_defaults(
        handler=run_open,
        namespace=DEFAULT_MONITORING_NAMESPACE,
    )


def add_stack_options(parser: argparse.ArgumentParser) -> None:
    """Add kube-prometheus-stack options."""
    parser.add_argument(
        "--release",
        default=DEFAULT_STACK_RELEASE,
        help=f"Helm release name. Defaults to {DEFAULT_STACK_RELEASE}.",
    )
    parser.add_argument(
        "--chart",
        default=DEFAULT_STACK_CHART,
        help=f"Helm chart reference. Defaults to {DEFAULT_STACK_CHART}.",
    )
    parser.add_argument(
        "--chart-version",
        help="Optional kube-prometheus-stack chart version.",
    )
    parser.add_argument(
        "--grafana-admin-password",
        help=(
            "Explicit Grafana admin password. Useful for homelab/dev; otherwise "
            "read the chart-created Secret."
        ),
    )
    parser.add_argument(
        "--skip-repo-update",
        action="store_true",
        help="Skip 'helm repo update prometheus-community'.",
    )


def run_setup(args, client=None) -> int:
    """Run the full observability setup command."""

    def action() -> int:
        cluster = build_cluster_target(args)
        response = resolve_client(args, client).observability_setup(
            ObservabilitySetupRequest(
                cluster=cluster,
                monitoring_namespace=getattr(
                    args,
                    "monitoring_namespace",
                    DEFAULT_MONITORING_NAMESPACE,
                ),
                release=getattr(args, "release", DEFAULT_STACK_RELEASE),
                chart=getattr(args, "chart", DEFAULT_STACK_CHART),
                chart_version=getattr(args, "chart_version", None),
                grafana_admin_password=getattr(args, "grafana_admin_password", None),
                skip_repo_update=getattr(args, "skip_repo_update", False),
                charts_dir=getattr(args, "charts_dir", None),
            )
        )
        observability = response["observability"]
        emit_result(
            args.output,
            CommandResult(
                summary=(
                    "Observability setup complete: Grafana/Prometheus in "
                    f"namespace {observability['monitoringNamespace']}, InferOps "
                    f"resources in namespace {observability['namespace']}."
                ),
                payload=response,
                details=tuple(observability["resources"]),
            ),
        )
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)


def run_install(args, client=None) -> int:
    """Run the observability stack install command."""

    def action() -> int:
        cluster = build_cluster_target(args)
        response = resolve_client(args, client).observability_install(
            ObservabilityInstallRequest(
                cluster=cluster,
                release=getattr(args, "release", DEFAULT_STACK_RELEASE),
                chart=getattr(args, "chart", DEFAULT_STACK_CHART),
                chart_version=getattr(args, "chart_version", None),
                grafana_admin_password=getattr(args, "grafana_admin_password", None),
                skip_repo_update=getattr(args, "skip_repo_update", False),
            )
        )
        observability = response["observability"]
        emit_result(
            args.output,
            CommandResult(
                summary=(
                    "Prometheus/Grafana installed in namespace "
                    f"{observability['monitoringNamespace']}."
                ),
                payload=response,
                details=tuple(observability["resources"]),
            ),
        )
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)


def run_enable(args, client=None) -> int:
    """Run the InferOps observability enable command."""

    def action() -> int:
        cluster = build_cluster_target(args)
        response = resolve_client(args, client).observability_enable(
            ObservabilityEnableRequest(
                cluster=cluster,
                charts_dir=getattr(args, "charts_dir", None),
            )
        )
        observability = response["observability"]
        emit_result(
            args.output,
            CommandResult(
                summary=(
                    "InferOps observability resources enabled in namespace "
                    f"{observability['namespace']}."
                ),
                payload=response,
                details=tuple(observability["resources"]),
            ),
        )
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)


def run_open(args, runner=None, sleep=time.sleep) -> int:
    """Run the interactive Grafana port-forward command."""

    def action() -> int:
        cluster = build_cluster_target(args)
        address = _validate_address(getattr(args, "address", DEFAULT_GRAFANA_ADDRESS))
        timeout = _validate_timeout(getattr(args, "timeout", "120s"))
        service = getattr(args, "service", None)
        if service:
            service = _validate_dns_label(service, "Grafana Service name")
        else:
            service = discover_grafana_service(
                cluster=cluster,
                selector=getattr(args, "selector", DEFAULT_GRAFANA_SELECTOR),
                runner=runner,
            )
        local_port = find_available_port(
            address,
            getattr(args, "local_port", DEFAULT_GRAFANA_LOCAL_PORT),
        )
        wait_for_grafana_pod(
            cluster=cluster,
            service=service,
            timeout=timeout,
            runner=runner,
        )
        command = build_grafana_forward_command(
            cluster=cluster,
            service=service,
            address=address,
            local_port=local_port,
            remote_port=getattr(args, "remote_port", DEFAULT_GRAFANA_REMOTE_PORT),
        )
        url = f"http://{address}:{local_port}"
        print(f"Grafana is available at {url}")
        print("Login user: admin")
        print(
            "Password: use --grafana-admin-password from setup/install, or read "
            f"the chart Secret in namespace {cluster.namespace}."
        )
        print("Press Ctrl-C to stop.")
        while True:
            try:
                completed = (runner or _run_command)(command)
            except FileNotFoundError as exc:
                raise CLIError("kubectl executable not found; install kubectl") from exc
            except subprocess.CalledProcessError as exc:
                raise _grafana_port_forward_error(exc, cluster, service) from exc
            except KeyboardInterrupt:
                return ExitCode.SUCCESS
            if getattr(args, "no_reconnect", False):
                return ExitCode.SUCCESS if completed.returncode == 0 else ExitCode.ERROR
            print("Grafana port-forward exited; reconnecting...")
            sleep(2)

    return run_with_cli_errors(action)


def discover_grafana_service(
    *,
    cluster: ClusterTarget,
    selector: str,
    runner=None,
) -> str:
    """Discover the Grafana Service by label."""
    if not selector.strip() or any(character.isspace() for character in selector):
        raise CLIError("Grafana selector must be a non-empty Kubernetes label selector")
    command = _kubectl_base(cluster)
    command.extend(
        (
            "--namespace",
            cluster.namespace,
            "get",
            "services",
            "--selector",
            selector,
            "--output",
            "json",
        )
    )
    try:
        completed = (runner or _run_command)(command)
    except FileNotFoundError as exc:
        raise CLIError("kubectl executable not found; install kubectl") from exc
    except subprocess.CalledProcessError as exc:
        detail = (exc.stderr or exc.stdout or "kubectl get services failed").strip()
        raise CLIError(
            "could not discover Grafana Service. Check the Kubernetes context, "
            f"namespace '{cluster.namespace}', RBAC for services, and selector "
            f"{selector!r}. Details: {detail}"
        ) from exc
    payload = _load_json_output(completed.stdout, "Grafana Service discovery")
    items = payload.get("items", [])
    if not items:
        raise CLIError(
            "no Grafana Service found. Check the monitoring namespace, Helm "
            f"release labels, or pass --service explicitly (namespace: {cluster.namespace}, "
            f"selector: {selector})."
        )
    services = sorted(
        item.get("metadata", {}).get("name", "")
        for item in items
        if item.get("metadata", {}).get("name")
    )
    if not services:
        raise CLIError("Grafana Service discovery returned objects without names")
    if len(services) > 1:
        raise CLIError(
            "multiple Grafana Services matched selector "
            f"{selector!r}: {', '.join(services)}. Pass --service explicitly."
        )
    return _validate_dns_label(services[0], "Grafana Service name")


def wait_for_grafana_pod(
    *,
    cluster: ClusterTarget,
    service: str,
    timeout: str,
    runner=None,
) -> None:
    """Wait for at least one Grafana Pod selected by the Service to be Ready."""
    service_json = _read_service(cluster=cluster, service=service, runner=runner)
    selector = service_json.get("spec", {}).get("selector", {})
    if not selector:
        raise CLIError(
            f"Grafana Service '{service}' has no Pod selector. Pass the real "
            "Grafana Service name or check customized chart settings."
        )
    selector_text = ",".join(f"{key}={value}" for key, value in sorted(selector.items()))
    deadline = time.monotonic() + _timeout_seconds(timeout)
    last_detail = "no Grafana Pods matched the Service selector"
    while True:
        command = _kubectl_base(cluster)
        command.extend(
            (
                "--namespace",
                cluster.namespace,
                "get",
                "pods",
                "--selector",
                selector_text,
                "--output",
                "json",
            )
        )
        try:
            completed = (runner or _run_command)(command)
        except FileNotFoundError as exc:
            raise CLIError("kubectl executable not found; install kubectl") from exc
        except subprocess.CalledProcessError as exc:
            detail = (exc.stderr or exc.stdout or "kubectl get pods failed").strip()
            raise CLIError(
                "could not inspect Grafana Pods. Check the monitoring namespace, "
                "Helm release status, Pod events, and RBAC for pods. Details: "
                + detail
            ) from exc

        payload = _load_json_output(completed.stdout, "Grafana Pod readiness")
        items = payload.get("items", [])
        if any(_pod_is_ready(item) for item in items):
            return
        last_detail = _summarize_pod_readiness(items)
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise CLIError(
                "Grafana Pod is not Ready. Check the monitoring namespace, Helm "
                "release status, Pod events, and RBAC for pods. Details: "
                + last_detail
            )
        time.sleep(min(2, remaining))


def build_grafana_forward_command(
    *,
    cluster: ClusterTarget,
    service: str,
    address: str,
    local_port: int,
    remote_port: int,
) -> list[str]:
    """Build the kubectl port-forward command for Grafana."""
    command = _kubectl_base(cluster)
    command.extend(
        (
            "--namespace",
            cluster.namespace,
            "port-forward",
            "--address",
            address,
            f"svc/{service}",
            f"{local_port}:{remote_port}",
        )
    )
    return command


def find_available_port(address: str, preferred_port: int) -> int:
    """Return preferred_port if free, otherwise the next free local TCP port."""
    for port in range(preferred_port, 65536):
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            try:
                sock.bind((address, port))
            except OSError:
                continue
            return port
    raise CLIError(f"no free local port found at or above {preferred_port}")


def _timeout_seconds(timeout: str) -> int:
    unit = timeout[-1]
    value = int(timeout[:-1])
    if unit == "s":
        return value
    if unit == "m":
        return value * 60
    return value * 60 * 60


def _pod_is_ready(item: Any) -> bool:
    if not isinstance(item, dict):
        return False
    metadata = item.get("metadata", {})
    status = item.get("status", {})
    if metadata.get("deletionTimestamp") or status.get("phase") != "Running":
        return False
    for condition in status.get("conditions", []):
        if condition.get("type") == "Ready" and condition.get("status") == "True":
            return True
    return False


def _summarize_pod_readiness(items: Any) -> str:
    if not isinstance(items, list) or not items:
        return "no Grafana Pods matched the Service selector"
    summaries = []
    for item in items[:5]:
        metadata = item.get("metadata", {}) if isinstance(item, dict) else {}
        status = item.get("status", {}) if isinstance(item, dict) else {}
        name = metadata.get("name", "unknown")
        phase = status.get("phase", "Unknown")
        reason = status.get("reason", "")
        ready = "Ready" if _pod_is_ready(item) else "NotReady"
        bits = [str(name), str(phase), ready]
        if reason:
            bits.append(str(reason))
        summaries.append("/".join(bits))
    if len(items) > 5:
        summaries.append(f"... {len(items) - 5} more")
    return "; ".join(summaries)


def _read_service(*, cluster: ClusterTarget, service: str, runner=None) -> dict[str, Any]:
    command = _kubectl_base(cluster)
    command.extend(
        (
            "--namespace",
            cluster.namespace,
            "get",
            "service",
            service,
            "--output",
            "json",
        )
    )
    try:
        completed = (runner or _run_command)(command)
    except FileNotFoundError as exc:
        raise CLIError("kubectl executable not found; install kubectl") from exc
    except subprocess.CalledProcessError as exc:
        detail = (exc.stderr or exc.stdout or "kubectl get service failed").strip()
        raise CLIError(
            f"could not read Grafana Service '{service}' in namespace "
            f"'{cluster.namespace}'. Check namespace, Service name, and RBAC for "
            f"services. Details: {detail}"
        ) from exc
    return _load_json_output(completed.stdout, "Grafana Service")


def _grafana_port_forward_error(
    exc: subprocess.CalledProcessError,
    cluster: ClusterTarget,
    service: str,
) -> CLIError:
    detail = (exc.stderr or exc.stdout or "kubectl port-forward failed").strip()
    return CLIError(
        "Grafana port-forward failed. Check the Kubernetes context, monitoring "
        f"namespace '{cluster.namespace}', Service '{service}', RBAC for "
        "services/pods/port-forward, local port conflicts, and WSL localhost "
        f"forwarding. Details: {detail}"
    )


def _load_json_output(output: str, label: str) -> dict[str, Any]:
    try:
        payload = json.loads(output or "{}")
    except json.JSONDecodeError as exc:
        raise CLIError(f"{label} returned invalid JSON") from exc
    if not isinstance(payload, dict):
        raise CLIError(f"{label} returned unexpected JSON")
    return payload


def _validate_dns_label(value: str, label: str) -> str:
    stripped = value.strip()
    if not DNS_LABEL.fullmatch(stripped):
        raise CLIError(f"{label} is invalid: {value}")
    return stripped


def _validate_address(value: str) -> str:
    address = value.strip()
    if not address:
        raise CLIError("local bind address must not be empty")
    if any(character.isspace() for character in address):
        raise CLIError("local bind address must not contain whitespace")
    return address


def _validate_timeout(value: str) -> str:
    timeout = value.strip()
    if not KUBECTL_TIMEOUT.fullmatch(timeout):
        raise CLIError(
            "Grafana readiness timeout must be a positive duration such as "
            "120s, 5m, or 1h"
        )
    return timeout


def _kubectl_base(cluster: ClusterTarget) -> list[str]:
    command = ["kubectl"]
    if cluster.kubeconfig:
        command.extend(("--kubeconfig", cluster.kubeconfig))
    if cluster.context:
        command.extend(("--context", cluster.context))
    return command


def _run_command(command: Sequence[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(command, text=True, check=True, capture_output=True)
