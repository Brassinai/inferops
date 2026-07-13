"""Gateway command group."""

from __future__ import annotations

import argparse
import re
import subprocess
from collections.abc import Sequence

from .errors import CLIError, ExitCode, run_with_cli_errors
from .kube import ClusterTarget, build_cluster_target
from .options import add_cluster_options

DEFAULT_GATEWAY_SERVICE = "inferops-gateway"
DEFAULT_LOCAL_ADDRESS = "127.0.0.1"
DEFAULT_LOCAL_PORT = 8080
DEFAULT_REMOTE_PORT = 80
DNS_LABEL = re.compile(r"^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$")


def register(subcommands) -> None:
    """Register the gateway command group."""
    parser = subcommands.add_parser(
        "gateway",
        help="Manage the InferOps gateway.",
        description="Manage the InferOps gateway for local access and inspection.",
    )
    gateway_commands = parser.add_subparsers(dest="gateway_command", metavar="command")
    gateway_commands.required = True

    forward_parser = gateway_commands.add_parser(
        "forward",
        help="Forward the gateway Service to localhost.",
        description="Forward the InferOps gateway Service to a local port.",
    )
    forward_parser.add_argument(
        "--service",
        default=DEFAULT_GATEWAY_SERVICE,
        help=f"Gateway Service name. Defaults to {DEFAULT_GATEWAY_SERVICE}.",
    )
    forward_parser.add_argument(
        "--address",
        default=DEFAULT_LOCAL_ADDRESS,
        help=f"Local bind address. Defaults to {DEFAULT_LOCAL_ADDRESS}.",
    )
    forward_parser.add_argument(
        "--local-port",
        type=parse_port,
        default=DEFAULT_LOCAL_PORT,
        help=f"Local port. Defaults to {DEFAULT_LOCAL_PORT}.",
    )
    forward_parser.add_argument(
        "--remote-port",
        type=parse_port,
        default=DEFAULT_REMOTE_PORT,
        help=f"Gateway Service port. Defaults to {DEFAULT_REMOTE_PORT}.",
    )
    add_cluster_options(forward_parser)
    forward_parser.set_defaults(handler=run_forward)


def run_forward(args, runner=None) -> int:
    """Run the gateway forward command."""

    def action() -> int:
        cluster = build_cluster_target(args)
        service = _validate_service_name(getattr(args, "service", DEFAULT_GATEWAY_SERVICE))
        address = _validate_address(getattr(args, "address", DEFAULT_LOCAL_ADDRESS))
        local_port = getattr(args, "local_port", DEFAULT_LOCAL_PORT)
        remote_port = getattr(args, "remote_port", DEFAULT_REMOTE_PORT)
        command = build_forward_command(
            cluster=cluster,
            service=service,
            address=address,
            local_port=local_port,
            remote_port=remote_port,
        )
        print(
            f"Forwarding gateway http://{address}:{local_port} "
            f"to service/{service}:{remote_port} in namespace {cluster.namespace}."
        )
        print("Press Ctrl-C to stop.")
        try:
            completed = (runner or _run_command)(command)
        except FileNotFoundError as exc:
            raise CLIError("kubectl executable not found; install kubectl") from exc
        except subprocess.CalledProcessError as exc:
            detail = (exc.stderr or exc.stdout or "kubectl port-forward failed").strip()
            raise CLIError(detail) from exc
        except KeyboardInterrupt:
            return ExitCode.SUCCESS
        return ExitCode.SUCCESS if completed.returncode == 0 else ExitCode.ERROR

    return run_with_cli_errors(action)


def build_forward_command(
    *,
    cluster: ClusterTarget,
    service: str,
    address: str,
    local_port: int,
    remote_port: int,
) -> list[str]:
    """Build the kubectl command used for gateway port forwarding."""
    command = ["kubectl"]
    if cluster.kubeconfig:
        command.extend(("--kubeconfig", cluster.kubeconfig))
    if cluster.context:
        command.extend(("--context", cluster.context))
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


def parse_port(value: str) -> int:
    """Parse a TCP port number."""
    try:
        port = int(value)
    except ValueError as exc:
        raise argparse.ArgumentTypeError("port must be an integer") from exc
    if port < 1 or port > 65535:
        raise argparse.ArgumentTypeError("port must be between 1 and 65535")
    return port


def _validate_service_name(value: str) -> str:
    service = value.strip()
    if not DNS_LABEL.fullmatch(service):
        raise CLIError(f"gateway service name is invalid: {value}")
    return service


def _validate_address(value: str) -> str:
    address = value.strip()
    if not address:
        raise CLIError("local bind address must not be empty")
    if any(character.isspace() for character in address):
        raise CLIError("local bind address must not contain whitespace")
    return address


def _run_command(command: Sequence[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(command, text=True, check=True)
