#!/usr/bin/env python3
"""Safe InferOps cache downloader.

Downloads a model revision into a staging directory and atomically publishes it
by writing a completion marker. The controller treats a completed Job with the
expected input hash as evidence of readiness; directory existence alone is not
sufficient.
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import sys
import tempfile
import time
from dataclasses import asdict, dataclass
from datetime import datetime, timezone
from pathlib import Path

MANIFEST_NAME = ".inferops-cache.json"


@dataclass(frozen=True)
class CacheManifest:
    repo: str
    requested_revision: str
    revision: str
    source: str
    byte_size: int
    completed_at: str
    input_hash: str


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="InferOps cache downloader")
    parser.add_argument("--source", required=True, choices=["huggingface"])
    parser.add_argument("--repo", required=True)
    parser.add_argument("--revision", default="main")
    parser.add_argument("--cache-root", required=True)
    parser.add_argument("--dest-subpath", required=True)
    parser.add_argument("--staging-subpath", required=True)
    parser.add_argument("--input-hash", default="")
    return parser.parse_args()


def main() -> int:
    args = parse_args()

    cache_root = Path(args.cache_root)
    dest_path = cache_root / args.dest_subpath
    staging_path = cache_root / args.staging_subpath
    manifest_path = dest_path / MANIFEST_NAME

    # Refuse to run if the destination escapes the cache root.
    try:
        dest_path.resolve().relative_to(cache_root.resolve())
        staging_path.resolve().relative_to(cache_root.resolve())
    except ValueError as exc:
        print(f"error: destination or staging escapes cache root: {exc}", file=sys.stderr)
        return 1

    # If a valid marker for the same identity already exists, succeed without
    # downloading. Refuse to overwrite a marker for a different identity.
    if manifest_path.exists():
        try:
            existing = json.loads(manifest_path.read_text())
            if manifest_matches(existing, args):
                print(f"cache already complete: {dest_path}")
                return 0
        except Exception:
            pass
        print(f"error: destination already contains a different cache: {dest_path}", file=sys.stderr)
        return 1

    # Prepare a clean staging directory for this attempt.
    if staging_path.exists():
        shutil.rmtree(staging_path)
    staging_path.mkdir(parents=True, exist_ok=True)

    try:
        byte_size, resolved_revision = download_source(args, staging_path)

        # Write the marker inside staging, then publish the whole completed
        # directory with one atomic rename. A crash can therefore expose either
        # no destination or a destination containing its completion marker.
        manifest = CacheManifest(
            repo=args.repo,
            requested_revision=args.revision,
            revision=resolved_revision,
            source=args.source,
            byte_size=byte_size,
            completed_at=datetime.now(timezone.utc).isoformat(),
            input_hash=args.input_hash,
        )
        write_manifest_atomically(staging_path / MANIFEST_NAME, manifest)
        dest_path.parent.mkdir(parents=True, exist_ok=True)
        os.replace(staging_path, dest_path)
        fsync_directory(dest_path.parent)
        print(f"cache ready: {dest_path}")
        return 0
    except Exception as exc:
        print(f"error: download failed: {exc}", file=sys.stderr)
        # Clean only this attempt's staging directory.
        if staging_path.exists():
            shutil.rmtree(staging_path)
        return 1


def manifest_matches(existing: object, args: argparse.Namespace) -> bool:
    if not isinstance(existing, dict):
        return False
    byte_size = existing.get("byte_size")
    return (
        existing.get("repo") == args.repo
        and existing.get("source") == args.source
        and existing.get("input_hash") == args.input_hash
        and isinstance(existing.get("revision"), str)
        and bool(existing["revision"])
        and existing.get("requested_revision", args.revision) == args.revision
        and isinstance(byte_size, int)
        and not isinstance(byte_size, bool)
        and byte_size >= 0
        and isinstance(existing.get("completed_at"), str)
        and bool(existing["completed_at"])
    )


def download_source(args: argparse.Namespace, staging_path: Path) -> tuple[int, str]:
    if args.source == "huggingface":
        return download_huggingface(args.repo, args.revision, staging_path)
    raise ValueError(f"unsupported source: {args.source}")


def download_huggingface(
    repo: str, revision: str, staging_path: Path
) -> tuple[int, str]:
    """Download a Hugging Face repository revision using huggingface_hub."""
    try:
        from huggingface_hub import HfApi, snapshot_download  # type: ignore
    except ImportError as exc:
        raise RuntimeError("huggingface_hub is not installed") from exc

    token = os.environ.get("HF_TOKEN") or None
    model_info = HfApi(token=token).model_info(repo_id=repo, revision=revision)
    resolved_revision = model_info.sha
    if not resolved_revision:
        raise RuntimeError(f"Hugging Face did not resolve revision {revision!r}")
    downloaded = snapshot_download(
        repo_id=repo,
        revision=resolved_revision,
        local_dir=str(staging_path),
        local_dir_use_symlinks=False,
        token=token,
    )
    return total_size(Path(downloaded)), resolved_revision


def total_size(path: Path) -> int:
    size = 0
    for item in path.rglob("*"):
        if item.is_file():
            size += item.stat().st_size
    return size


def write_manifest_atomically(manifest_path: Path, manifest: CacheManifest) -> None:
    data = json.dumps(asdict(manifest), indent=2).encode("utf-8")
    fd, temp_path = tempfile.mkstemp(dir=str(manifest_path.parent), prefix=".manifest-")
    try:
        with os.fdopen(fd, "wb") as f:
            f.write(data)
            f.flush()
            os.fsync(f.fileno())
        os.replace(temp_path, manifest_path)
    except Exception:
        try:
            os.unlink(temp_path)
        except FileNotFoundError:
            pass
        raise


def fsync_directory(path: Path) -> None:
    fd = os.open(path, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


if __name__ == "__main__":
    sys.exit(main())
