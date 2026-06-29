"""Status command."""

from __future__ import annotations

from .errors import ExitCode, run_with_cli_errors
from .kube import NamedRequest, build_cluster_target, resolve_client
from .options import add_cluster_options
from .output import CommandResult, emit_result


def register(subcommands) -> None:
    """Register the status command."""
    parser = subcommands.add_parser(
        "status",
        help="Show deployment status.",
        description="Show placeholder deployment status through the Kubernetes client boundary.",
    )
    parser.add_argument("name", help="Deployment name.")
    add_cluster_options(parser)
    parser.set_defaults(handler=run)


def run(args, client=None) -> int:
    """Run the status command."""

    def action() -> int:
        cluster = build_cluster_target(args)
        response = resolve_client(args, client).status(NamedRequest(cluster=cluster, name=args.name))
        deployment = response["deployment"]
        emit_result(
            args.output,
            CommandResult(
                summary=f"{deployment['name']} is {deployment['phase']} in namespace {deployment['namespace']}.",
                payload=response,
            ),
        )
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)
