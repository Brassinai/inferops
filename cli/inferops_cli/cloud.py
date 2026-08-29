"""Cloud mesh connection command group."""

from __future__ import annotations

import os
import sys

from .errors import CLIError, ExitCode, run_with_cli_errors
from .kube import (
    CloudConnectRequest,
    CloudDisconnectRequest,
    CloudStatusRequest,
    build_cluster_target,
    resolve_client,
)
from .lifecycle import parse_timeout
from .options import add_cluster_options
from .output import CommandResult, emit_result


def register(subcommands) -> None:
    """Register the cloud command group."""
    parser = subcommands.add_parser(
        "cloud",
        help="Manage InferOps cloud mesh connectivity.",
        description="Connect, inspect, and disconnect an InferOps cluster from a cloud mesh.",
    )
    cloud_commands = parser.add_subparsers(dest="cloud_command", metavar="command")
    cloud_commands.required = True

    connect_parser = cloud_commands.add_parser(
        "connect",
        help="Connect this InferOps cluster to a cloud mesh.",
        description="Redeem a bootstrap token and configure persistent cloud mesh connectivity.",
    )
    token_group = connect_parser.add_mutually_exclusive_group()
    token_group.add_argument(
        "--token",
        help="Short-lived cloud bootstrap token. Prefer --token-stdin for shell history safety.",
    )
    token_group.add_argument(
        "--token-stdin",
        action="store_true",
        help="Read the short-lived cloud bootstrap token from stdin.",
    )
    connect_parser.add_argument(
        "--cloud-api-url",
        help="Cloud API URL. Defaults to INFEROPS_CLOUD_API_URL or https://cloud.inferops.dev.",
    )
    connect_parser.add_argument(
        "--cluster-name",
        help="Optional provider cluster display name sent during token redemption.",
    )
    connect_parser.add_argument(
        "--charts-dir",
        help="Path to InferOps Helm charts. Usually detected automatically.",
    )
    connect_parser.add_argument(
        "--cloud-agent-image",
        help=(
            "Cloud agent image reference. Defaults to the chart image; useful for "
            "local dev images such as inferops-cloud-agent:dev."
        ),
    )
    connect_parser.add_argument(
        "--cloud-agent-image-pull-policy",
        choices=("Always", "IfNotPresent", "Never"),
        help=(
            "Kubernetes image pull policy for the cloud agent. Local dev HTTP cloud "
            "URLs default to Never so a locally built image is used."
        ),
    )
    connect_parser.add_argument(
        "--benchmark-offerings",
        action="store_true",
        help=(
            "Let the cloud agent run a tiny one-time OpenAI-compatible benchmark "
            "against newly discovered runtime Services to infer token throughput."
        ),
    )
    connect_parser.add_argument(
        "--no-wait",
        action="store_true",
        help="Do not wait for Helm rollouts to finish.",
    )
    connect_parser.add_argument(
        "--timeout",
        type=parse_timeout,
        default=300,
        metavar="DURATION",
        help="Maximum rollout wait duration, for example 30s or 5m. Defaults to 5m.",
    )
    add_cluster_options(connect_parser)
    connect_parser.set_defaults(handler=run_connect)

    status_parser = cloud_commands.add_parser(
        "status",
        help="Show cloud mesh connection status.",
        description="Report saved config, gateway, mesh exposure, agent, and cloud API status.",
    )
    add_cluster_options(status_parser)
    status_parser.set_defaults(handler=run_status)

    disconnect_parser = cloud_commands.add_parser(
        "disconnect",
        help="Disconnect this InferOps cluster from the cloud mesh.",
        description="Remove cloud connection resources without deleting local inference workloads.",
    )
    disconnect_parser.add_argument(
        "--charts-dir",
        help="Path to InferOps Helm charts. Usually detected automatically.",
    )
    disconnect_parser.add_argument(
        "--disable-mesh-exposure",
        action="store_true",
        help="Also disable the InferOps gateway mesh exposure managed by connect.",
    )
    add_cluster_options(disconnect_parser)
    disconnect_parser.set_defaults(handler=run_disconnect)


def run_connect(args, client=None) -> int:
    """Run cloud connect."""

    def action() -> int:
        token = _read_token(args)
        response = resolve_client(args, client).cloud_connect(
            CloudConnectRequest(
                cluster=build_cluster_target(args),
                token=token,
                cloud_api_url=getattr(args, "cloud_api_url", None),
                cluster_name=getattr(args, "cluster_name", None),
                charts_dir=getattr(args, "charts_dir", None),
                cloud_agent_image=getattr(args, "cloud_agent_image", None),
                cloud_agent_image_pull_policy=getattr(
                    args, "cloud_agent_image_pull_policy", None
                ),
                benchmark_offerings=getattr(args, "benchmark_offerings", False),
                wait=not getattr(args, "no_wait", False),
                timeout_seconds=getattr(args, "timeout", 300),
            )
        )
        connection = response["connection"]
        emit_result(
            args.output,
            CommandResult(
                summary=(
                    "Cloud mesh connected for provider cluster "
                    f"{connection['providerClusterId']}."
                ),
                payload=response,
                details=tuple(response.get("steps", [])),
            ),
        )
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)


def run_status(args, client=None) -> int:
    """Run cloud status."""

    def action() -> int:
        response = resolve_client(args, client).cloud_status(
            CloudStatusRequest(cluster=build_cluster_target(args))
        )
        checks = response["checks"]
        details = []
        has_fail = False
        for check in checks:
            status = check["status"]
            if status == "FAIL":
                has_fail = True
            icon = {"PASS": "OK", "WARN": "WARN", "FAIL": "FAIL"}.get(status, status)
            details.append(f"{icon} {check['id']}: {check['message']}")
        emit_result(
            args.output,
            CommandResult(
                summary=response["summary"],
                payload=response,
                details=tuple(details),
            ),
        )
        return ExitCode.ERROR if has_fail else ExitCode.SUCCESS

    return run_with_cli_errors(action)


def run_disconnect(args, client=None) -> int:
    """Run cloud disconnect."""

    def action() -> int:
        response = resolve_client(args, client).cloud_disconnect(
            CloudDisconnectRequest(
                cluster=build_cluster_target(args),
                charts_dir=getattr(args, "charts_dir", None),
                disable_mesh_exposure=getattr(args, "disable_mesh_exposure", False),
            )
        )
        emit_result(
            args.output,
            CommandResult(
                summary="Cloud mesh disconnected; local InferOps resources were preserved.",
                payload=response,
                details=tuple(response.get("steps", [])),
            ),
        )
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)


def _read_token(args) -> str:
    if getattr(args, "token_stdin", False):
        token = sys.stdin.read().strip()
    else:
        token = (
            getattr(args, "token", None)
            or os.environ.get("INFEROPS_CLOUD_TOKEN")
            or ""
        ).strip()
    if not token:
        raise CLIError(
            "cloud bootstrap token is required; pass --token, --token-stdin, or INFEROPS_CLOUD_TOKEN"
        )
    return token
