"""Local deployment state for idempotent CLI operations."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

STATE_DIR = ".inferops"
STATE_FILE = "state.json"


def state_path(project_dir: Path | str = ".") -> Path:
    """Return the InferOps state file path for a project."""
    return Path(project_dir) / STATE_DIR / STATE_FILE


def load_state(project_dir: Path | str = ".") -> dict[str, Any]:
    """Load local deployment state.

    Returns a fresh empty state if the file is missing or corrupted.
    """
    path = state_path(project_dir)
    if not path.exists():
        return {"version": 1, "deployments": {}}
    try:
        return json.loads(path.read_text())
    except (json.JSONDecodeError, OSError):
        return {"version": 1, "deployments": {}}


def save_state(state: dict[str, Any], project_dir: Path | str = ".") -> None:
    """Save local deployment state."""
    path = state_path(project_dir)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(state, indent=2, sort_keys=True) + "\n")
