"""Activate command."""

from __future__ import annotations

from .errors import run_with_cli_errors
from .kube import ActivationRequest, build_cluster_target, resolve_client
from .lifecycle import (
    ACTIVATION_POLICIES,
    activation_details,
    activation_transition_line,
    lifecycle_exit_code,
    parse_timeout,
)
from .options import add_cluster_options
from .output import CommandResult, emit_result


def register(subcommands) -> None:
    """Register the activate command."""
    parser = subcommands.add_parser(
        "activate",
        help="Request activation for a deployed model.",
        description="Request activation and report the observed Kubernetes status transition.",
    )
    parser.add_argument("name", help="Deployment name.")
    parser.add_argument(
        "--when-full",
        choices=ACTIVATION_POLICIES,
        help=(
            "Capacity policy for this activation. Replacement is allowed only "
            "when ReplaceOldest or ReplaceLowestPriority is explicitly selected."
        ),
    )
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
        help="Submit the activation request without waiting for observed status.",
    )
    parser.add_argument(
        "--verbose",
        "--debug",
        dest="verbose",
        action="store_true",
        help="Include deeper activation evidence such as conditions, events, and log tails.",
    )
    add_cluster_options(parser)
    parser.set_defaults(handler=run)


def run(args, client=None) -> int:
    """Run the activate command."""

    def action() -> int:
        cluster = build_cluster_target(args)
        verbose = getattr(args, "verbose", False)
        response = resolve_client(args, client).activate(
            ActivationRequest(
                cluster=cluster,
                name=args.name,
                when_full=args.when_full,
                wait=not args.no_wait,
                timeout_seconds=args.timeout,
                verbose=verbose,
                on_transition=(
                    lambda transition: print(activation_transition_line(transition))
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
                    f"Activation for {deployment['name']} is {outcome} "
                    f"(phase: {deployment['phase']})."
                ),
                payload=response,
                details=activation_details(response, verbose=verbose),
            ),
        )
        return lifecycle_exit_code(response)

    return run_with_cli_errors(action)
