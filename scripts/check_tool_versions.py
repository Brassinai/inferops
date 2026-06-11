#!/usr/bin/env python3
"""Check the minimum tool versions required by make verify."""

from __future__ import annotations

import argparse
import re
import shutil
import subprocess
import sys


VERSION_PATTERN = re.compile(r"\d+(?:\.\d+)+")


def version_tuple(value: str) -> tuple[int, ...]:
    match = VERSION_PATTERN.search(value)
    if not match:
        raise ValueError(f"could not parse version from {value!r}")
    return tuple(int(part) for part in match.group(0).split("."))


def output(command: str, *args: str) -> str:
    return subprocess.check_output((command, *args), text=True, stderr=subprocess.STDOUT).strip()


def require(name: str, actual: str, minimum: str) -> None:
    if version_tuple(actual) < version_tuple(minimum):
        raise ValueError(f"{name} {minimum}+ is required; found {actual}")


def kubeconform_version(go_command: str, kubeconform_command: str) -> str:
    reported = output(kubeconform_command, "-v")
    if VERSION_PATTERN.search(reported):
        return reported

    kubeconform_path = shutil.which(kubeconform_command)
    if not kubeconform_path:
        raise ValueError(f"could not locate kubeconform executable {kubeconform_command!r}")

    metadata = output(go_command, "version", "-m", kubeconform_path)
    module_line = next(
        (line for line in metadata.splitlines() if "github.com/yannh/kubeconform" in line and "\tmod\t" in line),
        "",
    )
    if not module_line:
        raise ValueError(f"could not determine kubeconform version from {reported!r}")
    return module_line


def main() -> None:
    parser = argparse.ArgumentParser()
    for name in ("go", "python", "helm", "kubeconform"):
        parser.add_argument(f"--{name}-command", required=True)
        parser.add_argument(f"--minimum-{name}", required=True)
    args = parser.parse_args()

    checks = (
        ("Go", output(args.go_command, "version"), args.minimum_go),
        ("Python", output(args.python_command, "--version"), args.minimum_python),
        ("Helm", output(args.helm_command, "version", "--short"), args.minimum_helm),
        ("kubeconform", kubeconform_version(args.go_command, args.kubeconform_command), args.minimum_kubeconform),
    )
    for name, actual, minimum in checks:
        require(name, actual, minimum)
    print("Required tool versions are available.")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, subprocess.CalledProcessError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        raise SystemExit(1) from exc
