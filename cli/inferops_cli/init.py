"""Init command."""

from __future__ import annotations

from .errors import ExitCode, run_with_cli_errors
from .output import CommandResult, emit_result


def register(subcommands) -> None:
    """Register the init command."""
    parser = subcommands.add_parser(
        "init",
        help="Create a starter project.",
        description="Create a starter project placeholder for the CLI surface.",
    )
    parser.add_argument(
        "--output",
        "-o",
        choices=("text", "json", "yaml"),
        default="text",
        help="Output format. Defaults to text.",
    )
    parser.set_defaults(handler=run)


def run(args, _client=None) -> int:
    """Run the init command."""

    def action() -> int:
        emit_result(
            args.output,
            CommandResult(
                summary="Init placeholder executed. Project scaffolding is not wired yet.",
                payload={
                    "mode": "placeholder",
                    "message": "Init placeholder executed. Project scaffolding is not wired yet.",
                },
            ),
        )
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)
