"""Upgrade command."""

from __future__ import annotations

from .errors import ExitCode, run_with_cli_errors
from .kube import UpgradeRequest, build_cluster_target, resolve_client
from .options import add_cluster_options
from .output import CommandResult, emit_result


def register(subcommands) -> None:
    """Register the upgrade command."""
    parser = subcommands.add_parser(
        "upgrade",
        help="Upgrade installed InferOps control-plane images.",
        description=(
            "Upgrade the installed operator and dashboard Helm releases to a new "
            "image tag while reusing existing chart values."
        ),
    )
    parser.add_argument(
        "--tag",
        required=True,
        help="Image tag to apply to the operator and dashboard images.",
    )
    parser.add_argument(
        "--operator-image",
        default="ghcr.io/brassinai/inferops-operator",
        help="Operator image repository without a tag.",
    )
    parser.add_argument(
        "--dashboard-image",
        default="ghcr.io/brassinai/inferops-dashboard",
        help="Dashboard image repository without a tag.",
    )
    parser.add_argument(
        "--skip-dashboard",
        action="store_true",
        help="Upgrade only the operator release.",
    )
    parser.add_argument(
        "--enable-observability",
        action="store_true",
        help="Enable operator ServiceMonitor and Grafana dashboard ConfigMaps.",
    )
    parser.add_argument(
        "--charts-dir",
        help="Path to the InferOps Helm charts. Usually detected automatically.",
    )
    add_cluster_options(parser)
    parser.set_defaults(handler=run)


def run(args, client=None) -> int:
    """Run the upgrade command."""

    def action() -> int:
        cluster = build_cluster_target(args)
        response = resolve_client(args, client).upgrade(
            UpgradeRequest(
                cluster=cluster,
                tag=getattr(args, "tag"),
                operator_image_repository=getattr(args, "operator_image"),
                dashboard_image_repository=getattr(args, "dashboard_image"),
                include_dashboard=not getattr(args, "skip_dashboard", False),
                enable_observability=getattr(args, "enable_observability", False),
                charts_dir=getattr(args, "charts_dir", None),
            )
        )
        upgrade = response["upgrade"]
        emit_result(
            args.output,
            CommandResult(
                summary=(
                    f"InferOps upgraded to image tag {upgrade['tag']} "
                    f"in namespace {upgrade['namespace']}."
                ),
                payload=response,
                details=tuple(upgrade["resources"]),
            ),
        )
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)
