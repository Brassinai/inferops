"""CLI entrypoint."""

import argparse

from . import delete, deploy, generate, init, logs, status


def build_parser() -> argparse.ArgumentParser:
    """Build the CLI argument parser."""
    parser = argparse.ArgumentParser(prog="inferops")
    subcommands = parser.add_subparsers(dest="command")
    deploy.register(subcommands)
    generate.register(subcommands)
    init.register(subcommands)
    status.register(subcommands)
    logs.register(subcommands)
    delete.register(subcommands)
    return parser


def main() -> int:
    """Run the CLI."""
    parser = build_parser()
    args = parser.parse_args()
    if not hasattr(args, "handler"):
        parser.print_help()
        return 1
    return args.handler(args)


if __name__ == "__main__":
    raise SystemExit(main())
