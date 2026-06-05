"""Logs command."""


def register(subcommands):
    """Register the logs command."""
    parser = subcommands.add_parser("logs", help="Show deployment logs.")
    parser.add_argument("name", nargs="?", help="Deployment name.")
    parser.set_defaults(handler=run)


def run(args) -> int:
    """Run the logs command."""
    name = args.name or "all"
    print(f"logs is not implemented yet: {name}")
    return 0
