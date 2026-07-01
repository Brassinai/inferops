"""CLI entrypoint."""

from __future__ import annotations

from . import (
    activate,
    cache,
    deactivate,
    delete,
    deploy,
    doctor,
    generate,
    gpu,
    init,
    install,
    logs,
    status,
)
from .errors import run_with_cli_errors
from .parser import CLIArgumentParser


def build_parser() -> CLIArgumentParser:
    """Build the CLI argument parser."""
    parser = CLIArgumentParser(
        prog="inferops",
        description="Deploy and operate OpenAI-compatible inference runtimes on Kubernetes.",
    )
    subcommands = parser.add_subparsers(dest="command", parser_class=CLIArgumentParser)
    subcommands.required = True
    activate.register(subcommands)
    cache.register(subcommands)
    deactivate.register(subcommands)
    delete.register(subcommands)
    deploy.register(subcommands)
    doctor.register(subcommands)
    generate.register(subcommands)
    gpu.register(subcommands)
    init.register(subcommands)
    install.register(subcommands)
    logs.register(subcommands)
    status.register(subcommands)
    return parser


def main(argv: list[str] | None = None) -> int:
    """Run the CLI."""

    def action() -> int:
        parser = build_parser()
        args = parser.parse_args(argv)
        return args.handler(args)

    return run_with_cli_errors(action)


if __name__ == "__main__":
    raise SystemExit(main())
