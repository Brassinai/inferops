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
    parser.add_argument(
        "--cache-capacity",
        help=(
            "Declared node-local cache capacity such as 100Gi. Without a cache "
            "node selector, exactly one Ready schedulable cache node must be eligible."
        ),
    )
    parser.add_argument(
        "--cache-node",
        help="Annotate one explicit Ready schedulable node with --cache-capacity.",
    )
    parser.add_argument(
        "--cache-node-selector",
        help=(
            "Kubernetes node label selector for annotating all matching Ready "
            "schedulable nodes with --cache-capacity."
        ),
    )
    parser.add_argument(
        "--cache-node-capacity",
        action="append",
        default=[],
        metavar="NODE=CAPACITY",
        help=(
            "Annotate one node with a declared cache capacity. Repeat for "
            "different capacities, for example node-a=100Gi."
        ),
    )
    parser.add_argument(
        "--compute-profile",
        choices=("cpu", "nvidia-gpu"),
        default="cpu",
        help=(
            "Node resource assumptions for cache placement and diagnostics. "
            "Use nvidia-gpu only when cache nodes advertise nvidia.com/gpu."
        ),
    )
    parser.add_argument(
        "--tailscale-hostname",
        help="Expose the gateway through an installed Tailscale Kubernetes Operator.",
    )
    parser.add_argument(
        "--exposure",
        choices=("cluster-ip", "load-balancer", "ingress", "gateway-api"),
        help=(
            "Gateway exposure method. Defaults to cluster-ip unless "
            "--tailscale-hostname is set."
        ),
    )
    parser.add_argument(
        "--ingress-class",
        help="IngressClass name; requires --exposure ingress.",
    )
    parser.add_argument(
        "--ingress-hostname",
        help="Optional DNS hostname for --exposure ingress.",
    )
    parser.add_argument(
        "--gateway-name",
        help="Existing Gateway API Gateway name; requires --exposure gateway-api.",
    )
    parser.add_argument(
        "--gateway-namespace",
        help="Namespace of the referenced Gateway when it is not the install namespace.",
    )
    parser.add_argument(
        "--gateway-section-name",
        help="Optional Gateway listener name for the HTTPRoute parent reference.",
    )
    parser.add_argument(
        "--gateway-hostname",
        help="Optional HTTPRoute hostname for --exposure gateway-api.",
    )
    parser.add_argument(
        "--load-balancer-class",
        help="Optional Service loadBalancerClass; requires --exposure load-balancer.",
    )
    parser.add_argument(
        "--gateway-auth-secret",
        help=(
            "Existing Secret containing the gateway bearer token. Required for "
            "Ingress, Gateway API, and LoadBalancer exposure unless explicitly overridden."
        ),
    )
    parser.add_argument(
        "--allow-unauthenticated-exposure",
        action="store_true",
        help=(
            "Explicitly allow external exposure without built-in gateway authentication. "
            "Use only when equivalent authentication is enforced upstream."
        ),
    )
    parser.add_argument(
        "--charts-dir",
        help="Path to the InferOps Helm charts. Usually detected automatically.",
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
                compute_profile=getattr(args, "compute_profile", "cpu"),
                cache_path=getattr(args, "cache_path", None),
                cache_capacity=getattr(args, "cache_capacity", None),
                cache_node=getattr(args, "cache_node", None),
                cache_node_selector=getattr(args, "cache_node_selector", None),
                cache_node_capacities=tuple(
                    getattr(args, "cache_node_capacity", ()) or ()
                ),
                tailscale_hostname=getattr(args, "tailscale_hostname", None),
                exposure=getattr(args, "exposure", None),
                ingress_class=getattr(args, "ingress_class", None),
                ingress_hostname=getattr(args, "ingress_hostname", None),
                gateway_name=getattr(args, "gateway_name", None),
                gateway_namespace=getattr(args, "gateway_namespace", None),
                gateway_section_name=getattr(args, "gateway_section_name", None),
                gateway_hostname=getattr(args, "gateway_hostname", None),
                load_balancer_class=getattr(args, "load_balancer_class", None),
                gateway_auth_secret=getattr(args, "gateway_auth_secret", None),
                allow_unauthenticated_exposure=getattr(
                    args, "allow_unauthenticated_exposure", False
                ),
                charts_dir=getattr(args, "charts_dir", None),
            )
        )
        install = response["install"]
        emit_result(
            args.output,
            CommandResult(
                summary=f"InferOps installed with profile {install['profile']} in namespace {install['namespace']}.",
                payload=response,
                details=tuple(install["resources"]),
            ),
        )
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)
