#!/usr/bin/env python3
"""Inspect HF metadata without downloading weights or executing model code."""

from __future__ import annotations

import argparse
import json
import math
import os
from pathlib import Path
import re
import sys
import urllib.error
import urllib.parse
import urllib.request

GIB = 1024**3
LIMIT = 16 * 1024**2
REPO = re.compile(r"^[A-Za-z0-9_-][A-Za-z0-9._-]*/[A-Za-z0-9_-][A-Za-z0-9._-]*$")


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def fetch_metadata(repo: str, revision: str) -> dict:
    if not REPO.fullmatch(repo) or not revision.strip():
        raise ValueError("use an owner/model repository and non-empty revision")
    url = (
        "https://huggingface.co/api/models/" + urllib.parse.quote(repo, safe="/")
        + "/revision/" + urllib.parse.quote(revision, safe="") + "?blobs=true"
    )
    headers = {"Accept": "application/json"}
    token = os.environ.get("HF_TOKEN")
    if token:
        headers["Authorization"] = "Bearer " + token
    request = urllib.request.Request(url, headers=headers)
    with urllib.request.build_opener(NoRedirect).open(request, timeout=30) as response:
        body = response.read(LIMIT + 1)
    if len(body) > LIMIT:
        raise ValueError("metadata exceeds 16MiB limit")
    value = json.loads(body)
    if not isinstance(value, dict):
        raise ValueError("expected model metadata object")
    return value


def inventory(metadata: dict, runtime: str, model_file: str | None = None,
              mmproj_file: str | None = None) -> dict:
    if (model_file or mmproj_file) and runtime != "llama-cpp":
        raise ValueError("explicit GGUF selection requires llama-cpp")
    if mmproj_file and not model_file:
        raise ValueError("a projector requires an explicit language model file")
    for name in (model_file, mmproj_file):
        if name is not None and (not name.endswith(".gguf") or "/" in name or "\\" in name):
            raise ValueError("select a top-level GGUF filename from the snapshot")
    if model_file and model_file == mmproj_file:
        raise ValueError("language model and projector must be different artifacts")
    files = metadata.get("siblings")
    if not isinstance(files, list) or not files:
        raise ValueError("model metadata must include a non-empty siblings inventory")
    total = 0
    weights = 0
    unknown = []
    ggufs = []
    safetensors = []
    bins = []
    seen = set()
    for item in files:
        name = item.get("rfilename")
        if not isinstance(name, str) or not name or name in seen:
            raise ValueError("missing or duplicate artifact filename")
        seen.add(name)
        size = item.get("size")
        if size is None and isinstance(item.get("lfs"), dict):
            size = item["lfs"].get("size")
        if type(size) is not int or size < 0:
            unknown.append(name)
        else:
            total += size
        if name.endswith(".gguf"):
            ggufs.append(name)
        if name.endswith(".safetensors"):
            safetensors.append(name)
        if name.endswith(".bin") and ("model" in name or "pytorch" in name):
            bins.append(name)
    selected = ([model_file] + ([mmproj_file] if mmproj_file else [])) if model_file else (
        ggufs if runtime == "llama-cpp" else safetensors or bins)
    if any(name not in seen for name in selected):
        raise ValueError("selected GGUF artifact is absent from the pinned snapshot")
    blockers = []
    if runtime == "llama-cpp" and not model_file and (len(ggufs) != 1 or "/" in ggufs[0]):
        blockers.append("llama-cpp requires exactly one top-level GGUF in the snapshot")
    if not selected:
        blockers.append("no recognized weights for the requested runtime")
    for item in files:
        if item["rfilename"] in selected:
            size = item.get("size")
            if size is None and isinstance(item.get("lfs"), dict):
                size = item["lfs"].get("size")
            if type(size) is int and size >= 0:
                weights += size
    if unknown:
        blockers.append("artifact sizes are incomplete; obtain sizes before allocating storage")
    return {
        "snapshot_bytes": total if not unknown else None,
        "weight_bytes": weights if selected and not any(n in selected for n in unknown) else None,
        "unknown_size_files": unknown,
        "weight_files": selected,
        "model_file": model_file,
        "mmproj_file": mmproj_file,
        "requires_runtime_configuration": bool(model_file),
        "blockers": blockers,
    }


def plan(metadata: dict, runtime: str, free_disk: float | None,
         free_ram: float | None, free_vram: float | None,
         kv_gib: float | None, *, model_file: str | None = None,
         mmproj_file: str | None = None, vision_workspace_gib: float | None = None,
         runtime_overhead_gib: float = 2) -> dict:
    for value in (free_disk, free_ram, free_vram, kv_gib, vision_workspace_gib, runtime_overhead_gib):
        if value is not None and (not math.isfinite(value) or value < 0):
            raise ValueError("resource measurements must be finite and non-negative")
    report = inventory(metadata, runtime, model_file, mmproj_file)
    if runtime_overhead_gib is None or runtime_overhead_gib <= 0:
        raise ValueError("runtime overhead must be a positive estimate")
    if mmproj_file and (vision_workspace_gib is None or vision_workspace_gib <= 0):
        report["blockers"].append("vision encoder/processor workspace is unknown; supply a justified positive estimate")
    sha = metadata.get("sha")
    if not isinstance(sha, str) or not re.fullmatch(r"[0-9a-fA-F]{40}", sha):
        report["blockers"].append("metadata lacks a resolved commit; pin a verified model revision")
    report.update({
        "repository": metadata.get("id") or metadata.get("modelId"),
        "revision": sha,
        "runtime": runtime,
        "gated": bool(metadata.get("gated")),
        "requires_runtime_compatibility_check": True,
        "assessment": "unknown",
        "notes": [
            "The downloader snapshots the whole repository, not only selected weights.",
            "RAM/VRAM are conservative estimates; context/concurrency KV memory must be supplied.",
            "A passed preflight is not a benchmark or a guarantee of architecture compatibility.",
            "Use per-node measurements; aggregate free memory across nodes or GPUs is not one slot.",
            "Explicit GGUF selection requires MODEL_FILE and a dedicated runtime; the projector must match the model and engine build.",
            "Runtime overhead is separate from system reserves already subtracted from the free budget; vision workspace is additional.",
        ],
    })
    snapshot = report["snapshot_bytes"]
    weights = report["weight_bytes"]
    required_disk = math.ceil(2 * snapshot / GIB) if snapshot is not None else None
    required_ram = math.ceil((weights * (1.25 if runtime == "llama-cpp" else 1.0)) / GIB + runtime_overhead_gib + (kv_gib or 0) + (vision_workspace_gib or 0)) if weights else None
    required_vram = math.ceil(weights * 1.2 / GIB + runtime_overhead_gib + (kv_gib or 0)) if weights and runtime != "llama-cpp" else None
    report["estimated_required_gib"] = {
        "disk_with_workspace": required_disk,
        "ram_with_headroom": required_ram,
        "vram_with_headroom": required_vram,
        "kv_supplied": kv_gib,
        "vision_workspace_supplied": vision_workspace_gib,
        "runtime_overhead_supplied": runtime_overhead_gib,
    }
    comparisons = [(free_disk, required_disk), (free_ram, required_ram)]
    if runtime != "llama-cpp":
        comparisons.append((free_vram, required_vram))
    if any(have is not None and need is not None and have < need for have, need in comparisons):
        report["assessment"] = "insufficient"
    elif report["blockers"] or kv_gib is None or any(have is None or need is None for have, need in comparisons):
        report["assessment"] = "unknown"
    else:
        report["assessment"] = "candidate_requires_inference_test"
    return report


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument("--repo")
    source.add_argument("--metadata", type=Path)
    parser.add_argument("--revision", default="main")
    parser.add_argument("--runtime", choices=("llama-cpp", "nano-vllm", "vllm", "sglang"), required=True)
    parser.add_argument("--free-disk-gib", type=float, help="after OS/image/cache reserves")
    parser.add_argument("--free-ram-gib", type=float, help="after system and workload reserves")
    parser.add_argument("--free-vram-gib", type=float, help="on one compatible available GPU")
    parser.add_argument("--kv-gib", type=float, help="KV estimate for planned context and concurrency")
    parser.add_argument("--model-file", help="exact top-level language GGUF in the pinned snapshot")
    parser.add_argument("--mmproj-file", help="exact matching top-level vision-projector GGUF")
    parser.add_argument("--vision-workspace-gib", type=float, help="estimated vision encoder/processor working memory")
    parser.add_argument("--runtime-overhead-gib", type=float, default=2, help="runtime overhead excluding system reserve and KV; default 2Gi")
    args = parser.parse_args()
    try:
        if args.metadata:
            if args.metadata.stat().st_size > LIMIT:
                raise ValueError("metadata exceeds 16MiB limit")
            metadata = json.loads(args.metadata.read_text())
        else:
            metadata = fetch_metadata(args.repo, args.revision)
        if not isinstance(metadata, dict):
            raise ValueError("expected model metadata object")
        report = plan(metadata, args.runtime, args.free_disk_gib,
                      args.free_ram_gib, args.free_vram_gib, args.kv_gib,
                      model_file=args.model_file, mmproj_file=args.mmproj_file,
                      vision_workspace_gib=args.vision_workspace_gib,
                      runtime_overhead_gib=args.runtime_overhead_gib)
    except urllib.error.HTTPError as exc:
        print(json.dumps({"error": f"HF metadata HTTP {exc.code}; check repository access, revision, or HF_TOKEN"}))
        return 2
    except (OSError, ValueError, TypeError, AttributeError, urllib.error.URLError) as exc:
        # Do not echo remote response bodies, authorization headers, or URLs.
        print(json.dumps({"error": "metadata unavailable or invalid", "kind": type(exc).__name__}))
        return 2
    print(json.dumps(report, indent=2))
    return 0 if report["assessment"] == "candidate_requires_inference_test" else 1


if __name__ == "__main__":
    sys.exit(main())
