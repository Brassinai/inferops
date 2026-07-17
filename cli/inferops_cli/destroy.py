"""Deprecated destroy command wrapper."""

from __future__ import annotations

from .errors import ExitCode, run_with_cli_errors
from .kube import UninstallRequest, build_cluster_target, resolve_client
from .options import add_cluster_options
from .output import CommandResult, emit_result


def register(subcommands) -> None:
    """Register the deprecated destroy command."""
    parser = subcommands.add_parser(
        "destroy",
        help="Deprecated alias for inferops uninstall.",
        description=(
            "Deprecated alias for inferops uninstall. Use uninstall in new "
            "automation."
        ),
    )
    parser.add_argument(
        "--skip-dashboard",
        action="store_true",
        help="Do not uninstall the optional dashboard release.",
    )
    parser.add_argument(
        "--crds",
        action="store_true",
        help=(
            "Also delete InferOps CRDs. This removes all InferOps custom "
            "resources in the cluster, but does not delete node-local model files."
        ),
    )
    add_cluster_options(parser)
    parser.set_defaults(handler=run)


def run(args, client=None) -> int:
    """Run the deprecated destroy command."""

    def action() -> int:
        cluster = build_cluster_target(args)
        response = resolve_client(args, client).uninstall(
            UninstallRequest(
                cluster=cluster,
                include_dashboard=not getattr(args, "skip_dashboard", False),
                delete_crds=getattr(args, "crds", False),
            )
        )
        uninstall = response["uninstall"]
        crd_note = (
            "InferOps CRDs were deleted."
            if uninstall["crdsDeleted"]
            else "InferOps CRDs and node-local model files were preserved."
        )
        emit_result(
            args.output,
            CommandResult(
                summary=(
                    f"InferOps uninstalled from namespace {uninstall['namespace']}. "
                    f"{crd_note}"
                ),
                payload=response,
                details=tuple(uninstall["resources"]),
            ),
        )
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)
