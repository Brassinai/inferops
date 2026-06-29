"""Cache command group."""

from __future__ import annotations

from .errors import ExitCode, run_with_cli_errors
from .kube import CacheDeleteRequest, build_cluster_target, resolve_client
from .options import add_cluster_options
from .output import CommandResult, emit_result


def register(subcommands) -> None:
    """Register the cache command group."""
    parser = subcommands.add_parser(
        "cache",
        help="Inspect or manage model cache placeholders.",
        description="Cache-related cluster inspection and cleanup commands.",
    )
    cache_commands = parser.add_subparsers(dest="cache_command", metavar="command")
    cache_commands.required = True

    list_parser = cache_commands.add_parser(
        "list",
        help="List cache entries.",
        description="List placeholder cache entries through the Kubernetes client boundary.",
    )
    add_cluster_options(list_parser)
    list_parser.set_defaults(handler=run_list)

    delete_parser = cache_commands.add_parser(
        "delete",
        help="Delete one cache entry.",
        description="Delete one placeholder cache entry through the Kubernetes client boundary.",
    )
    delete_parser.add_argument("name", help="Deployment name whose cache should be removed.")
    delete_parser.add_argument(
        "--force",
        action="store_true",
        help="Force deletion in the placeholder workflow.",
    )
    add_cluster_options(delete_parser)
    delete_parser.set_defaults(handler=run_delete)


def run_list(args, client=None) -> int:
    """Run the cache list command."""

    def action() -> int:
        cluster = build_cluster_target(args)
        response = resolve_client(args, client).cache_list(cluster)
        caches = response["caches"]
        details = tuple(f"{cache['name']} {cache['status']} {cache['path']}" for cache in caches)
        emit_result(
            args.output,
            CommandResult(
                summary="Cache inventory placeholder returned from the fake Kubernetes client.",
                payload=response,
                details=details or ("no cache entries available in placeholder mode",),
            ),
        )
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)


def run_delete(args, client=None) -> int:
    """Run the cache delete command."""

    def action() -> int:
        cluster = build_cluster_target(args)
        response = resolve_client(args, client).cache_delete(
            CacheDeleteRequest(
                cluster=cluster,
                name=args.name,
                force=bool(getattr(args, "force", False)),
            )
        )
        cache = response["cache"]
        emit_result(
            args.output,
            CommandResult(
                summary=f"Cache placeholder deleted for {cache['name']} in namespace {cache['namespace']}.",
                payload=response,
            ),
        )
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)
