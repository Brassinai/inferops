"""Init command."""


def register(subcommands):
    """Register the init command."""
    parser = subcommands.add_parser("init", help="Create a starter project.")
    parser.set_defaults(handler=run)


def run(_args) -> int:
    """Run the init command."""
    print("init is not implemented yet")
    return 0
