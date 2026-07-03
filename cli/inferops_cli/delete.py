"""Delete command."""

from __future__ import annotations

from .errors import ExitCode, run_with_cli_errors
from .kube import NamedRequest, build_cluster_target, resolve_client
from .options import add_cluster_options
from .output import CommandResult, emit_result


def register(subcommands) -> None:
    """Register the delete command."""
    parser = subcommands.add_parser(
        "delete",
        help="Delete a deployment.",
        description="Delete a ModelDeployment and managed runtime resources. Model caches are preserved.",
    )
    parser.add_argument("name", help="Deployment name.")
    add_cluster_options(parser)
    parser.set_defaults(handler=run)


def run(args, client=None) -> int:
    """Run the delete command."""

    def action() -> int:
        cluster = build_cluster_target(args)
        response = resolve_client(args, client).delete(NamedRequest(cluster=cluster, name=args.name))
        deployment = response["deployment"]
        emit_result(
            args.output,
            CommandResult(
                summary=(
                    f"Deleted {deployment['name']} from namespace "
                    f"{deployment['namespace']}; its model cache was preserved."
                ),
                payload=response,
            ),
        )
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)
