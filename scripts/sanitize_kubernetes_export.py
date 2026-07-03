#!/usr/bin/env python3
"""Remove cluster-specific state from Kubernetes JSON before logical backup."""

from __future__ import annotations

import copy
import json
import sys
from typing import Any


EPHEMERAL_METADATA_FIELDS = {
    "creationTimestamp",
    "deletionGracePeriodSeconds",
    "deletionTimestamp",
    "generation",
    "managedFields",
    "ownerReferences",
    "resourceVersion",
    "selfLink",
    "uid",
}
LAST_APPLIED_ANNOTATION = "kubectl.kubernetes.io/last-applied-configuration"


def sanitize_document(document: dict[str, Any]) -> dict[str, Any]:
    """Return a restorable copy of one Kubernetes object or List."""
    sanitized = copy.deepcopy(document)
    if sanitized.get("kind") == "List":
        items = sanitized.get("items")
        if not isinstance(items, list):
            raise ValueError("Kubernetes List.items must be an array")
        sanitized["items"] = [_sanitize_object(item) for item in items]
        sanitized.pop("metadata", None)
        return sanitized
    return _sanitize_object(sanitized)


def _sanitize_object(document: Any) -> dict[str, Any]:
    if not isinstance(document, dict):
        raise ValueError("Kubernetes object must be a mapping")
    document.pop("status", None)
    metadata = document.get("metadata")
    if isinstance(metadata, dict):
        for field in EPHEMERAL_METADATA_FIELDS:
            metadata.pop(field, None)
        annotations = metadata.get("annotations")
        if isinstance(annotations, dict):
            annotations.pop(LAST_APPLIED_ANNOTATION, None)
            if not annotations:
                metadata.pop("annotations", None)
    return document


def main() -> int:
    try:
        document = json.load(sys.stdin)
        if not isinstance(document, dict):
            raise ValueError("input must be a Kubernetes JSON object")
        json.dump(sanitize_document(document), sys.stdout, indent=2, sort_keys=True)
        sys.stdout.write("\n")
    except (json.JSONDecodeError, ValueError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
