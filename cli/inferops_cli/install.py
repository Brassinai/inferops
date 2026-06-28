"""Install command."""

from __future__ import annotations

from .errors import ExitCode, run_with_cli_errors
from .kube import InstallRequest, build_cluster_target, resolve_client
from .options import add_cluster_options
from .output import CommandResult, emit_result


def register(subcommands) -> None:
    """Register the install command."""
    parser = subcommands.add_parser(
        "install",
        help="Install InferOps into a Kubernetes cluster.",
        description="Install the operator and related resources into the selected cluster.",
    )
    parser.add_argument(
        "--profile",
        choices=("default", "homelab"),
        default="default",
        help="Installation profile. Defaults to default.",
    )
    parser.add_argument(
        "--cache-path",
        help="Optional cache root path for profile-specific configuration.",
    )
    add_cluster_options(parser)
    parser.set_defaults(handler=run)


def run(args, client=None) -> int:
    """Run the install command."""

    def action() -> int:
        cluster = build_cluster_target(args)
        response = resolve_client(args, client).install(
            InstallRequest(
                cluster=cluster,
                profile=getattr(args, "profile", "default"),
                cache_path=getattr(args, "cache_path", None),
            )
        )
        install = response["install"]
        emit_result(
            args.output,
            CommandResult(
                summary=f"Install placeholder prepared for profile {install['profile']} in namespace {install['namespace']}.",
                payload=response,
                details=tuple(install["resources"]),
            ),
        )
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)
