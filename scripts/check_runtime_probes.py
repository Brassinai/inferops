#!/usr/bin/env python3
"""Validate rendered runtime probe contracts for slow-starting model servers."""

from __future__ import annotations

import argparse
from pathlib import Path
from typing import Any

import yaml


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("rendered_chart", type=Path)
    parser.add_argument("--startup-failure-threshold", type=int, required=True)
    parser.add_argument("--probe-period-seconds", type=int, required=True)
    parser.add_argument("--probe-timeout-seconds", type=int, required=True)
    args = parser.parse_args()

    deployment = _one_deployment(args.rendered_chart)
    containers = (
        deployment.get("spec", {})
        .get("template", {})
        .get("spec", {})
        .get("containers", [])
    )
    _require(len(containers) == 1, "runtime chart must render exactly one container")
    container = containers[0]

    _validate_probe(
        container.get("startupProbe"),
        "startupProbe",
        failure_threshold=args.startup_failure_threshold,
        period_seconds=args.probe_period_seconds,
        timeout_seconds=args.probe_timeout_seconds,
    )
    _validate_probe(
        container.get("readinessProbe"),
        "readinessProbe",
        failure_threshold=None,
        period_seconds=None,
        timeout_seconds=None,
    )
    _validate_probe(
        container.get("livenessProbe"),
        "livenessProbe",
        failure_threshold=None,
        period_seconds=None,
        timeout_seconds=None,
    )
    return 0


def _one_deployment(path: Path) -> dict[str, Any]:
    documents = [
        document
        for document in yaml.safe_load_all(path.read_text(encoding="utf-8"))
        if isinstance(document, dict)
    ]
    deployments = [
        document for document in documents if document.get("kind") == "Deployment"
    ]
    _require(len(deployments) == 1, f"{path}: expected exactly one Deployment")
    return deployments[0]


def _validate_probe(
    probe: Any,
    name: str,
    *,
    failure_threshold: int | None,
    period_seconds: int | None,
    timeout_seconds: int | None,
) -> None:
    _require(isinstance(probe, dict), f"{name} is missing")
    http_get = probe.get("httpGet", {})
    _require(http_get.get("port") == "http", f"{name} must target the named HTTP port")
    _require(http_get.get("path"), f"{name} must set an HTTP path")
    if failure_threshold is not None:
        _require(
            probe.get("failureThreshold") == failure_threshold,
            (
                f"{name}.failureThreshold = {probe.get('failureThreshold')}, "
                f"want {failure_threshold}"
            ),
        )
    if period_seconds is not None:
        _require(
            probe.get("periodSeconds") == period_seconds,
            f"{name}.periodSeconds = {probe.get('periodSeconds')}, want {period_seconds}",
        )
    if timeout_seconds is not None:
        _require(
            probe.get("timeoutSeconds") == timeout_seconds,
            f"{name}.timeoutSeconds = {probe.get('timeoutSeconds')}, want {timeout_seconds}",
        )


def _require(condition: bool, message: str) -> None:
    if not condition:
        raise ValueError(message)


if __name__ == "__main__":
    raise SystemExit(main())
