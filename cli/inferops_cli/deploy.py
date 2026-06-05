"""Deploy command."""


def register(subcommands):
    """Register the deploy command."""
    parser = subcommands.add_parser("deploy", help="Deploy an application file.")
    parser.add_argument("app", help="Path to the application file.")
    parser.set_defaults(handler=run)


def run(args) -> int:
    """Run the deploy command."""
    print(f"deploy is not implemented yet: {args.app}")
    return 0
