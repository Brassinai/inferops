"""Status command."""


def register(subcommands):
    """Register the status command."""
    parser = subcommands.add_parser("status", help="Show deployment status.")
    parser.set_defaults(handler=run)


def run(_args) -> int:
    """Run the status command."""
    print("status is not implemented yet")
    return 0
