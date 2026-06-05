"""Delete command."""


def register(subcommands):
    """Register the delete command."""
    parser = subcommands.add_parser("delete", help="Delete a deployment.")
    parser.add_argument("name", help="Deployment name.")
    parser.set_defaults(handler=run)


def run(args) -> int:
    """Run the delete command."""
    print(f"delete is not implemented yet: {args.name}")
    return 0
