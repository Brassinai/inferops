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
        help="Inspect GPU availability and occupancy.",
        description="GPU-related cluster inspection commands.",
    )
    gpu_commands = parser.add_subparsers(dest="gpu_command", metavar="command")
    gpu_commands.required = True

    list_parser = gpu_commands.add_parser(
        "list",
        help="List GPU inventory per node.",
        description="List GPU capacity, occupancy, and availability through the Kubernetes client boundary.",
    )
    add_cluster_options(list_parser)
    list_parser.set_defaults(handler=run_list)


def run_list(args, client=None) -> int:
    """Run the gpu list command."""

    def action() -> int:
        cluster = build_cluster_target(args)
        response = resolve_client(args, client).gpu_list(cluster)
        gpus = response["gpus"]
        details = []
        for gpu in gpus:
            occupied = gpu["occupied"] if gpu["occupied"] is not None else "unknown"
            available = gpu["available"] if gpu["available"] is not None else "unknown"
            line = (
                f"{gpu['node']}  {gpu['resourceName']}  "
                f"capacity={gpu['capacity']}  allocatable={gpu['allocatable']}  "
                f"occupied={occupied}  available={available}"
            )
            if gpu.get("product"):
                line += f"  product={gpu['product']}"
            details.append(line)
        emit_result(
            args.output,
            CommandResult(
                summary=f"GPU inventory: {len(gpus)} node-resource entries.",
                payload=response,
                details=tuple(details) or ("no GPU-capable nodes found",),
            ),
        )
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)
