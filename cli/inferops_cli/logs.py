"""Logs command."""

from __future__ import annotations

from .errors import ExitCode, run_with_cli_errors
from .kube import LogsRequest, build_cluster_target, resolve_client
from .lifecycle import parse_positive_integer
from .options import add_cluster_options
from .output import CommandResult, emit_result


def register(subcommands) -> None:
    """Register the logs command."""
    parser = subcommands.add_parser(
        "logs",
        help="Show deployment logs.",
        description="Show logs from the deployment's current runtime Pod.",
    )
    parser.add_argument("name", help="Deployment name.")
    parser.add_argument(
        "--tail",
        type=parse_positive_integer,
        default=20,
        help="Maximum number of log lines to print. Defaults to 20.",
    )
    add_cluster_options(parser)
    parser.set_defaults(handler=run)


def run(args, client=None) -> int:
    """Run the logs command."""

    def action() -> int:
        cluster = build_cluster_target(args)
        response = resolve_client(args, client).logs(
            LogsRequest(cluster=cluster, name=args.name, tail=getattr(args, "tail", 20))
        )
        emit_result(
            args.output,
            CommandResult(
                summary=f"Logs for {args.name} in namespace {cluster.namespace}.",
                payload=response,
                details=tuple(response["lines"]),
            ),
        )
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)
