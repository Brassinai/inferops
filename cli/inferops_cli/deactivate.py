"""Deactivate command."""

from __future__ import annotations

from .errors import run_with_cli_errors
from .kube import DeactivationRequest, build_cluster_target, resolve_client
from .lifecycle import (
    lifecycle_exit_code,
    parse_timeout,
    progress_guidance,
    progress_line,
)
from .options import add_cluster_options
from .output import CommandResult, emit_result


def register(subcommands) -> None:
    """Register the deactivate command."""
    parser = subcommands.add_parser(
        "deactivate",
        help="Request deactivation for a deployed model.",
        description="Drain and deactivate a deployment while preserving its model cache.",
    )
    parser.add_argument("name", help="Deployment name.")
    parser.add_argument(
        "--timeout",
        type=parse_timeout,
        default=300,
        metavar="DURATION",
        help="Maximum status wait, for example 30s or 5m. Defaults to 5m.",
    )
    parser.add_argument(
        "--no-wait",
        action="store_true",
        help="Submit the deactivation request without waiting for observed status.",
    )
    add_cluster_options(parser)
    parser.set_defaults(handler=run)


def run(args, client=None) -> int:
    """Run the deactivate command."""

    def action() -> int:
        cluster = build_cluster_target(args)
        response = resolve_client(args, client).deactivate(
            DeactivationRequest(
                cluster=cluster,
                name=args.name,
                wait=not args.no_wait,
                timeout_seconds=args.timeout,
                on_transition=(
                    lambda transition: print(progress_line(transition))
                    if args.output == "text"
                    else None
                ),
            )
        )
        deployment = response["deployment"]
        outcome = response["outcome"]
        emit_result(
            args.output,
            CommandResult(
                summary=(
                    f"Deactivation for {deployment['name']} is {outcome} "
                    f"(phase: {deployment['phase']}); its model cache is preserved."
                ),
                payload=response,
                details=progress_guidance(response),
            ),
        )
        return lifecycle_exit_code(response)

    return run_with_cli_errors(action)
