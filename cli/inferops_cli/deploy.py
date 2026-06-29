"""Deploy command."""

from __future__ import annotations

from inferops import build_manifests

from .app_loader import load_app
from .errors import ExitCode, run_with_cli_errors
from .kube import DeployRequest, build_cluster_target, resolve_client
from .options import add_cluster_options
from .output import CommandResult, emit_result


def register(subcommands) -> None:
    """Register the deploy command."""
    parser = subcommands.add_parser(
        "deploy",
        help="Deploy an application file.",
        description="Load an InferOps application, generate ModelDeployment manifests, and send them through the Kubernetes workflow.",
    )
    parser.add_argument("app", help="Path to the application file.")
    parser.add_argument(
        "--activate",
        action="store_true",
        help="Request activation after preparing the deployment.",
    )
    parser.add_argument(
        "--when-full",
        help="Optional replacement policy to use with --activate.",
    )
    add_cluster_options(parser)
    parser.set_defaults(handler=run)


def run(args, client=None) -> int:
    """Run the deploy command."""

    def action() -> int:
        app = load_app(args.app)
        manifests = build_manifests(app)
        cluster = build_cluster_target(args)
        activate_requested = bool(getattr(args, "activate", False))
        response = resolve_client(args, client).deploy(
            DeployRequest(
                cluster=cluster,
                app_path=args.app,
                manifests=manifests,
                activate=activate_requested,
                when_full=getattr(args, "when_full", None),
            )
        )
        names = ", ".join(deployment["name"] for deployment in response["deployments"])
        emit_result(
            args.output,
            CommandResult(
                summary=f"Deployment placeholder prepared for {names} in namespace {cluster.namespace}.",
                payload=response,
            ),
        )
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)
