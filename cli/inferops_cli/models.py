"""Models command."""

from __future__ import annotations

from .errors import ExitCode, run_with_cli_errors
from .kube import build_cluster_target, resolve_client
from .options import add_cluster_options
from .output import CommandResult, emit_result


def register(subcommands) -> None:
    """Register the models command."""
    parser = subcommands.add_parser(
        "models",
        help="List deployed models.",
        description="List ModelDeployment resources and their observed state.",
    )
    add_cluster_options(parser)
    parser.set_defaults(handler=run)


def run(args, client=None) -> int:
    """Run the models command."""

    def action() -> int:
        cluster = build_cluster_target(args)
        response = resolve_client(args, client).models(cluster)
        models = response["models"]
        emit_result(
            args.output,
            CommandResult(
                summary=f"Found {len(models)} model deployment(s) in namespace {cluster.namespace}.",
                payload=response,
                details=tuple(
                    f"{model['name']}\t{model['phase']}\t{model['model']}"
                    for model in models
                ),
            ),
        )
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)
