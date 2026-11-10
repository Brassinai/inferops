#!/usr/bin/env python3
"""Create a download Secret through stdin without logging the Hugging Face token."""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys

DNS = re.compile(r"^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$")


def create(context: str, namespace: str, name: str, token: str,
           kubeconfig: str | None = None) -> dict:
    if not context or context.startswith("-"):
        raise ValueError("explicit context is required")
    if any(not DNS.fullmatch(v) or len(v) > 63 for v in (namespace, name)):
        raise ValueError("namespace and Secret name must be DNS labels")
    if not token.strip():
        raise ValueError("HF_TOKEN must be supplied securely")
    command = ["kubectl", "--context", context, "--namespace", namespace]
    if kubeconfig:
        command += ["--kubeconfig", kubeconfig]
    manifest = {
        "apiVersion": "v1", "kind": "Secret", "type": "Opaque",
        "metadata": {"name": name, "namespace": namespace},
        "stringData": {"token": token.strip()},
    }
    # Create-only: a supplied token does not authorize rotating an existing Secret.
    result = subprocess.run(
        command + ["create", "-f", "-"], input=json.dumps(manifest),
        text=True, capture_output=True, timeout=30, check=False,
    )
    return {
        "created": result.returncode == 0, "secret_name": name,
        "namespace": namespace, "exit_code": result.returncode,
        "note": "existing Secrets are not overwritten; inspect existence and permissions on failure",
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--context", required=True)
    parser.add_argument("--namespace", required=True)
    parser.add_argument("--name", default="hf-token")
    parser.add_argument("--kubeconfig")
    args = parser.parse_args()
    try:
        report = create(args.context, args.namespace, args.name,
                        os.environ.get("HF_TOKEN", ""), args.kubeconfig)
    except (OSError, ValueError, subprocess.TimeoutExpired):
        print(json.dumps({"created": False, "error": "check context, permissions, and secure HF_TOKEN input"}))
        return 1
    print(json.dumps(report))
    return 0 if report["created"] else 1


if __name__ == "__main__":
    sys.exit(main())
