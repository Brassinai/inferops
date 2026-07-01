"""Doctor command for cluster diagnostics."""

from __future__ import annotations

from .contracts import CheckStatus
from .errors import ExitCode, run_with_cli_errors
from .kube import DoctorRequest, build_cluster_target, resolve_client
from .options import add_cluster_options
from .output import CommandResult, emit_result

CHECK_IDS = (
    "kubernetes-api",
    "device-plugin",
    "gpu-capacity",
    "cache",
    "runtime-class",
    "gateway",
    "tailscale",
)


def register(subcommands) -> None:
    """Register the doctor command."""
    parser = subcommands.add_parser(
        "doctor",
        help="Run cluster health diagnostics.",
        description="Check Kubernetes API, GPUs, cache, gateway, and optional Tailscale health.",
    )
    parser.add_argument(
        "--check",
        action="append",
        dest="checks",
        choices=CHECK_IDS,
        metavar="CHECK",
        help="Run only the named check. May be given multiple times.",
    )
    add_cluster_options(parser)
    parser.set_defaults(handler=run)


def run(args, client=None) -> int:
    """Run the doctor command."""

    def action() -> int:
        cluster = build_cluster_target(args)
        checks = getattr(args, "checks", None) or []
        response = resolve_client(args, client).doctor(
            DoctorRequest(cluster=cluster, checks=checks)
        )
        results = response["checks"]
        details = []
        has_fail = False
        for check in results:
            status = check["status"]
            if status == CheckStatus.FAIL:
                has_fail = True
            icon = {"PASS": "✓", "WARN": "!", "FAIL": "✗"}.get(status, "?")
            details.append(f"{icon} {check['id']}: {status} — {check['message']}")
            if check.get("remediation"):
                details.append(f"  → {check['remediation']}")

        summary = (
            f"Doctor completed: {sum(1 for c in results if c['status'] == 'PASS')} passed, "
            f"{sum(1 for c in results if c['status'] == 'WARN')} warned, "
            f"{sum(1 for c in results if c['status'] == 'FAIL')} failed."
        )
        emit_result(
            args.output,
            CommandResult(
                summary=summary,
                payload=response,
                details=tuple(details),
            ),
        )
        return ExitCode.ERROR if has_fail else ExitCode.SUCCESS

    return run_with_cli_errors(action)
