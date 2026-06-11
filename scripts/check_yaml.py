#!/usr/bin/env python3
"""Validate checked-in YAML syntax and the minimum InferOps CRD contracts."""

from __future__ import annotations

from pathlib import Path
import sys

import yaml


YAML_ROOTS = (Path(".github"), Path("deploy"), Path("examples"))
EXPECTED_CRDS = {
    "modeldeployments.inference.inferops.dev": "ModelDeployment",
    "modelruntimes.inference.inferops.dev": "ModelRuntime",
    "modelcaches.inference.inferops.dev": "ModelCache",
}


def yaml_files() -> list[Path]:
    return sorted(
        path
        for root in YAML_ROOTS
        for pattern in ("*.yaml", "*.yml")
        for path in root.rglob(pattern)
        if "templates" not in path.parts
    )


def load_documents(path: Path) -> list[dict]:
    try:
        documents = [doc for doc in yaml.safe_load_all(path.read_text(encoding="utf-8")) if doc]
    except yaml.YAMLError as exc:
        raise ValueError(f"{path}: invalid YAML: {exc}") from exc

    for index, document in enumerate(documents, start=1):
        if not isinstance(document, dict):
            raise ValueError(f"{path}: document {index} must be a mapping")
    return documents


def validate_crds(documents: list[tuple[Path, dict]]) -> None:
    found: dict[str, str] = {}
    for path, document in documents:
        if document.get("kind") != "CustomResourceDefinition":
            continue
        name = document.get("metadata", {}).get("name")
        kind = document.get("spec", {}).get("names", {}).get("kind")
        versions = document.get("spec", {}).get("versions", [])
        if not name or not kind:
            raise ValueError(f"{path}: CRD metadata.name and spec.names.kind are required")
        if not any(version.get("served") and version.get("schema", {}).get("openAPIV3Schema") for version in versions):
            raise ValueError(f"{path}: CRD requires a served version with an OpenAPI schema")
        found[name] = kind

    if found != EXPECTED_CRDS:
        raise ValueError(f"CRD set mismatch: got {found!r}, want {EXPECTED_CRDS!r}")


def main() -> None:
    paths = yaml_files()
    if not paths:
        raise SystemExit("error: no YAML files found")

    documents = [(path, document) for path in paths for document in load_documents(path)]
    validate_crds(documents)
    print(f"Parsed {len(paths)} YAML files and validated {len(EXPECTED_CRDS)} CRD contracts.")


if __name__ == "__main__":
    try:
        main()
    except ValueError as exc:
        print(f"error: {exc}", file=sys.stderr)
        raise SystemExit(1) from exc
