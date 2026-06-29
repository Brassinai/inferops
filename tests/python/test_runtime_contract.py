"""Contract tests for the thin runtime image entrypoints."""

from __future__ import annotations

import os
import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
ENTRYPOINTS = {
    "nano-vllm": ROOT / "runtimes/nano_vllm/entrypoint.sh",
    "vllm": ROOT / "runtimes/vllm/entrypoint.sh",
    "sglang": ROOT / "runtimes/sglang/entrypoint.sh",
    "llama-cpp": ROOT / "runtimes/llama_cpp/entrypoint.sh",
}


class RuntimeContractTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp_dir.cleanup)
        self.temp_path = Path(self.temp_dir.name)
        self.model_path = self.temp_path / "model cache"
        self.model_path.mkdir()
        self.capture_path = self.temp_path / "argv"
        self.bin_path = self.temp_path / "bin"
        self.bin_path.mkdir()

        fake_command = (
            "#!/bin/sh\n"
            ': > "$CAPTURE_PATH"\n'
            "for argument do\n"
            '    printf "%s\\n" "$argument" >> "$CAPTURE_PATH"\n'
            "done\n"
        )
        for command in ("nanovllm", "vllm", "python3", "llama-server"):
            path = self.bin_path / command
            path.write_text(fake_command, encoding="utf-8")
            path.chmod(0o755)

    def run_entrypoint(
        self,
        runtime: str,
        *,
        model_path: str | None = None,
        extra_env: dict[str, str] | None = None,
        extra_args: tuple[str, ...] = (),
    ) -> subprocess.CompletedProcess[str]:
        env = {
            "PATH": f"{self.bin_path}:{os.environ['PATH']}",
            "CAPTURE_PATH": str(self.capture_path),
        }
        if model_path is not None:
            env["MODEL_PATH"] = model_path
        if extra_env:
            env.update(extra_env)
        return subprocess.run(
            ["/bin/sh", str(ENTRYPOINTS[runtime]), *extra_args],
            env=env,
            text=True,
            capture_output=True,
            check=False,
        )

    def captured_args(self) -> list[str]:
        return self.capture_path.read_text(encoding="utf-8").splitlines()

    def test_entrypoints_require_model_cache_path(self) -> None:
        for runtime in ENTRYPOINTS:
            with self.subTest(runtime=runtime):
                result = self.run_entrypoint(runtime)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("MODEL_PATH", result.stderr)

    def test_entrypoints_reject_missing_model_cache_directory(self) -> None:
        missing = str(self.temp_path / "missing")
        for runtime in ENTRYPOINTS:
            with self.subTest(runtime=runtime):
                result = self.run_entrypoint(runtime, model_path=missing)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("accessible directory", result.stderr)

    def test_vllm_maps_contract_to_upstream_cli(self) -> None:
        result = self.run_entrypoint(
            "vllm",
            model_path=str(self.model_path),
            extra_env={
                "HOST": "127.0.0.1",
                "PORT": "9000",
                "MODEL_REPO": "org/model",
                "TENSOR_PARALLEL_SIZE": "2",
                "MODEL_DTYPE": "bfloat16",
                "MAX_MODEL_LEN": "4096",
                "GPU_MEMORY_UTILIZATION": "0.9",
                "MAX_NUM_SEQS": "64",
                "ENFORCE_EAGER": "true",
            },
            extra_args=("--disable-uvicorn-access-log",),
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(
            self.captured_args(),
            [
                "serve",
                str(self.model_path),
                "--host",
                "127.0.0.1",
                "--port",
                "9000",
                "--disable-uvicorn-access-log",
                "--served-model-name",
                "org/model",
                "--tensor-parallel-size",
                "2",
                "--dtype",
                "bfloat16",
                "--max-model-len",
                "4096",
                "--gpu-memory-utilization",
                "0.9",
                "--max-num-seqs",
                "64",
                "--enforce-eager",
            ],
        )

    def test_sglang_enables_metrics_and_maps_contract(self) -> None:
        result = self.run_entrypoint(
            "sglang",
            model_path=str(self.model_path),
            extra_env={
                "MODEL_REPO": "org/model",
                "TENSOR_PARALLEL_SIZE": "2",
                "MODEL_DTYPE": "bfloat16",
                "MAX_MODEL_LEN": "4096",
                "GPU_MEMORY_UTILIZATION": "0.8",
                "MAX_NUM_SEQS": "32",
                "ENFORCE_EAGER": "true",
            },
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(
            self.captured_args(),
            [
                "-m",
                "sglang.launch_server",
                "--model-path",
                str(self.model_path),
                "--host",
                "0.0.0.0",
                "--port",
                "8000",
                "--enable-metrics",
                "--served-model-name",
                "org/model",
                "--tp-size",
                "2",
                "--dtype",
                "bfloat16",
                "--context-length",
                "4096",
                "--mem-fraction-static",
                "0.8",
                "--max-running-requests",
                "32",
                "--disable-prefill-cuda-graph",
                "--disable-decode-cuda-graph",
            ],
        )

    def test_nano_vllm_uses_standard_engine_server_cli(self) -> None:
        result = self.run_entrypoint(
            "nano-vllm",
            model_path=str(self.model_path),
            extra_env={"MODEL_REPO": "org/model"},
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(
            self.captured_args(),
            [
                "serve",
                str(self.model_path),
                "--host",
                "0.0.0.0",
                "--port",
                "8000",
                "--served-model-name",
                "org/model",
            ],
        )

    def test_llama_cpp_maps_cpu_contract_and_enables_metrics(self) -> None:
        (self.model_path / "model.gguf").touch()
        result = self.run_entrypoint(
            "llama-cpp",
            model_path=str(self.model_path),
            extra_env={
                "MODEL_FILE": "model.gguf",
                "MODEL_REPO": "org/model",
                "MAX_MODEL_LEN": "2048",
                "MAX_NUM_SEQS": "4",
                "CPU_THREADS": "6",
                "CPU_THREADS_BATCH": "8",
                "LLAMA_SERVER_BIN": str(self.bin_path / "llama-server"),
            },
            extra_args=("--no-warmup",),
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(
            self.captured_args(),
            [
                "--model",
                str(self.model_path / "model.gguf"),
                "--host",
                "0.0.0.0",
                "--port",
                "8000",
                "--metrics",
                "--no-warmup",
                "--alias",
                "org/model",
                "--ctx-size",
                "2048",
                "--parallel",
                "4",
                "--threads",
                "6",
                "--threads-batch",
                "8",
            ],
        )

    def test_llama_cpp_auto_detects_one_gguf_file(self) -> None:
        gguf_path = self.model_path / "model.gguf"
        gguf_path.touch()
        result = self.run_entrypoint(
            "llama-cpp",
            model_path=str(self.model_path),
            extra_env={"LLAMA_SERVER_BIN": str(self.bin_path / "llama-server")},
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.captured_args()[0:2], ["--model", str(gguf_path)])

    def test_llama_cpp_requires_an_unambiguous_gguf_file(self) -> None:
        for filename in ("first.gguf", "second.gguf"):
            (self.model_path / filename).touch()
        result = self.run_entrypoint(
            "llama-cpp",
            model_path=str(self.model_path),
            extra_env={"LLAMA_SERVER_BIN": str(self.bin_path / "llama-server")},
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("exactly one", result.stderr)
        self.assertFalse(self.capture_path.exists())

    def test_llama_cpp_rejects_model_file_outside_cache_root(self) -> None:
        outside_path = self.temp_path / "outside.gguf"
        outside_path.touch()
        result = self.run_entrypoint(
            "llama-cpp",
            model_path=str(self.model_path),
            extra_env={
                "MODEL_FILE": str(outside_path),
                "LLAMA_SERVER_BIN": str(self.bin_path / "llama-server"),
            },
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("relative to MODEL_PATH", result.stderr)
        self.assertFalse(self.capture_path.exists())

    def test_model_repo_never_replaces_model_cache_path(self) -> None:
        result = self.run_entrypoint(
            "vllm",
            extra_env={"MODEL_REPO": "org/download-me"},
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.capture_path.exists())

    def test_invalid_boolean_fails_before_starting_engine(self) -> None:
        result = self.run_entrypoint(
            "vllm",
            model_path=str(self.model_path),
            extra_env={"ENFORCE_EAGER": "sometimes"},
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("ENFORCE_EAGER", result.stderr)
        self.assertFalse(self.capture_path.exists())

    def test_vllm_cpu_image_uses_pinned_upstream_cpu_build(self) -> None:
        dockerfile = (ROOT / "runtimes/vllm/Dockerfile.cpu").read_text(
            encoding="utf-8"
        )
        self.assertIn("vllm/vllm-openai-cpu:", dockerfile)
        self.assertIn("@sha256:", dockerfile)
        self.assertIn("runtimes/vllm/entrypoint.sh", dockerfile)


if __name__ == "__main__":
    unittest.main()
