"""Status command."""

from __future__ import annotations

from .errors import ExitCode, run_with_cli_errors
from .kube import StatusRequest, build_cluster_target, resolve_client
from .lifecycle import parse_timeout, progress_guidance, progress_line
from .options import add_cluster_options
from .output import CommandResult, emit_result


def register(subcommands) -> None:
    """Register the status command."""
    parser = subcommands.add_parser(
        "status",
        help="Show deployment status.",
        description="Show observed deployment phase, conditions, placement, and endpoint.",
    )
    parser.add_argument("name", help="Deployment name.")
    parser.add_argument(
        "--watch",
        action="store_true",
        help="Watch observed phase transitions until the deployment reaches a stable state.",
    )
    parser.add_argument(
        "--timeout",
        type=parse_timeout,
        default=300,
        metavar="DURATION",
        help="Maximum watch duration, for example 30s or 5m. Defaults to 5m.",
    )
    add_cluster_options(parser)
    parser.set_defaults(handler=run)


def run(args, client=None) -> int:
    """Run the status command."""

    def action() -> int:
        cluster = build_cluster_target(args)
        response = resolve_client(args, client).status(
            StatusRequest(
                cluster=cluster,
                name=args.name,
                watch=args.watch,
                timeout_seconds=args.timeout,
                on_transition=(
                    lambda transition: print(progress_line(transition))
                    if args.output == "text"
                    else None
                ),
            )
        )
        deployment = response["deployment"]
        condition_lines = tuple(
            _format_condition(condition)
            for condition in deployment.get("conditions", [])
        )
        guidance = progress_guidance(response) if args.watch else ()
        emit_result(
            args.output,
            CommandResult(
                summary=f"{deployment['name']} is {deployment['phase']} in namespace {deployment['namespace']}.",
                payload=response,
                details=guidance + condition_lines,
            ),
        )
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)


def _format_condition(condition: dict) -> str:
    status = condition.get("status", "Unknown")
    reason = condition.get("reason", "")
    message = condition.get("message", "")
    detail = ": ".join(part for part in (reason, message) if part)
    return (
        f"{condition.get('type', 'Unknown')}={status}: {detail}"
        if detail
        else f"{condition.get('type', 'Unknown')}={status}"
    )
