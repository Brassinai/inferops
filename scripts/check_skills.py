#!/usr/bin/env python3
"""Validate standalone skill bundles and parse their InferOps command examples."""

from __future__ import annotations

import ast
from pathlib import Path
import re
import shlex
import sys
import urllib.parse

import yaml

ROOT = Path(__file__).resolve().parent.parent
BUNDLED_COPIES = {
    "inferops-install/references/operations.md": [
        f"{name}/references/operations.md" for name in (
            "inferops-deploy", "inferops-status", "inferops-status-fix",
            "inferops-observability", "inferops-gateway", "inferops-cloudconnect",
            "inferops-deactivate", "inferops-switchdeploy", "inferops-uninstall",
        )
    ],
    "inferops-install/references/capacity.md": [
        "inferops-deploy/references/capacity.md",
        "inferops-switchdeploy/references/capacity.md",
    ],
    "inferops-status/references/runbook.md": ["inferops-status-fix/references/status.md"],
    "inferops-deploy/references/runbook.md": ["inferops-switchdeploy/references/deployment.md"],
    "inferops-deploy/scripts/model_plan.py": ["inferops-switchdeploy/scripts/model_plan.py"],
    "inferops-deploy/scripts/create_hf_secret.py": ["inferops-switchdeploy/scripts/create_hf_secret.py"],
    "inferops-deploy/scripts/smoke_inference.py": [
        "inferops-gateway/scripts/smoke_inference.py",
        "inferops-switchdeploy/scripts/smoke_inference.py",
    ],
}


def check_bundle(skill: Path, parser=None) -> list[str]:
    errors = []
    entrypoint = skill / "SKILL.md"
    try:
        content = entrypoint.read_text()
        match = re.match(r"^---\n(.*?)\n---\n", content, re.DOTALL)
        metadata = yaml.safe_load(match.group(1)) if match else None
        if not isinstance(metadata, dict):
            raise ValueError("missing YAML frontmatter")
        name = metadata.get("name")
        if (name != skill.name or not isinstance(name, str)
                or not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", name)
                or len(name) > 64):
            raise ValueError("skill name must match its folder in kebab-case")
        description = metadata.get("description")
        if not isinstance(description, str) or not 1 <= len(description.strip()) <= 1024:
            raise ValueError("missing or oversized description")
    except (OSError, ValueError, yaml.YAMLError) as exc:
        return [f"{entrypoint}: {exc}"]

    for path in sorted(skill.rglob("*")):
        if path.is_symlink():
            errors.append(f"{path}: skill bundles must not rely on symlinks")
            continue
        if not path.is_file():
            continue
        if path.name == ".gitkeep":
            errors.append(f"{path}: remove unused scaffold resource")
        if path.suffix == ".py":
            try:
                ast.parse(path.read_text(), filename=str(path))
            except (OSError, SyntaxError) as exc:
                errors.append(f"{path}: {exc}")
        if path.suffix != ".md":
            continue
        text = path.read_text()
        if re.search(r"\[TODO:|maturity:\s*scaffold|This scaffold", text):
            errors.append(f"{path}: unfinished skill scaffold")
        for target in re.findall(r"\[[^\]]+\]\(([^)\s]+)\)", text):
            url = urllib.parse.urlsplit(target)
            if url.scheme or url.netloc or target.startswith("#"):
                continue
            destination = (path.parent / urllib.parse.unquote(url.path)).resolve()
            if not destination.is_relative_to(skill.resolve()) or not destination.is_file():
                errors.append(f"{path}: reference is missing or outside installed bundle: {target}")
        if parser is not None:
            for block in re.findall(r"~~~(?:sh|bash|shell)\n(.*?)~~~", text, re.DOTALL):
                for line in block.replace("\\\n", " ").splitlines():
                    command = line.strip()
                    if not command.startswith("inferops "):
                        continue
                    try:
                        args = shlex.split(command, comments=True)[1:]
                        args = ["cpu" if arg == "$COMPUTE_PROFILE" else arg for arg in args]
                        parser.parse_args(args)
                    except (Exception, SystemExit) as exc:
                        errors.append(f"{path}: invalid CLI example {command!r}: {exc}")
    try:
        interface = yaml.safe_load((skill / "agents/openai.yaml").read_text())["interface"]
        if not 25 <= len(interface["short_description"]) <= 64:
            errors.append(f"{skill}: short description must have 25-64 characters")
    except (OSError, KeyError, TypeError, yaml.YAMLError) as exc:
        errors.append(f"{skill}: invalid Codex presentation metadata: {exc}")
    return errors


def main() -> int:
    sys.path[:0] = [str(ROOT / "cli"), str(ROOT / "sdk/python")]
    from inferops_cli.main import build_parser

    parser = build_parser()
    skills = sorted((ROOT / "skills").glob("*/SKILL.md"))
    if not skills:
        print("error: no skill bundles found", file=sys.stderr)
        return 1
    errors = [error for entry in skills for error in check_bundle(entry.parent, parser)]
    for canonical, copies in BUNDLED_COPIES.items():
        source = ROOT / "skills" / canonical
        if not source.is_file():
            errors.append(f"{source}: missing canonical bundled resource")
            continue
        for copy in copies:
            destination = ROOT / "skills" / copy
            if not destination.is_file() or destination.read_bytes() != source.read_bytes():
                errors.append(f"{destination}: bundled copy differs from {source}")
    if errors:
        print("\n".join(errors), file=sys.stderr)
        return 1
    print(f"Validated {len(skills)} standalone skills, local references, Python helpers, and CLI examples.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
