"""Generate command."""

from __future__ import annotations

import hashlib
import importlib.util
from pathlib import Path
import sys

from inferops import App, render_yaml


def register(subcommands):
    """Register the generate command."""
    parser = subcommands.add_parser("generate", help="Generate Kubernetes YAML from an application file.")
    parser.add_argument("app", help="Path to the application file.")
    parser.set_defaults(handler=run)


def run(args) -> int:
    """Run the generate command."""
    try:
        app = _load_app(args.app)
        sys.stdout.write(render_yaml(app))
        return 0
    except Exception as err:
        print(f"error: {err}", file=sys.stderr)
        return 1


def _load_app(app_path: str) -> App:
    path = Path(app_path).expanduser().resolve()
    if not path.exists():
        raise FileNotFoundError(f"application file does not exist: {path}")
    if not path.is_file():
        raise ValueError(f"application path must be a file: {path}")

    module_name = f"inferops_app_{hashlib.sha256(str(path).encode('utf-8')).hexdigest()[:12]}"
    spec = importlib.util.spec_from_file_location(module_name, path)
    if spec is None or spec.loader is None:
        raise ValueError(f"could not load Python module from {path}")

    module = importlib.util.module_from_spec(spec)
    sys.path.insert(0, str(path.parent))
    try:
        spec.loader.exec_module(module)
    finally:
        sys.path.pop(0)

    explicit_app = getattr(module, "app", None)
    if isinstance(explicit_app, App):
        return explicit_app

    discovered_apps = [value for value in module.__dict__.values() if isinstance(value, App)]
    if not discovered_apps:
        raise ValueError("no inferops.App instance found; define one as `app = inferops.App(...)`")
    if len(discovered_apps) > 1:
        raise ValueError("multiple inferops.App instances found; export the intended one as `app`")
    return discovered_apps[0]
