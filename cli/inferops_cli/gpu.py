"""GPU command group."""

from __future__ import annotations

from .errors import ExitCode, run_with_cli_errors
from .kube import build_cluster_target, resolve_client
from .options import add_cluster_options
from .output import CommandResult, emit_result


def register(subcommands) -> None:
    """Register the gpu command group."""
    parser = subcommands.add_parser(
        "gpu",
        help="Inspect GPU availability and placeholder inventory.",
        description="GPU-related cluster inspection commands.",
    )
    gpu_commands = parser.add_subparsers(dest="gpu_command", metavar="command")
    gpu_commands.required = True

    list_parser = gpu_commands.add_parser(
        "list",
        help="List GPU inventory.",
        description="List placeholder GPU inventory through the Kubernetes client boundary.",
    )
    add_cluster_options(list_parser)
    list_parser.set_defaults(handler=run_list)


def run_list(args, client=None) -> int:
    """Run the gpu list command."""

    def action() -> int:
        cluster = build_cluster_target(args)
        response = resolve_client(args, client).gpu_list(cluster)
        gpus = response["gpus"]
        details = tuple(
            f"{gpu['name']} {gpu['vendor']} {gpu['product']} {gpu['status']}" for gpu in gpus
        )
        emit_result(
            args.output,
            CommandResult(
                summary="GPU inventory placeholder returned from the fake Kubernetes client.",
                payload=response,
                details=details or ("no gpu inventory available in placeholder mode",),
            ),
        )
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)
