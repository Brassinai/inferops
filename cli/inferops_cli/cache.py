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
        help="Inspect or manage model caches.",
        description="Cache-related cluster inspection and cleanup commands.",
    )
    cache_commands = parser.add_subparsers(dest="cache_command", metavar="command")
    cache_commands.required = True

    list_parser = cache_commands.add_parser(
        "list",
        help="List cache entries.",
        description="List ModelCache objects with observed status and referencing deployments.",
    )
    add_cluster_options(list_parser)
    list_parser.set_defaults(handler=run_list)

    delete_parser = cache_commands.add_parser(
        "delete",
        help="Delete one cache entry.",
        description="Delete one ModelCache. Refuses when referenced by a deployment unless --force is used.",
    )
    delete_parser.add_argument("name", help="Cache name to delete.")
    delete_parser.add_argument(
        "--force",
        action="store_true",
        help="Force deletion even when referenced by a deployment.",
    )
    add_cluster_options(delete_parser)
    delete_parser.set_defaults(handler=run_delete)


def run_list(args, client=None) -> int:
    """Run the cache list command."""

    def action() -> int:
        cluster = build_cluster_target(args)
        response = resolve_client(args, client).cache_list(cluster)
        caches = response["caches"]
        details = []
        for cache in caches:
            line = (
                f"{cache['name']}  {cache['phase']}  "
                f"node={cache.get('node', 'unknown')}  "
                f"path={cache.get('path', '')}  size={cache.get('size', '')}"
            )
            refs = cache.get("referencedBy", [])
            if refs:
                line += f"  referencedBy={','.join(refs)}"
            if not cache.get("referencesKnown", True):
                line += "  referencedBy=unknown"
            details.append(line)
        emit_result(
            args.output,
            CommandResult(
                summary=f"Cache inventory: {len(caches)} entries.",
                payload=response,
                details=tuple(details) or ("no cache entries found",),
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
        refs = cache.get("referencedBy", [])
        summary = (
            f"ModelCache {cache['name']} deleted; node-local files were not modified."
        )
        if refs:
            summary += f" Forced despite references from: {', '.join(refs)}."
        emit_result(
            args.output,
            CommandResult(
                summary=summary,
                payload=response,
            ),
        )
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)
