"""Generate command."""

from __future__ import annotations

import sys

from inferops import render_yaml

from .app_loader import load_app
from .errors import ExitCode, run_with_cli_errors


def register(subcommands):
    """Register the generate command."""
    parser = subcommands.add_parser("generate", help="Generate Kubernetes YAML from an application file.")
    parser.add_argument("app", help="Path to the application file.")
    parser.set_defaults(handler=run)


def run(args) -> int:
    """Run the generate command."""

    def action() -> int:
        app = load_app(args.app)
        sys.stdout.write(render_yaml(app))
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)
