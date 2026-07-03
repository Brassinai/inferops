#!/usr/bin/env python3
"""Validate checked-in YAML syntax and the minimum InferOps CRD contracts."""

from __future__ import annotations

from pathlib import Path
import sys

import yaml


YAML_ROOTS = (Path(".github"), Path("deploy"), Path("examples"))
CRD_SOURCE_ROOT = Path("deploy/manifests/crds")
CRD_UPGRADE_BASELINE_ROOT = Path("tests/fixtures/crds/pre-mvp508")
EXPECTED_CRDS = {
    "modeldeployments.inference.inferops.dev": "ModelDeployment",
    "modelruntimes.inference.inferops.dev": "ModelRuntime",
    "modelcaches.inference.inferops.dev": "ModelCache",
}
COMPATIBILITY_CONTRACT = {
    "modeldeployments.inference.inferops.dev": {
        "required": {
            "": {"spec"},
            "spec": {"model", "runtime"},
            "spec.model": {"repo"},
            "spec.runtime": {"ref"},
            "spec.resources.gpu": {"count"},
        },
        "types": {
            "spec.model.repo": "string",
            "spec.runtime.ref": "string",
            "spec.runtime.tensorParallelSize": "integer",
            "spec.resources.cpu": "string",
            "spec.resources.memory": "string",
            "spec.resources.gpu.count": "integer",
            "spec.activation.desiredState": "string",
            "spec.scaling.minReplicas": "integer",
            "spec.scaling.maxReplicas": "integer",
            "spec.routing.enabled": "boolean",
            "spec.cache.path": "string",
            "spec.secrets.huggingFaceTokenSecretName": "string",
            "spec.scheduling.nodeSelector": "object",
            "spec.scheduling.tolerations": "array",
            "spec.scheduling.topologySpreadConstraints": "array",
            "spec.availability.podDisruptionBudget": "object",
            "status.phase": "string",
            "status.conditions": "array",
        },
        "enums": {
            "spec.activation.desiredState": {"Inactive", "Active"},
            "spec.activation.whenFull": {
                "Queue",
                "Reject",
                "ReplaceOldest",
                "ReplaceLowestPriority",
            },
            "status.phase": {
                "Pending",
                "Downloading",
                "Cached",
                "WaitingForCapacity",
                "WaitingForGPU",
                "Activating",
                "Active",
                "Draining",
                "Deactivating",
                "Failed",
            },
        },
    },
    "modelruntimes.inference.inferops.dev": {
        "required": {
            "": {"spec"},
            "spec": {"engine", "protocol", "defaultImage", "port", "healthPath"},
        },
        "types": {
            "spec.engine": "string",
            "spec.protocol": "string",
            "spec.defaultImage": "string",
            "spec.port": "integer",
            "spec.healthPath": "string",
            "spec.command": "array",
            "spec.args": "array",
            "spec.env": "object",
            "status.phase": "string",
            "status.conditions": "array",
        },
        "enums": {
            "status.phase": {"Pending", "Ready", "Unavailable", "Failed"},
        },
    },
    "modelcaches.inference.inferops.dev": {
        "required": {
            "": {"spec"},
            "spec": {"modelRepo", "storage"},
            "spec.storage": {"type", "size", "path"},
        },
        "types": {
            "spec.modelRepo": "string",
            "spec.revision": "string",
            "spec.storage.type": "string",
            "spec.storage.size": "string",
            "spec.storage.nodeName": "string",
            "spec.storage.nodeSelector": "object",
            "spec.storage.tolerations": "array",
            "spec.storage.path": "string",
            "spec.secretRef": "string",
            "status.phase": "string",
            "status.conditions": "array",
        },
        "enums": {
            "spec.storage.type": {"nodeLocal"},
            "status.phase": {"Pending", "Downloading", "Ready", "Failed"},
        },
    },
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
        validate_crd_compatibility(path, name, document)

    if found != EXPECTED_CRDS:
        raise ValueError(f"CRD set mismatch: got {found!r}, want {EXPECTED_CRDS!r}")
    validate_crd_upgrade_compatibility(documents)


def validate_crd_upgrade_compatibility(
    documents: list[tuple[Path, dict]],
) -> None:
    current = {
        document["metadata"]["name"]: document
        for path, document in documents
        if path.parent == CRD_SOURCE_ROOT
        and document.get("kind") == "CustomResourceDefinition"
    }
    baseline: dict[str, dict] = {}
    for path in sorted(CRD_UPGRADE_BASELINE_ROOT.glob("*.yaml")):
        for document in load_documents(path):
            if document.get("kind") == "CustomResourceDefinition":
                baseline[document["metadata"]["name"]] = document
    if set(baseline) != set(EXPECTED_CRDS):
        raise ValueError(
            "CRD upgrade baseline set mismatch: "
            f"got {sorted(baseline)}, want {sorted(EXPECTED_CRDS)}"
        )

    for name, old_crd in baseline.items():
        new_crd = current[name]
        for field in ("group", "scope"):
            if old_crd["spec"][field] != new_crd["spec"][field]:
                raise ValueError(f"{name}: spec.{field} changed across upgrade")
        for field in ("kind", "plural", "singular"):
            if old_crd["spec"]["names"][field] != new_crd["spec"]["names"][field]:
                raise ValueError(f"{name}: spec.names.{field} changed across upgrade")
        old_schema = crd_version_schema(old_crd, "v1alpha1")
        new_schema = crd_version_schema(new_crd, "v1alpha1")
        compare_openapi_schema(old_schema, new_schema, f"{name}.v1alpha1")


def crd_version_schema(crd: dict, version_name: str) -> dict:
    for version in crd["spec"]["versions"]:
        if version.get("name") == version_name:
            if not version.get("served"):
                raise ValueError(
                    f"{crd['metadata']['name']}: {version_name} is no longer served"
                )
            return version["schema"]["openAPIV3Schema"]
    raise ValueError(f"{crd['metadata']['name']}: {version_name} was removed")


def compare_openapi_schema(old: dict, new: dict, path: str) -> None:
    missing = object()
    for field in (
        "type",
        "format",
        "default",
        "pattern",
        "minimum",
        "maximum",
        "minLength",
        "maxLength",
        "x-kubernetes-validations",
    ):
        old_value = old.get(field, missing)
        new_value = new.get(field, missing)
        if old_value != new_value:
            raise ValueError(
                f"{path}: OpenAPI {field} changed from "
                f"{None if old_value is missing else old_value!r} to "
                f"{None if new_value is missing else new_value!r}"
            )

    old_enum = set(old.get("enum", []))
    new_enum = set(new.get("enum", []))
    if not old_enum and new_enum:
        raise ValueError(f"{path}: added enum restriction {sorted(new_enum)}")
    if old_enum:
        removed = old_enum - new_enum
        if removed:
            raise ValueError(f"{path}: removed enum values {sorted(removed)}")

    newly_required = set(new.get("required", [])) - set(old.get("required", []))
    if newly_required:
        raise ValueError(
            f"{path}: added required fields {sorted(newly_required)}"
        )

    old_properties = old.get("properties", {})
    new_properties = new.get("properties", {})
    for name, old_property in old_properties.items():
        if name not in new_properties:
            raise ValueError(f"{path}.{name}: property was removed")
        compare_openapi_schema(
            old_property,
            new_properties[name],
            f"{path}.{name}",
        )

    if isinstance(old.get("items"), dict):
        if not isinstance(new.get("items"), dict):
            raise ValueError(f"{path}: array item schema was removed")
        compare_openapi_schema(old["items"], new["items"], f"{path}[]")

    if isinstance(old.get("additionalProperties"), dict):
        if not isinstance(new.get("additionalProperties"), dict):
            raise ValueError(f"{path}: additionalProperties schema was removed")
        compare_openapi_schema(
            old["additionalProperties"],
            new["additionalProperties"],
            f"{path}.*",
        )


def validate_crd_compatibility(path: Path, name: str, document: dict) -> None:
    contract = COMPATIBILITY_CONTRACT.get(name)
    if contract is None:
        return
    versions = document["spec"]["versions"]
    version = next((item for item in versions if item.get("name") == "v1alpha1"), None)
    if version is None or not version.get("served") or not version.get("storage"):
        raise ValueError(f"{path}: v1alpha1 must remain served and storage")
    schema = version["schema"]["openAPIV3Schema"]

    for object_path, allowed_required in contract["required"].items():
        node = schema_node(schema, object_path)
        current_required = set(node.get("required", []))
        added = current_required - allowed_required
        if added:
            raise ValueError(
                f"{path}: {object_path or '<root>'} adds compatibility-breaking "
                f"required fields: {sorted(added)}"
            )
    for property_path, expected_type in contract["types"].items():
        actual_type = schema_node(schema, property_path).get("type")
        if actual_type != expected_type:
            raise ValueError(
                f"{path}: {property_path} type changed from {expected_type!r} "
                f"to {actual_type!r}"
            )
    for property_path, old_values in contract["enums"].items():
        current_values = set(schema_node(schema, property_path).get("enum", []))
        removed = old_values - current_values
        if removed:
            raise ValueError(
                f"{path}: {property_path} removed enum values: {sorted(removed)}"
            )


def schema_node(schema: dict, property_path: str) -> dict:
    node = schema
    if not property_path:
        return node
    for segment in property_path.split("."):
        properties = node.get("properties", {})
        if segment not in properties:
            raise ValueError(f"CRD compatibility field {property_path!r} is missing")
        node = properties[segment]
    return node


def main() -> None:
    paths = yaml_files()
    if not paths:
        raise SystemExit("error: no YAML files found")

    documents = [(path, document) for path in paths for document in load_documents(path)]
    validate_crds(documents)
    print(
        f"Parsed {len(paths)} YAML files and validated "
        f"{len(EXPECTED_CRDS)} additive CRD contracts and the upgrade baseline."
    )


if __name__ == "__main__":
    try:
        main()
    except ValueError as exc:
        print(f"error: {exc}", file=sys.stderr)
        raise SystemExit(1) from exc
