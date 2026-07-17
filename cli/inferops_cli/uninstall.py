"""Uninstall command."""

from __future__ import annotations

from .errors import ExitCode, run_with_cli_errors
from .kube import UninstallRequest, build_cluster_target, resolve_client
from .options import add_cluster_options
from .output import CommandResult, emit_result


def register(subcommands) -> None:
    """Register the uninstall command."""
    parser = subcommands.add_parser(
        "uninstall",
        help="Uninstall the InferOps platform from a Kubernetes cluster.",
        description=(
            "Uninstall InferOps Helm releases from the selected cluster. CRDs, "
            "ModelDeployment/ModelCache records, and node-local cache files are "
            "preserved unless explicit destructive flags are used."
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
    parser.add_argument(
        "--purge-cache-files",
        action="store_true",
        help=(
            "Delete model files under --cache-path on nodes matched by "
            "--cache-node-selector. Requires --confirm-cache-purge."
        ),
    )
    parser.add_argument(
        "--cache-path",
        help="Host cache path to purge when --purge-cache-files is used.",
    )
    parser.add_argument(
        "--cache-node-selector",
        help=(
            "Comma-separated node labels such as inferops.dev/cache=true. "
            "Required when --purge-cache-files is used."
        ),
    )
    parser.add_argument(
        "--confirm-cache-purge",
        metavar="DELETE-CACHE-FILES",
        help=(
            "Confirmation token required with --purge-cache-files. The value "
            "must be exactly DELETE-CACHE-FILES."
        ),
    )
    add_cluster_options(parser)
    parser.set_defaults(handler=run)


def run(args, client=None) -> int:
    """Run the uninstall command."""

    def action() -> int:
        cluster = build_cluster_target(args)
        response = resolve_client(args, client).uninstall(
            UninstallRequest(
                cluster=cluster,
                include_dashboard=not getattr(args, "skip_dashboard", False),
                delete_crds=bool(getattr(args, "crds", False)),
                purge_cache_files=bool(getattr(args, "purge_cache_files", False)),
                cache_path=getattr(args, "cache_path", None),
                cache_node_selector=getattr(args, "cache_node_selector", None),
                confirm_cache_purge=getattr(args, "confirm_cache_purge", None),
            )
        )
        uninstall = response["uninstall"]
        notes = []
        if uninstall["crdsDeleted"]:
            notes.append("InferOps CRDs and custom resources were deleted.")
        else:
            notes.append("InferOps CRDs and custom resources were preserved.")
        if uninstall["cacheFilesDeleted"]:
            notes.append("Node-local cache files were purged.")
        else:
            notes.append("Node-local cache files were preserved.")
        emit_result(
            args.output,
            CommandResult(
                summary=(
                    f"InferOps uninstalled from namespace {uninstall['namespace']}. "
                    + " ".join(notes)
                ),
                payload=response,
                details=tuple(uninstall["resources"]),
            ),
        )
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)
