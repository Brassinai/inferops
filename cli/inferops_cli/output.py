"""Output helpers for CLI commands."""

from __future__ import annotations

from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field
import json
import sys
from typing import Any

import yaml

REDACTED = "***REDACTED***"
SENSITIVE_KEYS = {
    "accesstoken",
    "authorization",
    "bearertoken",
    "certificateauthoritydata",
    "clientcertificatedata",
    "clientkeydata",
    "data",
    "idtoken",
    "kubeconfig",
    "kubeconfigcontent",
    "kubeconfigcontents",
    "password",
    "refreshtoken",
    "secretdata",
    "secretvalue",
    "stringdata",
    "token",
    "tokenvalue",
}


@dataclass(frozen=True)
class CommandResult:
    """Rendered result for a CLI command."""

    summary: str
    payload: Mapping[str, Any]
    details: Sequence[str] = field(default_factory=tuple)


def emit_result(output_format: str, result: CommandResult) -> None:
    """Emit one command result in the requested format."""
    safe_payload = _redact(result.payload)
    if output_format == "json":
        json.dump(safe_payload, sys.stdout, indent=2, sort_keys=True)
        sys.stdout.write("\n")
        return
    if output_format == "yaml":
        yaml.safe_dump(safe_payload, sys.stdout, sort_keys=False)
        return

    print(result.summary)
    for line in result.details:
        print(line)


def _redact(value: Any, key: str | None = None) -> Any:
    if _is_sensitive_key(key):
        return REDACTED
    if isinstance(value, Mapping):
        return {item_key: _redact(item_value, item_key) for item_key, item_value in value.items()}
    if isinstance(value, list):
        return [_redact(item) for item in value]
    return value


def _is_sensitive_key(key: str | None) -> bool:
    if key is None:
        return False
    normalized = "".join(character for character in key.lower() if character.isalnum())
    return normalized in SENSITIVE_KEYS
