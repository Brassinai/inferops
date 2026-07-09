#!/usr/bin/env python3
"""Validate security-sensitive team tenancy resources."""

from __future__ import annotations

import ipaddress
import sys
from pathlib import Path

import yaml

TEAM_ROLE = "inferops-team-operator"
WRITE_VERBS = {"create", "delete", "patch", "update"}


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: check_tenant_rbac.py <rendered-chart.yaml>", file=sys.stderr)
        return 2

    documents = [
        document
        for document in yaml.safe_load_all(Path(sys.argv[1]).read_text())
        if isinstance(document, dict)
    ]
    role = _find(documents, "Role", TEAM_ROLE)
    binding = _find(documents, "RoleBinding", TEAM_ROLE)
    _find_kind(documents, "ResourceQuota")
    _find_kind(documents, "LimitRange")

    network_policies = [
        document for document in documents if document.get("kind") == "NetworkPolicy"
    ]
    if len(network_policies) != 2:
        raise ValueError(
            f"gateway tenancy render has {len(network_policies)} NetworkPolicies, want 2"
        )

    for rule in role.get("rules", []):
        resources = set(rule.get("resources", []))
        verbs = set(rule.get("verbs", []))
        if "*" in resources or "*" in verbs:
            raise ValueError("team Role must not contain wildcard permissions")
        if resources & {"nodes", "secrets"}:
            raise ValueError(
                f"team Role grants forbidden resources: {sorted(resources)}"
            )
        if any(resource.endswith("/status") for resource in resources):
            if verbs != {"get"}:
                raise ValueError(
                    f"team Role status access must be get-only, got {sorted(verbs)}"
                )
        if "modelruntimes" in resources and verbs & WRITE_VERBS:
            raise ValueError("default team Role must not mutate ModelRuntime objects")
        if "modelcaches" in resources and "delete" in verbs:
            raise ValueError("default team Role must not delete ModelCache objects")

    for policy in network_policies:
        _check_network_policy(policy)

    subjects = binding.get("subjects", [])
    if not subjects or subjects[0].get("kind") != "Group":
        raise ValueError("team RoleBinding must contain the rendered Group subject")
    return 0


def _check_network_policy(policy: dict) -> None:
    spec = policy.get("spec", {})
    for direction in ("ingress", "egress"):
        for rule in spec.get(direction, []):
            for peer in rule.get("to", []) + rule.get("from", []):
                cidr = peer.get("ipBlock", {}).get("cidr")
                if cidr and ipaddress.ip_network(cidr).prefixlen == 0:
                    raise ValueError(
                        f"NetworkPolicy {policy['metadata']['name']!r} permits "
                        f"unrestricted {direction} through {cidr}"
                    )

    selector = spec.get("podSelector", {}).get("matchLabels", {})
    if selector.get("app.kubernetes.io/component") == "model-runtime":
        ingress = spec.get("ingress", [])
        if not ingress or not all(_has_http_port(rule) for rule in ingress):
            raise ValueError(
                "runtime ingress must be restricted to the named HTTP port"
            )

    for rule in spec.get("egress", []):
        reaches_runtime = any(
            peer.get("podSelector", {})
            .get("matchLabels", {})
            .get("app.kubernetes.io/component")
            == "model-runtime"
            for peer in rule.get("to", [])
        )
        if reaches_runtime and not _has_http_port(rule):
            raise ValueError(
                "gateway egress to runtimes must be restricted to the named HTTP port"
            )


def _has_http_port(rule: dict) -> bool:
    return any(
        port.get("port") == "http" and port.get("protocol") == "TCP"
        for port in rule.get("ports", [])
    )


def _find(documents: list[dict], kind: str, name: str) -> dict:
    for document in documents:
        if (
            document.get("kind") == kind
            and document.get("metadata", {}).get("name") == name
        ):
            return document
    raise ValueError(f"{kind} {name!r} is missing")


def _find_kind(documents: list[dict], kind: str) -> dict:
    for document in documents:
        if document.get("kind") == kind:
            return document
    raise ValueError(f"{kind} is missing")


if __name__ == "__main__":
    raise SystemExit(main())
