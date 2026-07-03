"""Endpoints command."""

from __future__ import annotations

from .errors import ExitCode, run_with_cli_errors
from .kube import build_cluster_target, resolve_client
from .options import add_cluster_options
from .output import CommandResult, emit_result


def register(subcommands) -> None:
    """Register the endpoints command."""
    parser = subcommands.add_parser(
        "endpoints",
        help="List model endpoints.",
        description="List stable gateway endpoints and their readiness state.",
    )
    add_cluster_options(parser)
    parser.set_defaults(handler=run)


def run(args, client=None) -> int:
    """Run the endpoints command."""

    def action() -> int:
        cluster = build_cluster_target(args)
        response = resolve_client(args, client).endpoints(cluster)
        endpoints = response["endpoints"]
        emit_result(
            args.output,
            CommandResult(
                summary=f"Found {len(endpoints)} model endpoint(s) in namespace {cluster.namespace}.",
                payload=response,
                details=tuple(
                    f"{endpoint['name']}\t{endpoint['phase']}\t{endpoint['endpoint']}"
                    for endpoint in endpoints
                ),
            ),
        )
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)
