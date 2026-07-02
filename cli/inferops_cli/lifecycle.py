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
    if response.get("outcome") in FAILURE_OUTCOMES:
        return ExitCode.ERROR
    return ExitCode.SUCCESS


def progress_line(transition: dict[str, Any]) -> str:
    """Render one observed status transition."""
    phase = transition.get("phase", "Unknown")
    message = transition.get("message", "")
    return f"{phase}: {message}" if message else phase


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
            "The request is still reconciling. Inspect status and retry with a "
            "longer --timeout."
        )
    elif outcome == "superseded":
        lines.append(
            "Another update changed the desired activation state while this "
            "request was being observed."
        )
    return tuple(lines)
