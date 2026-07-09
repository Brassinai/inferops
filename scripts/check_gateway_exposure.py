#!/usr/bin/env python3
"""Validate rendered gateway exposure contracts that kubeconform cannot cover."""

from __future__ import annotations

import argparse
from pathlib import Path
from typing import Any

import yaml


def load_documents(path: Path) -> list[dict[str, Any]]:
    """Load mapping documents from one Helm render."""
    documents = [
        document
        for document in yaml.safe_load_all(path.read_text(encoding="utf-8"))
        if document
    ]
    if not all(isinstance(document, dict) for document in documents):
        raise ValueError(f"{path}: every YAML document must be a mapping")
    return documents


def validate_documents(
    documents: list[dict[str, Any]],
    mode: str,
    *,
    expected_class: str | None = None,
    expected_parent_namespace: str | None = None,
    require_auth: bool = False,
) -> None:
    """Validate one rendered exposure mode."""
    service = _one(documents, "Service")
    service_name = service["metadata"]["name"]
    service_port = _http_service_port(service)
    if require_auth:
        _validate_gateway_auth(documents)

    if mode in {"ingress", "tailscale"}:
        ingress = _one(documents, "Ingress")
        expected = "tailscale" if mode == "tailscale" else expected_class
        _require(expected, "an expected IngressClass is required")
        _require(
            ingress.get("apiVersion") == "networking.k8s.io/v1",
            "Ingress must use networking.k8s.io/v1",
        )
        _require(
            ingress.get("spec", {}).get("ingressClassName") == expected,
            f"Ingress must select class {expected!r}",
        )
        paths = ingress.get("spec", {}).get("rules", [{}])[0].get("http", {}).get(
            "paths", []
        )
        _require(len(paths) == 1, "Ingress must have exactly one HTTP path")
        path = paths[0]
        _require(
            path.get("path") == "/" and path.get("pathType") == "Prefix",
            "Ingress must preserve all /models/... paths with Prefix matching",
        )
        backend = path.get("backend", {}).get("service", {})
        _require(
            backend.get("name") == service_name,
            "Ingress backend must reference the rendered gateway Service",
        )
        _require(
            backend.get("port", {}).get("name") == "http",
            "Ingress backend must reference the named HTTP port",
        )
        return

    if mode == "gateway-api":
        route = _one(documents, "HTTPRoute")
        _require(
            route.get("apiVersion") == "gateway.networking.k8s.io/v1",
            "HTTPRoute must use gateway.networking.k8s.io/v1",
        )
        parent_refs = route.get("spec", {}).get("parentRefs", [])
        _require(parent_refs, "HTTPRoute requires at least one parent reference")
        if expected_parent_namespace is not None:
            _require(
                parent_refs[0].get("namespace") == expected_parent_namespace,
                "HTTPRoute parent namespace does not match the requested Gateway",
            )
        rules = route.get("spec", {}).get("rules", [])
        _require(len(rules) == 1, "HTTPRoute must have exactly one rule")
        matches = rules[0].get("matches", [])
        _require(len(matches) == 1, "HTTPRoute must have exactly one path match")
        path = matches[0].get("path", {})
        _require(
            path.get("type") == "PathPrefix" and path.get("value") == "/",
            "HTTPRoute must preserve all /models/... paths",
        )
        backends = rules[0].get("backendRefs", [])
        _require(len(backends) == 1, "HTTPRoute must have exactly one backend")
        backend = backends[0]
        _require(
            backend.get("group") == ""
            and backend.get("kind") == "Service"
            and backend.get("name") == service_name
            and backend.get("port") == service_port,
            "HTTPRoute backend must reference the rendered gateway Service and port",
        )
        return

    if mode == "load-balancer":
        _require(
            service.get("spec", {}).get("type") == "LoadBalancer",
            "gateway Service must be type LoadBalancer",
        )
        _require(
            service.get("spec", {}).get("externalTrafficPolicy") in {"Cluster", "Local"},
            "LoadBalancer Service requires a supported externalTrafficPolicy",
        )
        return

    if mode == "multi-node":
        deployment = _one(documents, "Deployment")
        _require(
            int(deployment.get("spec", {}).get("replicas", 0)) >= 2,
            "multi-node profile requires at least two gateway replicas",
        )
        pod_spec = (
            deployment.get("spec", {}).get("template", {}).get("spec", {})
        )
        constraints = pod_spec.get("topologySpreadConstraints", [])
        _require(constraints, "multi-node profile requires topology spread")
        hostname_constraints = [
            constraint
            for constraint in constraints
            if constraint.get("topologyKey") == "kubernetes.io/hostname"
        ]
        _require(
            hostname_constraints,
            "multi-node profile must spread gateways across hostnames",
        )
        pod_labels = (
            deployment.get("spec", {})
            .get("template", {})
            .get("metadata", {})
            .get("labels", {})
        )
        selector = hostname_constraints[0].get("labelSelector", {}).get(
            "matchLabels", {}
        )
        _require(
            selector
            and all(pod_labels.get(key) == value for key, value in selector.items()),
            "topology spread selector must match this release's gateway Pods",
        )
        _one(documents, "PodDisruptionBudget")
        return

    raise ValueError(f"unsupported validation mode: {mode}")


def _one(documents: list[dict[str, Any]], kind: str) -> dict[str, Any]:
    matches = [document for document in documents if document.get("kind") == kind]
    _require(len(matches) == 1, f"expected exactly one {kind}, found {len(matches)}")
    return matches[0]


def _http_service_port(service: dict[str, Any]) -> int:
    ports = [
        port
        for port in service.get("spec", {}).get("ports", [])
        if port.get("name") == "http"
    ]
    _require(len(ports) == 1, "gateway Service requires exactly one named HTTP port")
    port = ports[0].get("port")
    _require(
        isinstance(port, int) and 1 <= port <= 65535,
        "gateway Service HTTP port is invalid",
    )
    return port


def _validate_gateway_auth(documents: list[dict[str, Any]]) -> None:
    deployment = _one(documents, "Deployment")
    containers = (
        deployment.get("spec", {})
        .get("template", {})
        .get("spec", {})
        .get("containers", [])
    )
    gateway = [
        container for container in containers if container.get("name") == "gateway"
    ]
    _require(len(gateway) == 1, "expected one gateway container")
    env_names = {item.get("name") for item in gateway[0].get("env", [])}
    _require(
        "INFEROPS_GATEWAY_AUTH_TOKEN_FILE" in env_names,
        "external exposure render must enable gateway authentication",
    )


def _require(condition: Any, message: str) -> None:
    if not condition:
        raise ValueError(message)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--mode",
        required=True,
        choices=("ingress", "tailscale", "gateway-api", "load-balancer", "multi-node"),
    )
    parser.add_argument("--expected-class")
    parser.add_argument("--expected-parent-namespace")
    parser.add_argument("--require-auth", action="store_true")
    parser.add_argument("render", type=Path)
    args = parser.parse_args()
    try:
        validate_documents(
            load_documents(args.render),
            args.mode,
            expected_class=args.expected_class,
            expected_parent_namespace=args.expected_parent_namespace,
            require_auth=args.require_auth,
        )
    except (OSError, ValueError, KeyError, TypeError) as exc:
        raise SystemExit(f"error: {args.render}: {exc}") from exc
    print(f"Validated {args.mode} gateway exposure: {args.render}")


if __name__ == "__main__":
    main()
