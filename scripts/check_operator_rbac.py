#!/usr/bin/env python3
"""Validate security-sensitive InferOps operator RBAC invariants."""

from __future__ import annotations

import sys
from pathlib import Path

import yaml


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: check_operator_rbac.py <rendered-chart.yaml>", file=sys.stderr)
        return 2

    documents = list(yaml.safe_load_all(Path(sys.argv[1]).read_text()))
    lease_role_found = False

    for document in documents:
        if not isinstance(document, dict):
            continue
        kind = document.get("kind")
        if kind not in {"Role", "ClusterRole"}:
            continue
        for rule in document.get("rules", []):
            resources = set(rule.get("resources", []))
            verbs = set(rule.get("verbs", []))
            if "*" in resources or "pods" in resources:
                raise ValueError(f"{kind} grants forbidden resources: {sorted(resources)}")
            if "secrets" in resources and verbs != {"get"}:
                raise ValueError(
                    f"{kind} Secret access must be get-only, got {sorted(verbs)}"
                )
            if "leases" in resources:
                if kind != "Role":
                    raise ValueError("leader-election Lease access must be namespace-scoped")
                lease_role_found = True

    if not lease_role_found:
        raise ValueError("namespace-scoped leader-election Lease Role is missing")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
