from __future__ import annotations

from contextlib import redirect_stdout
import importlib.util
import io
import json
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import threading
import unittest
from unittest.mock import patch
import urllib.error

ROOT = Path(__file__).resolve().parents[2]
SKILLS = ROOT / "skills"
GIB = 1024**3


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


inspector = load("skill_inspector", SKILLS / "inferops-install/scripts/inspect_system.py")
planner = load("skill_planner", SKILLS / "inferops-deploy/scripts/model_plan.py")
secret = load("skill_secret", SKILLS / "inferops-deploy/scripts/create_hf_secret.py")
validator = load("skill_validator", ROOT / "scripts/check_skills.py")


def metadata(files=None):
    return {
        "id": "example/model", "sha": "a" * 40,
        "siblings": files if files is not None else [
            {"rfilename": "config.json", "size": 1024},
            {"rfilename": "model.safetensors", "size": 4 * GIB},
        ],
    }


class CacheBudgetTests(unittest.TestCase):
    def test_existing_cache_does_not_reduce_new_model_workspace(self):
        report = inspector.cache_budget(100 * GIB, 50 * GIB, 20 * GIB, 8 * GIB)
        self.assertEqual(report["recommended_capacity"], "36Gi")
        self.assertEqual(report["required_download_workspace_bytes"], 16 * GIB)

    def test_preserves_os_reserve_instead_of_using_all_free_space(self):
        report = inspector.cache_budget(100 * GIB, 20 * GIB, 0, 4 * GIB)
        self.assertFalse(report["fits_disk_policy"])
        self.assertIsNone(report["recommended_capacity"])

    def test_rounding_cannot_overcommit_remaining_bytes(self):
        report = inspector.cache_budget(100 * GIB, 17 * GIB - 1, 0, GIB - 1)
        self.assertFalse(report["fits_disk_policy"])

    def test_unknown_model_has_bounded_provisional_budget(self):
        report = inspector.cache_budget(1000 * GIB, 800 * GIB, 10 * GIB, None)
        self.assertEqual(report["recommended_capacity"], "60Gi")
        self.assertFalse(report["model_size_known"])

    def test_small_full_disk_does_not_return_a_capacity(self):
        report = inspector.cache_budget(8 * GIB, 4 * GIB, 0, None)
        self.assertIsNone(report["recommended_capacity"])

    def test_small_model_rounds_up_to_a_useful_minimum_cache(self):
        report = inspector.cache_budget(100 * GIB, 50 * GIB, 0, 100 * 1024**2)
        self.assertTrue(report["fits_disk_policy"])
        self.assertEqual(report["recommended_capacity"], "1Gi")

    def test_invalid_measurements_fail(self):
        for values in ((10, 20, 0, None), (10, 8, 8, None), (10, 5, 0, -1)):
            with self.subTest(values=values), self.assertRaises(ValueError):
                inspector.cache_budget(*values)

    def test_missing_cache_path_is_measured_without_creation(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "missing/cache"
            self.assertEqual(inspector.nearest_existing(path), Path(directory).resolve())
            self.assertEqual(inspector.disk_usage_bytes(path), 0)
            self.assertFalse(path.exists())

    def test_unreadable_memory_is_unknown(self):
        self.assertIsNone(inspector.read_memory(Path("/nonexistent/inferops-meminfo"))["available_bytes"])

    def test_memory_parser_uses_available_not_free(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "meminfo"
            path.write_text("MemTotal: 100 kB\nMemFree: 1 kB\nMemAvailable: 50 kB\n")
            self.assertEqual(inspector.read_memory(path)["available_bytes"], 50 * 1024)


class ModelPlanTests(unittest.TestCase):
    def test_storage_counts_full_snapshot_with_duplicate_weight_formats(self):
        value = metadata([
            {"rfilename": "model.safetensors", "size": 4 * GIB},
            {"rfilename": "pytorch_model.bin", "size": 4 * GIB},
        ])
        report = planner.plan(value, "vllm", 40, 16, 16, 1)
        self.assertEqual(report["snapshot_bytes"], 8 * GIB)
        self.assertEqual(report["weight_bytes"], 4 * GIB)
        self.assertEqual(report["estimated_required_gib"]["disk_with_workspace"], 16)

    def test_lfs_size_fallback(self):
        report = planner.inventory(metadata([
            {"rfilename": "model.gguf", "lfs": {"size": GIB}},
        ]), "llama-cpp")
        self.assertEqual(report["snapshot_bytes"], GIB)

    def test_unknown_file_sizes_do_not_produce_a_fit(self):
        value = metadata([
            {"rfilename": "model.safetensors", "size": GIB},
            {"rfilename": "readme.md"},
        ])
        report = planner.plan(value, "vllm", 40, 16, 16, 1)
        self.assertEqual(report["assessment"], "unknown")
        self.assertIsNone(report["snapshot_bytes"])

    def test_multi_gguf_requires_an_explicit_runtime_override(self):
        value = metadata([
            {"rfilename": "q4.gguf", "size": GIB},
            {"rfilename": "q8.gguf", "size": 2 * GIB},
        ])
        report = planner.plan(value, "llama-cpp", 40, 16, None, 1)
        self.assertEqual(report["assessment"], "unknown")
        self.assertTrue(any("one top-level GGUF" in b for b in report["blockers"]))

    def test_gpu_inventory_never_invents_vram(self):
        report = planner.plan(metadata(), "vllm", 40, 16, None, 1)
        self.assertEqual(report["assessment"], "unknown")

    def test_missing_kv_estimate_is_unknown(self):
        report = planner.plan(metadata(), "vllm", 40, 16, 16, None)
        self.assertEqual(report["assessment"], "unknown")

    def test_insufficient_resources_block_fit(self):
        for budgets in ((1, 16, 16), (40, 1, 16), (40, 16, 1)):
            with self.subTest(budgets=budgets):
                self.assertEqual(planner.plan(metadata(), "vllm", *budgets, 1)["assessment"], "insufficient")

    def test_estimated_fit_still_requires_runtime_test(self):
        report = planner.plan(metadata(), "vllm", 40, 16, 16, 1)
        self.assertEqual(report["assessment"], "candidate_requires_inference_test")
        self.assertTrue(report["requires_runtime_compatibility_check"])

    def test_missing_resolved_revision_is_unknown(self):
        value = metadata()
        value["sha"] = "main"
        self.assertEqual(planner.plan(value, "vllm", 40, 16, 16, 1)["assessment"], "unknown")

    def test_negative_and_nan_budgets_rejected(self):
        for budget in (-1, float("nan"), float("inf")):
            with self.subTest(budget=budget), self.assertRaises(ValueError):
                planner.plan(metadata(), "vllm", budget, 16, 16, 1)

    def test_duplicate_filenames_rejected(self):
        value = metadata([{"rfilename": "model.gguf", "size": 1}] * 2)
        with self.assertRaises(ValueError):
            planner.inventory(value, "llama-cpp")


class SecretHelperTests(unittest.TestCase):
    def test_key_only_in_stdin_and_no_raw_error_output(self):
        fake_key = "fixture-only-not-a-real-token"
        result = subprocess.CompletedProcess([], 1, stdout=fake_key, stderr=fake_key)
        with patch.object(secret.subprocess, "run", return_value=result) as run:
            report = secret.create("test-context", "default", "hf-token", fake_key)
        args, kwargs = run.call_args
        self.assertNotIn(fake_key, " ".join(args[0]))
        self.assertEqual(json.loads(kwargs["input"])["stringData"]["token"], fake_key)
        self.assertEqual(args[0][-3:], ["create", "-f", "-"])
        self.assertNotIn(fake_key, json.dumps(report))
        self.assertFalse(report["created"])

    def test_missing_key_does_not_call_kubectl(self):
        with patch.object(secret.subprocess, "run") as run:
            with self.assertRaises(ValueError):
                secret.create("test", "default", "hf-token", "")
            run.assert_not_called()


class BenchmarkTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.modules = [
            load("skill_smoke_" + str(i), SKILLS / name / "scripts/smoke_inference.py")
            for i, name in enumerate(("inferops-deploy", "inferops-gateway", "inferops-switchdeploy"))
        ]

    def run_server(self, response, test):
        seen = []

        class Handler(BaseHTTPRequestHandler):
            def do_POST(self):
                seen.append((self.path, self.headers.get("Authorization"),
                             json.loads(self.rfile.read(int(self.headers["Content-Length"])))))
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(json.dumps(response).encode())

            def log_message(self, *_args):
                pass

        with HTTPServer(("127.0.0.1", 0), Handler) as server:
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            try:
                test(f"http://127.0.0.1:{server.server_port}", seen)
            finally:
                server.shutdown()
                thread.join()

    def test_real_http_call_routes_and_auth_without_reporting_text(self):
        reply = {"choices": [{"message": {"content": "private generated response"}}],
                 "usage": {"completion_tokens": 5}}

        def check(origin, seen):
            for module in self.modules:
                report = module.benchmark(origin, "chat", 1, 5, "fixture-secret")
                self.assertEqual(report["completion_tokens"], 5)
                self.assertNotIn("private", json.dumps(report))
                self.assertNotIn("fixture-secret", json.dumps(report))
            self.assertTrue(all(p[0] == "/models/chat/v1/chat/completions" for p in seen))
            self.assertTrue(all(p[1] == "Bearer fixture-secret" for p in seen))
            self.assertTrue(all(p[2]["max_tokens"] == 16 for p in seen))
        self.run_server(reply, check)

    def test_missing_usage_does_not_invent_token_speed(self):
        reply = {"choices": [{"message": {"content": "hello"}}]}

        def check(origin, _seen):
            report = self.modules[0].benchmark(origin, "chat", 1, 5)
            self.assertIsNone(report["completion_tokens"])
            self.assertIsNone(report["sequential_completion_tokens_per_second"])
        self.run_server(reply, check)

    def test_empty_choices_rejected(self):
        def check(origin, _seen):
            with self.assertRaises(ValueError):
                self.modules[0].benchmark(origin, "chat", 1, 5)
        self.run_server({"choices": []}, check)

    def test_credentials_in_url_rejected(self):
        for module in self.modules:
            for url in ("https://key@example.com", "https://example.com?token=x", "file:///tmp/model"):
                with self.subTest(url=url), self.assertRaises(ValueError):
                    module.endpoint(url, "chat")

    def test_unbounded_requests_rejected(self):
        for count in (0, 6):
            with self.assertRaises(ValueError):
                self.modules[0].benchmark("http://127.0.0.1:1", "chat", count)

    def test_http_error_body_never_printed_by_cli(self):
        module = self.modules[0]
        failure = urllib.error.HTTPError(
            "http://localhost", 401, "private", {}, io.BytesIO(b"private-token"))
        output = io.StringIO()
        with patch.object(sys, "argv", ["smoke", "--gateway-url", "http://localhost", "--model", "chat"]):
            with patch.object(module, "benchmark", side_effect=failure), redirect_stdout(output):
                self.assertEqual(module.main(), 1)
        self.assertNotIn("private", output.getvalue())

    def test_redirect_is_not_followed_with_gateway_credentials(self):
        received = []

        class Redirect(BaseHTTPRequestHandler):
            def do_POST(self):
                received.append("initial")
                self.rfile.read(int(self.headers["Content-Length"]))
                self.send_response(302)
                self.send_header("Location", "/untrusted-destination")
                self.end_headers()

            def do_GET(self):
                received.append("redirected")
                self.send_response(200)
                self.end_headers()

            def log_message(self, *_args):
                pass

        with HTTPServer(("127.0.0.1", 0), Redirect) as server:
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            try:
                for module in self.modules:
                    with self.assertRaises(urllib.error.HTTPError):
                        module.benchmark(f"http://127.0.0.1:{server.server_port}", "chat", 1, 5, "fixture-secret")
            finally:
                server.shutdown()
                thread.join()
        self.assertEqual(received, ["initial"] * len(self.modules))


class BundleIsolationTests(unittest.TestCase):
    def test_offline_model_planner_runs_without_repo_or_other_skills(self):
        with tempfile.TemporaryDirectory() as directory:
            skill = Path(directory) / "inferops-deploy"
            shutil.copytree(SKILLS / "inferops-deploy", skill)
            model = Path(directory) / "model.json"
            model.write_text(json.dumps(metadata()))
            result = subprocess.run([
                sys.executable, str(skill / "scripts/model_plan.py"), "--metadata", str(model),
                "--runtime", "vllm", "--free-disk-gib", "40", "--free-ram-gib", "16",
                "--free-vram-gib", "16", "--kv-gib", "1",
            ], cwd=directory, capture_output=True, text=True, check=False)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual(json.loads(result.stdout)["assessment"], "candidate_requires_inference_test")

    def test_all_skill_links_remain_inside_individual_bundle(self):
        for entry in SKILLS.glob("*/SKILL.md"):
            with self.subTest(skill=entry.parent.name):
                self.assertEqual(validator.check_bundle(entry.parent), [])

    def test_validator_rejects_a_sibling_dependency(self):
        with tempfile.TemporaryDirectory() as directory:
            skill = Path(directory) / "inferops-gateway"
            shutil.copytree(SKILLS / "inferops-gateway", skill)
            with (skill / "SKILL.md").open("a") as out:
                out.write("\nRead [deployment](../inferops-deploy/SKILL.md).\n")
            self.assertTrue(any("outside installed bundle" in e for e in validator.check_bundle(skill)))


if __name__ == "__main__":
    unittest.main()
