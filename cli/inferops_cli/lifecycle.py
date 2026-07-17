"""Shared lifecycle command contracts and rendering helpers."""

from __future__ import annotations

import argparse
import re
from typing import Any

from .errors import ExitCode

ACTIVATION_POLICIES = (
    "Queue",
    "Reject",
    "ReplaceOldest",
    "ReplaceLowestPriority",
)
FAILURE_OUTCOMES = frozenset({"failed", "rejected", "superseded", "timeout"})
ACTIVATION_STEPS = (
    ("spec", "spec validated"),
    ("runtime", "runtime resolved"),
    ("cache", "cache placed/downloaded"),
    ("gpu", "GPU assigned"),
    ("pod", "runtime Pod ready"),
    ("model", "model loaded"),
    ("route", "gateway route ready"),
)
_DURATION_PATTERN = re.compile(r"^(?P<value>[0-9]+(?:\.[0-9]+)?)(?P<unit>[smh]?)$")
_DURATION_MULTIPLIERS = {"": 1, "s": 1, "m": 60, "h": 3600}


def parse_timeout(value: str) -> float:
    """Parse a positive timeout expressed as seconds or an s/m/h duration."""
    match = _DURATION_PATTERN.fullmatch(value.strip().lower())
    if match is None:
        raise argparse.ArgumentTypeError(
            "timeout must be a positive duration such as 30s, 5m, or 1h"
        )
    seconds = float(match.group("value")) * _DURATION_MULTIPLIERS[match.group("unit")]
    if seconds <= 0:
        raise argparse.ArgumentTypeError("timeout must be greater than zero")
    return seconds


def parse_positive_integer(value: str) -> int:
    """Parse a strictly positive integer command option."""
    try:
        parsed = int(value)
    except ValueError as exc:
        raise argparse.ArgumentTypeError("value must be a positive integer") from exc
    if parsed <= 0:
        raise argparse.ArgumentTypeError("value must be greater than zero")
    return parsed


def lifecycle_exit_code(response: dict[str, Any]) -> ExitCode:
    """Return a stable command exit code for a lifecycle response."""
    outcome = response.get("outcome")
    if outcome == "rejected":
        return ExitCode.LIFECYCLE_REJECTED
    if outcome == "failed":
        return ExitCode.LIFECYCLE_FAILED
    if outcome == "timeout":
        return ExitCode.LIFECYCLE_TIMEOUT
    if outcome == "superseded":
        return ExitCode.LIFECYCLE_SUPERSEDED
    if outcome in FAILURE_OUTCOMES:
        return ExitCode.ERROR
    return ExitCode.SUCCESS


def progress_line(transition: dict[str, Any]) -> str:
    """Render one observed status transition."""
    phase = transition.get("phase", "Unknown")
    message = transition.get("message", "")
    return f"{phase}: {message}" if message else phase


def activation_transition_line(transition: dict[str, Any]) -> str:
    """Render one activation transition as a checklist progress line."""
    step = _transition_step(transition)
    label = _step_label(step)
    message = transition.get("message") or transition.get("reason") or transition.get(
        "phase", "Unknown"
    )
    return f"[>] {label}: {message}"


def activation_details(
    response: dict[str, Any], *, verbose: bool = False
) -> tuple[str, ...]:
    """Render activation checklist and diagnosis details."""
    lines = list(_activation_checklist(response))
    lines.extend(progress_guidance(response))
    diagnosis = response.get("diagnosis")
    if isinstance(diagnosis, dict):
        if lines:
            lines.append("")
        lines.extend(_diagnosis_lines(diagnosis, verbose=verbose))
    return tuple(lines)


def progress_guidance(response: dict[str, Any]) -> tuple[str, ...]:
    """Render actionable guidance for a lifecycle outcome."""
    lines: list[str] = []
    outcome = response.get("outcome")
    if outcome == "waiting":
        lines.append(
            "Capacity is unavailable; the activation remains queued. "
            "Run 'inferops status' to follow it."
        )
    elif outcome == "rejected":
        lines.append(
            "Capacity rejected the request. Choose Queue or explicitly allow a "
            "replacement policy with --when-full."
        )
    elif outcome == "timeout":
        lines.append(
            "The request is still reconciling. Use the blocking step below or retry "
            "with a longer --timeout if the evidence shows normal progress."
        )
    elif outcome == "superseded":
        lines.append(
            "Another update changed the desired activation state while this "
            "request was being observed."
        )
    return tuple(lines)


def activation_diagnosis(
    deployment: dict[str, Any],
    *,
    outcome: str,
    event: dict[str, Any] | None = None,
    log_tail: dict[str, Any] | None = None,
    checked_resources: list[dict[str, Any]] | None = None,
    verbose: bool = False,
) -> dict[str, Any]:
    """Build a structured, InferOps-first activation diagnosis."""
    conditions = _current_conditions(deployment)
    condition = _actionable_condition(conditions)
    reason = str(condition.get("reason") or _reason_from_event(event) or outcome)
    message = str(condition.get("message") or _message_from_event(event) or "")
    blocking_step = _blocking_step(deployment, reason, message, outcome)
    blocking_resource = _blocking_resource(deployment, blocking_step, log_tail)
    diagnosis: dict[str, Any] = {
        "blockingStep": _step_label(blocking_step),
        "blockingResource": blocking_resource,
        "reason": reason,
        "message": message,
        "suggestedFixes": _suggested_fixes(reason, message, deployment, outcome),
    }
    if conditions:
        diagnosis["conditions"] = conditions if verbose else conditions[-3:]
    if event:
        diagnosis["lastEvent"] = event
    if log_tail and log_tail.get("lines"):
        diagnosis["logTail"] = log_tail
    if checked_resources:
        diagnosis["checkedResources"] = (
            checked_resources if verbose else checked_resources[:5]
        )
    return diagnosis


def deployment_diagnosis_report(
    deployment: dict[str, Any],
    *,
    event: dict[str, Any] | None = None,
    log_tail: dict[str, Any] | None = None,
    checked_resources: list[dict[str, Any]] | None = None,
    verbose: bool = False,
) -> dict[str, Any]:
    """Build a read-only incident report for one ModelDeployment."""
    deployment_event = event or _inactive_event(deployment)
    outcome = _deployment_diagnosis_outcome(deployment)
    diagnosis = activation_diagnosis(
        deployment,
        outcome=outcome,
        event=deployment_event,
        log_tail=log_tail,
        checked_resources=checked_resources,
        verbose=verbose,
    )
    evidence: dict[str, Any] = {}
    conditions = diagnosis.get("conditions")
    if conditions:
        evidence["conditions"] = conditions
    if diagnosis.get("lastEvent"):
        evidence["lastEvent"] = diagnosis["lastEvent"]
    if diagnosis.get("logTail") and outcome != "active":
        evidence["logTail"] = diagnosis["logTail"]
    if outcome == "active":
        return {
            "phase": deployment.get("phase", "Unknown"),
            "blockingStep": "none",
            "evidence": evidence,
            "problem": {
                "reason": "Ready",
                "message": "deployment is active; no blocking resource found",
                "resource": {},
            },
            "suggestedFixes": [],
            "checkedResources": diagnosis.get("checkedResources", []),
        }
    problem = {
        "reason": diagnosis.get("reason", outcome),
        "message": diagnosis.get("message", ""),
        "resource": diagnosis.get("blockingResource", {}),
    }
    return {
        "phase": deployment.get("phase", "Unknown"),
        "blockingStep": diagnosis.get("blockingStep", "unknown"),
        "evidence": evidence,
        "problem": problem,
        "suggestedFixes": diagnosis.get("suggestedFixes", []),
        "checkedResources": diagnosis.get("checkedResources", []),
    }


def _activation_checklist(response: dict[str, Any]) -> tuple[str, ...]:
    deployment = response.get("deployment", {})
    outcome = response.get("outcome", "requested")
    active_step = _blocking_step(
        deployment,
        _diagnosis_reason(response),
        _diagnosis_message(response),
        outcome,
    )
    completed = _completed_steps(deployment, outcome)
    lines = ["Activation progress:"]
    for step, label in ACTIVATION_STEPS:
        if step in completed:
            marker = "x"
        elif step == active_step and outcome not in {"active", "requested"}:
            marker = ">"
        else:
            marker = " "
        lines.append(f"[{marker}] {label}")
    return tuple(lines)


def _diagnosis_lines(diagnosis: dict[str, Any], *, verbose: bool) -> tuple[str, ...]:
    lines = [
        "Activation diagnosis:",
        f"Blocking step: {diagnosis.get('blockingStep', 'unknown')}",
    ]
    resource = diagnosis.get("blockingResource")
    if isinstance(resource, dict) and resource.get("kind"):
        namespace = resource.get("namespace")
        name = resource.get("name", "")
        qualified = f"{namespace}/{name}" if namespace else name
        lines.append(f"Blocking resource: {resource['kind']} {qualified}".rstrip())
    if diagnosis.get("reason"):
        lines.append(f"Reason: {diagnosis['reason']}")
    if diagnosis.get("message"):
        lines.append(f"Evidence: {diagnosis['message']}")
    event = diagnosis.get("lastEvent")
    if isinstance(event, dict):
        event_bits = [
            str(event.get("reason", "")).strip(),
            str(event.get("message", "")).strip(),
        ]
        lines.append("Last event: " + " - ".join(bit for bit in event_bits if bit))
    log_tail = diagnosis.get("logTail")
    if isinstance(log_tail, dict) and log_tail.get("lines"):
        pod = log_tail.get("pod", "pod")
        lines.append(f"Log tail ({pod}):")
        for line in log_tail["lines"][-4:]:
            lines.append(f"  {line}")
    fixes = diagnosis.get("suggestedFixes") or ()
    if fixes:
        lines.append("Suggested fixes:")
        for fix in fixes:
            lines.append(f"- {fix}")
    if verbose:
        conditions = diagnosis.get("conditions") or ()
        if conditions:
            lines.append("Conditions:")
            for condition in conditions:
                reason = condition.get("reason", "")
                status = condition.get("status", "")
                message = condition.get("message", "")
                condition_type = condition.get("type", "Condition")
                lines.append(
                    f"- {condition_type}={status} {reason}: {message}".rstrip()
                )
        checked = diagnosis.get("checkedResources") or ()
        if checked:
            lines.append("Checked resources:")
            for item in checked:
                parts = [
                    str(item.get(key, "")).strip()
                    for key in ("kind", "namespace", "name", "status")
                ]
                lines.append("- " + " ".join(part for part in parts if part))
    return tuple(lines)


def _completed_steps(deployment: dict[str, Any], outcome: str) -> set[str]:
    phase = str(deployment.get("phase", ""))
    completed = {"spec"}
    if deployment.get("runtime") or phase in {"Activating", "Active", "WaitingForGPU"}:
        completed.add("runtime")
    cache = deployment.get("cache")
    if isinstance(cache, dict) and (
        cache.get("state") or cache.get("nodeName") or cache.get("path")
    ):
        completed.add("cache")
    if (
        deployment.get("assignedNode")
        or deployment.get("assignedGPUs")
        or phase == "Active"
    ):
        completed.add("gpu")
    replicas = deployment.get("replicas")
    if isinstance(replicas, dict) and int(replicas.get("ready") or 0) > 0:
        completed.add("pod")
    if deployment.get("modelLoaded") or outcome == "active":
        completed.add("model")
    if (
        deployment.get("endpoint") or deployment.get("serviceName")
    ) and outcome == "active":
        completed.add("route")
    if outcome == "active":
        completed.update(step for step, _ in ACTIVATION_STEPS)
    return completed


def _transition_step(transition: dict[str, Any]) -> str:
    reason = str(transition.get("reason", "")).lower()
    message = str(transition.get("message", "")).lower()
    phase = str(transition.get("phase", "")).lower()
    text = " ".join((reason, message, phase))
    if "cache" in text or "download" in text or "hugging" in text or "hf_" in text:
        return "cache"
    if "gpu" in text or "capacity" in text or "device" in text:
        return "gpu"
    if (
        "pod" in text
        or "imagepull" in text
        or "crashloop" in text
        or "oom" in text
        or "runtimefailed" in text
        or "runtimeready" in text
        or "failed to become ready" in text
    ):
        return "pod"
    if "model" in text or "load" in text:
        return "model"
    if "gateway" in text or "route" in text or "service" in text:
        return "route"
    if "runtime" in text:
        return "runtime"
    if "invalid" in text or "reject" in text:
        return "spec"
    return "runtime"


def _blocking_step(
    deployment: dict[str, Any], reason: str, message: str, outcome: str
) -> str:
    if outcome == "active":
        return "route"
    reason_text = f"{reason} {message}".lower()
    if deployment.get("observedGeneration") != deployment.get("generation"):
        return "spec"
    detected = _transition_step(
        {
            "reason": reason,
            "message": message,
            "phase": deployment.get("phase", ""),
        }
    )
    if detected != "runtime":
        return detected
    completed = _completed_steps(deployment, outcome)
    for step, _ in ACTIVATION_STEPS:
        if step not in completed:
            return step
    if "pending" in reason_text:
        return "pod"
    return detected


def _blocking_resource(
    deployment: dict[str, Any], step: str, log_tail: dict[str, Any] | None
) -> dict[str, Any]:
    namespace = deployment.get("namespace", "default")
    name = deployment.get("name", "")
    if step == "cache":
        cache = deployment.get("cache") if isinstance(deployment.get("cache"), dict) else {}
        return {
            "kind": "ModelCache",
            "namespace": namespace,
            "name": cache.get("name") or name,
        }
    if step in {"pod", "model"} and log_tail and log_tail.get("pod"):
        return {"kind": "Pod", "namespace": namespace, "name": log_tail["pod"]}
    if step == "gpu":
        assigned_node = deployment.get("assignedNode")
        if assigned_node:
            return {"kind": "Node", "namespace": "", "name": assigned_node}
        return {"kind": "GPUCapacity", "namespace": namespace, "name": name}
    if step == "route":
        return {
            "kind": "Service",
            "namespace": namespace,
            "name": deployment.get("serviceName", ""),
        }
    return {"kind": "ModelDeployment", "namespace": namespace, "name": name}


def _suggested_fixes(
    reason: str, message: str, deployment: dict[str, Any], outcome: str
) -> list[str]:
    text = f"{reason} {message}".lower()
    fixes: list[str] = []
    if "cache" in text and (
        "small" in text or "capacity" in text or "insufficient" in text
    ):
        fixes.append(
            "Increase node cache capacity with 'inferops install --cache-capacity ...' "
            "or choose a node with enough free cache."
        )
    if "cache" in text and (
        "path" in text or "hostpath" in text or "directory" in text
    ):
        fixes.append(
            "Verify --cache-path exists on the selected cache node and is readable "
            "by InferOps."
        )
    if (
        "download" in text
        or "hugging" in text
        or "hf_" in text
        or "401" in text
        or "403" in text
    ):
        fixes.append(
            "Check model repository access and Hugging Face credentials before "
            "retrying activation."
        )
    if "runtimeclass" in text or "compute profile" in text:
        fixes.append(
            "Run 'inferops install --compute-profile ...' with the profile that "
            "matches this runtime."
        )
    if "device" in text or "nvidia" in text:
        fixes.append(
            "Install or repair the NVIDIA device plugin, then confirm GPUs appear "
            "with 'inferops gpu list'."
        )
    if "gpu" in text or "capacity" in text:
        fixes.append(
            "Free a compatible GPU, use --when-full Queue, or explicitly choose "
            "a replacement policy."
        )
    if "imagepull" in text or "errimagepull" in text:
        fixes.append(
            "Verify the runtime image name, tag, registry credentials, and cluster "
            "network access."
        )
    if (
        "runtimefailed" in text
        or "failed to become ready" in text
        or "crashloop" in text
        or "crash" in text
    ):
        fixes.append(
            "Inspect the runtime Pod logs; fix runtime configuration or image "
            "startup errors."
        )
    if "oom" in text or "outofmemory" in text:
        fixes.append(
            "Increase the runtime memory request/limit or choose a smaller "
            "model/quantization."
        )
    if "readiness" in text or "not ready" in text or outcome == "timeout":
        fixes.append(
            "Keep waiting with a longer --timeout only if events/logs show "
            "forward progress."
        )
    if deployment.get("observedGeneration") != deployment.get("generation"):
        fixes.append(
            "Wait for the operator to observe the latest generation; restart the "
            "operator only if status remains stale."
        )
    if "inactive" in text or deployment.get("desiredState") == "Inactive":
        name = deployment.get("name", "<deployment>")
        fixes.append(
            f"Run 'inferops activate {name}' when you are ready to start the runtime."
        )
    if not fixes:
        name = deployment.get("name", "<deployment>")
        fixes.append(
            f"Run 'inferops status {name}' and retry activation after the "
            "blocking resource changes."
        )
    return fixes


def _current_conditions(deployment: dict[str, Any]) -> list[dict[str, Any]]:
    generation = deployment.get("generation", 0)
    current: list[dict[str, Any]] = []
    for condition in deployment.get("conditions", []):
        observed = condition.get("observedGeneration")
        if observed is None:
            current.append(condition)
            continue
        try:
            if int(observed) >= int(generation or 0):
                current.append(condition)
        except (TypeError, ValueError):
            continue
    return current


def _actionable_condition(conditions: list[dict[str, Any]]) -> dict[str, Any]:
    for condition in reversed(conditions):
        if condition.get("status") == "False" and (
            condition.get("reason") or condition.get("message")
        ):
            return condition
    for condition in reversed(conditions):
        if condition.get("type") == "Ready":
            return condition
    return {}


def _diagnosis_reason(response: dict[str, Any]) -> str:
    diagnosis = response.get("diagnosis")
    if isinstance(diagnosis, dict):
        return str(diagnosis.get("reason", ""))
    transitions = response.get("transitions")
    if isinstance(transitions, list) and transitions:
        return str(transitions[-1].get("reason", ""))
    return str(response.get("outcome", ""))


def _diagnosis_message(response: dict[str, Any]) -> str:
    diagnosis = response.get("diagnosis")
    if isinstance(diagnosis, dict):
        return str(diagnosis.get("message", ""))
    transitions = response.get("transitions")
    if isinstance(transitions, list) and transitions:
        return str(transitions[-1].get("message", ""))
    return ""


def _reason_from_event(event: dict[str, Any] | None) -> str:
    if not event:
        return ""
    return str(event.get("reason", ""))


def _message_from_event(event: dict[str, Any] | None) -> str:
    if not event:
        return ""
    return str(event.get("message", ""))


def _deployment_diagnosis_outcome(deployment: dict[str, Any]) -> str:
    phase = str(deployment.get("phase", "Unknown"))
    if phase == "Active":
        return "active"
    if phase == "Failed":
        return "failed"
    if phase in {"WaitingForCapacity", "WaitingForGPU"}:
        return "waiting"
    if deployment.get("desiredState") == "Inactive":
        return "inactive"
    return "timeout"


def _inactive_event(deployment: dict[str, Any]) -> dict[str, Any] | None:
    if deployment.get("desiredState") != "Inactive":
        return None
    if deployment.get("phase") == "Active":
        return None
    return {
        "reason": "Inactive",
        "message": "deployment is inactive; no runtime should be running yet",
        "involvedObject": {
            "kind": "ModelDeployment",
            "namespace": deployment.get("namespace", "default"),
            "name": deployment.get("name", ""),
        },
    }


def _step_label(step: str) -> str:
    labels = dict(ACTIVATION_STEPS)
    return labels.get(step, step)
