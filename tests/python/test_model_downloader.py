"""Tests for the node-local model downloader's publish safety."""

from __future__ import annotations

import importlib.util
import io
import json
import sys
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path
from unittest import mock


def _load_downloader():
    path = Path(__file__).parents[2] / "runtimes" / "model-downloader" / "download.py"
    spec = importlib.util.spec_from_file_location("inferops_model_downloader", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load downloader from {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


downloader = _load_downloader()


class ModelDownloaderTests(unittest.TestCase):
    def _args(self, root: Path, *, input_hash: str = "sha256:test") -> list[str]:
        return [
            "download.py",
            "--source",
            "huggingface",
            "--repo",
            "org/model",
            "--revision",
            "main",
            "--cache-root",
            str(root),
            "--dest-subpath",
            "model",
            "--staging-subpath",
            "model.staging",
            "--input-hash",
            input_hash,
        ]

    def _run(self) -> int:
        with redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
            return downloader.main()

    def test_publishes_marker_with_directory(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)

            def fake_download(_args, staging: Path) -> int:
                (staging / "weights.bin").write_bytes(b"weights")
                return 7

            with (
                mock.patch.object(sys, "argv", self._args(root)),
                mock.patch.object(downloader, "download_source", side_effect=fake_download),
            ):
                self.assertEqual(self._run(), 0)

            destination = root / "model"
            self.assertEqual((destination / "weights.bin").read_bytes(), b"weights")
            manifest = json.loads((destination / downloader.MANIFEST_NAME).read_text())
            self.assertEqual(manifest["input_hash"], "sha256:test")
            self.assertFalse((root / "model.staging").exists())

    def test_existing_matching_marker_skips_download(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            destination = root / "model"
            destination.mkdir()
            (destination / downloader.MANIFEST_NAME).write_text(
                json.dumps({"input_hash": "sha256:test", "revision": "main"})
            )

            with (
                mock.patch.object(sys, "argv", self._args(root)),
                mock.patch.object(downloader, "download_source") as download_source,
            ):
                self.assertEqual(self._run(), 0)
            download_source.assert_not_called()

    def test_publish_failure_never_exposes_incomplete_destination(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)

            def fake_download(_args, staging: Path) -> int:
                (staging / "weights.bin").write_bytes(b"weights")
                return 7

            original_replace = downloader.os.replace

            def fail_directory_publish(source, destination):
                if Path(source) == root / "model.staging":
                    raise OSError("simulated publish failure")
                return original_replace(source, destination)

            with (
                mock.patch.object(sys, "argv", self._args(root)),
                mock.patch.object(downloader, "download_source", side_effect=fake_download),
                mock.patch.object(downloader.os, "replace", side_effect=fail_directory_publish),
            ):
                self.assertEqual(self._run(), 1)

            self.assertFalse((root / "model").exists())
            self.assertFalse((root / "model.staging").exists())

    def test_rejects_destination_outside_root(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            args = self._args(root)
            args[args.index("--dest-subpath") + 1] = "../escape"
            with mock.patch.object(sys, "argv", args):
                self.assertEqual(self._run(), 1)


if __name__ == "__main__":
    unittest.main()
