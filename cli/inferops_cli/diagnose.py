"""Deployment-specific diagnosis command."""

from __future__ import annotations

from typing import Any

from .errors import ExitCode, run_with_cli_errors
from .kube import DiagnoseRequest, build_cluster_target, resolve_client
from .options import add_cluster_options
from .output import CommandResult, emit_result


def register(subcommands) -> None:
    """Register the diagnose command."""
    parser = subcommands.add_parser(
        "diagnose",
        help="Explain why one deployment is not ready.",
        description=(
            "Explain why one deployment is not ready by inspecting its "
            "ModelDeployment, cache, runtime, routing, event, log, GPU, and "
            "cache-capacity evidence."
        ),
    )
    parser.add_argument("name", help="Deployment name.")
    parser.add_argument(
        "--verbose",
        "--debug",
        dest="verbose",
        action="store_true",
        help="Include deeper checked-resource evidence in text output.",
    )
    add_cluster_options(parser)
    parser.set_defaults(handler=run)


def run(args, client=None) -> int:
    """Run the diagnose command."""

    def action() -> int:
        cluster = build_cluster_target(args)
        verbose = getattr(args, "verbose", False)
        response = resolve_client(args, client).diagnose(
            DiagnoseRequest(cluster=cluster, name=args.name, verbose=verbose)
        )
        deployment = response["deployment"]
        emit_result(
            args.output,
            CommandResult(
                summary=(
                    f"Diagnosis for {deployment['name']}: "
                    f"{response['phase']} in namespace {deployment['namespace']}."
                ),
                payload=response,
                details=_diagnose_details(response, verbose=verbose),
            ),
        )
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)


def _diagnose_details(
    response: dict[str, Any], *, verbose: bool = False
) -> tuple[str, ...]:
    lines = [
        "Incident report:",
        f"Status: {response.get('phase', 'Unknown')}",
        f"Blocking step: {response.get('blockingStep', 'unknown')}",
    ]
    problem = response.get("problem")
    if isinstance(problem, dict):
        resource = problem.get("resource")
        if isinstance(resource, dict) and resource.get("kind"):
            lines.append(f"Blocking resource: {_format_resource(resource)}")
        if problem.get("reason"):
            lines.append(f"Problem: {problem['reason']}")
        if problem.get("message"):
            lines.append(f"Evidence: {problem['message']}")

    evidence = response.get("evidence")
    if isinstance(evidence, dict):
        _append_evidence_lines(lines, evidence)

    fixes = response.get("suggestedFixes") or ()
    if fixes:
        lines.append("Suggested fixes:")
        for fix in fixes:
            lines.append(f"- {fix}")

    checked = response.get("checkedResources") or ()
    if checked and verbose:
        lines.append("Checked resources:")
        for item in checked:
            lines.append(f"- {_format_checked_resource(item)}")
    return tuple(lines)


def _append_evidence_lines(lines: list[str], evidence: dict[str, Any]) -> None:
    event = evidence.get("lastEvent")
    if isinstance(event, dict):
        event_bits = [
            str(event.get("reason", "")).strip(),
            str(event.get("message", "")).strip(),
        ]
        event_text = " - ".join(bit for bit in event_bits if bit)
        if event_text:
            lines.append(f"Last event: {event_text}")

    log_tail = evidence.get("logTail")
    if isinstance(log_tail, dict) and log_tail.get("lines"):
        pod = log_tail.get("pod", "pod")
        lines.append(f"Log tail ({pod}):")
        for line in log_tail["lines"][-4:]:
            lines.append(f"  {line}")


def _format_resource(resource: dict[str, Any]) -> str:
    namespace = str(resource.get("namespace") or "").strip()
    name = str(resource.get("name") or "").strip()
    qualified = f"{namespace}/{name}" if namespace else name
    return f"{resource.get('kind', 'Resource')} {qualified}".rstrip()


def _format_checked_resource(item: dict[str, Any]) -> str:
    parts = [
        str(item.get(key, "")).strip()
        for key in ("kind", "namespace", "name", "status")
    ]
    return " ".join(part for part in parts if part)
