#!/usr/bin/env python3
"""Inspect only the execution host and calculate a conservative disk budget."""

from __future__ import annotations

import argparse
import importlib.metadata
import json
import math
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys

GIB = 1024**3


def cache_budget(total: int, free: int, existing: int, artifact: int | None) -> dict:
    """Budget a new artifact without counting existing unrelated caches as free."""
    if min(total, free, existing) < 0 or free > total or existing > total - free:
        raise ValueError("inconsistent filesystem measurements")
    if artifact is not None and artifact <= 0:
        raise ValueError("artifact bytes must be positive")
    reserve = max(10 * GIB, math.ceil(total * 0.15))
    additional = max(0, free - reserve)
    required = 2 * artifact if artifact is not None else None
    chosen = max(GIB, required) if required is not None else min(50 * GIB, additional // 2)
    fits = additional >= chosen and chosen >= GIB
    declared_gib = math.ceil((existing + chosen) / GIB) if fits else None
    if declared_gib is not None and declared_gib * GIB > existing + additional:
        fits = False
        declared_gib = None
    return {
        "reserve_bytes": reserve,
        "existing_cache_bytes": existing,
        "safe_additional_bytes": additional,
        "required_download_workspace_bytes": required,
        "recommended_capacity": f"{declared_gib}Gi" if fits else None,
        "fits_disk_policy": fits,
        "model_size_known": artifact is not None,
        "policy": "15% filesystem or 10Gi reserve; 2x new artifact for staging; "
        "without a model use at most 50Gi or half the safe additional space",
        "scope": "declared scheduling budget, not a physical filesystem quota",
    }


def read_memory(path: Path | None = None) -> dict:
    if path is None and platform.system() == "Darwin":
        try:
            total = int(subprocess.check_output(["sysctl", "-n", "hw.memsize"], text=True, timeout=5))
            stats = subprocess.check_output(["vm_stat"], text=True, timeout=5)
            page_size = int(re.search(r"page size of (\d+) bytes", stats).group(1))
            pages = dict(re.findall(r"^(Pages (?:free|inactive|speculative)):\s*(\d+)\.", stats, re.MULTILINE))
            if len(pages) != 3:
                raise ValueError("incomplete VM statistics")
            available = sum(int(value) for value in pages.values()) * page_size
            if total <= 0 or not 0 <= available <= total:
                raise ValueError("inconsistent VM statistics")
            return {"total_bytes": total, "available_bytes": available,
                    "source": "macOS vm_stat free+inactive+speculative pages; reclaimable estimate"}
        except (OSError, ValueError, AttributeError, subprocess.SubprocessError):
            return {"total_bytes": None, "available_bytes": None}
    path = path or Path("/proc/meminfo")
    try:
        fields = {}
        for line in path.read_text().splitlines():
            key, value = line.split(":", 1)
            if key in ("MemTotal", "MemAvailable"):
                fields[key] = int(value.split()[0]) * 1024
        return {
            "total_bytes": fields.get("MemTotal"),
            "available_bytes": fields.get("MemAvailable"),
        }
    except (OSError, ValueError, IndexError):
        return {"total_bytes": None, "available_bytes": None}


def memory_budget(total: int | None, available: int | None) -> dict | None:
    """Keep host/platform headroom distinct from the persistent disk cache."""
    if total is None or available is None:
        return None
    if total <= 0 or not 0 <= available <= total:
        raise ValueError("inconsistent memory measurements")
    reserve = max(2 * GIB, math.ceil(total * 0.20))
    return {
        "reserve_bytes": reserve,
        "candidate_model_budget_bytes": max(0, available - reserve),
        "policy": "retain 20% of total RAM or 2Gi for the OS, Kubernetes and control plane",
        "scope": "planning ceiling on this execution host; remeasure node/VM and subtract workload requests before use; not model-fit proof",
    }


def nearest_existing(path: Path) -> Path:
    current = path.expanduser().absolute()
    while not current.exists() and current != current.parent:
        current = current.parent
    return current.resolve()


def disk_usage_bytes(path: Path) -> int | None:
    if not path.exists():
        return 0
    try:
        result = subprocess.run(
            ["du", "-sk", str(path)], capture_output=True, text=True,
            timeout=20, check=False,
        )
        if result.returncode == 0:
            return int(result.stdout.split()[0]) * 1024
    except (OSError, ValueError, IndexError, subprocess.TimeoutExpired):
        pass
    return None


def inspect(cache_path: Path, artifact_bytes: int | None = None) -> dict:
    measured = nearest_existing(cache_path)
    disk = shutil.disk_usage(measured)
    existing = disk_usage_bytes(cache_path.expanduser().absolute())
    versions = {}
    for package in ("inferops", "inferops-cli"):
        try:
            versions[package] = importlib.metadata.version(package)
        except importlib.metadata.PackageNotFoundError:
            versions[package] = None
    kernel = platform.release()
    memory = read_memory()
    result = {
        "scope": "execution_host_only; never use as a remote node or Docker VM measurement",
        "system": platform.system(),
        "architecture": platform.machine(),
        "kernel": kernel,
        "wsl": "microsoft" in kernel.lower() or bool(os.environ.get("WSL_INTEROP")),
        "cpu_count": os.cpu_count(),
        "memory": memory,
        "memory_plan": memory_budget(memory["total_bytes"], memory["available_bytes"]),
        "tools": {name: shutil.which(name) is not None for name in (
            "kubectl", "helm", "k3s", "kind", "docker", "podman", "inferops",
            "nvidia-smi", "nvidia-ctk", "systemctl", "curl", "pipx",
        )},
        "packages": versions,
        "storage": {
            "requested_path": str(cache_path),
            "measured_existing_path": str(measured),
            "total_bytes": disk.total,
            "free_bytes": disk.free,
            "existing_cache_bytes": existing,
        },
        "cache_plan": None,
    }
    if existing is not None:
        result["cache_plan"] = cache_budget(disk.total, disk.free, existing, artifact_bytes)
    return result


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cache-path", type=Path, required=True)
    parser.add_argument("--artifact-bytes", type=int)
    args = parser.parse_args()
    try:
        report = inspect(args.cache_path, args.artifact_bytes)
    except (OSError, ValueError) as exc:
        print(json.dumps({"error": str(exc)}))
        return 2
    print(json.dumps(report, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
