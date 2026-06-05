"""Generate command."""


def register(subcommands):
    """Register the generate command."""
    parser = subcommands.add_parser("generate", help="Generate Kubernetes YAML from an application file.")
    parser.add_argument("app", help="Path to the application file.")
    parser.set_defaults(handler=run)


def run(args) -> int:
    """Run the generate command."""
    print(f"generate is not implemented yet: {args.app}")
    return 0
