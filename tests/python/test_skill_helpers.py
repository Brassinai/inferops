from __future__ import annotations

from contextlib import redirect_stdout
import base64
import importlib.util
import io
import json
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
import os
import re
import shutil
import struct
import subprocess
import sys
import tempfile
import threading
import unittest
from unittest.mock import patch
import urllib.error
import zlib

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


def test_png(color=(255, 0, 0), width=16, height=16):
    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data))
    pixels = (b"\x00" + bytes(color) * width) * height
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(pixels)) + chunk(b"IEND", b""))


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

    def test_macos_memory_uses_vm_pages_and_real_page_size(self):
        stats = ('Mach Virtual Memory Statistics: (page size of 16384 bytes)\n'
                 'Pages free: 10.\nPages inactive: 20.\nPages speculative: 3.\nPages wired down: 99.\n')
        with patch.object(inspector.platform, "system", return_value="Darwin"), patch.object(
                inspector.subprocess, "check_output", side_effect=[str(16 * GIB), stats]):
            report = inspector.read_memory()
        self.assertEqual(report["total_bytes"], 16 * GIB)
        self.assertEqual(report["available_bytes"], 33 * 16384)

    def test_macos_missing_measurements_are_unknown(self):
        with patch.object(inspector.platform, "system", return_value="Darwin"), patch.object(
                inspector.subprocess, "check_output", side_effect=[str(16 * GIB), "incomplete"]):
            self.assertIsNone(inspector.read_memory()["available_bytes"])

    def test_memory_budget_keeps_platform_headroom_separate_from_disk(self):
        plan = inspector.memory_budget(10 * GIB, 6 * GIB)
        self.assertEqual(plan["reserve_bytes"], 2 * GIB)
        self.assertEqual(plan["candidate_model_budget_bytes"], 4 * GIB)
        self.assertEqual(inspector.memory_budget(2 * GIB, GIB)["candidate_model_budget_bytes"], 0)
        self.assertIsNone(inspector.memory_budget(None, None))
        with self.assertRaises(ValueError):
            inspector.memory_budget(GIB, 2 * GIB)


class ModelPlanTests(unittest.TestCase):
    def test_selected_vision_pair_counts_full_snapshot_disk_but_only_selected_weights_for_ram(self):
        value = metadata([
            {"rfilename": "model.gguf", "size": GIB // 2},
            {"rfilename": "mmproj.gguf", "size": GIB // 8},
            {"rfilename": "other-quant.gguf", "size": 4 * GIB},
        ])
        report = planner.plan(value, "llama-cpp", 20, 2, None, 0.25,
                              model_file="model.gguf", mmproj_file="mmproj.gguf",
                              vision_workspace_gib=0.25, runtime_overhead_gib=0.25)
        self.assertEqual(report["weight_bytes"], 5 * GIB // 8)
        self.assertEqual(report["snapshot_bytes"], 37 * GIB // 8)
        self.assertEqual(report["estimated_required_gib"]["disk_with_workspace"], 10)
        self.assertEqual(report["estimated_required_gib"]["ram_with_headroom"], 2)
        self.assertEqual(report["assessment"], "candidate_requires_inference_test")
        self.assertTrue(report["requires_runtime_configuration"])
        self.assertEqual(report["weight_files"], ["model.gguf", "mmproj.gguf"])
        blocked = planner.plan(value, "llama-cpp", 5, 2, None, 0.25,
                               model_file="model.gguf", mmproj_file="mmproj.gguf",
                               vision_workspace_gib=0.25, runtime_overhead_gib=0.25)
        self.assertEqual(blocked["assessment"], "insufficient")

    def test_selected_vision_pair_requires_workspace_estimate(self):
        value = metadata([{"rfilename": name, "size": GIB} for name in ["model.gguf", "mmproj.gguf"]])
        report = planner.plan(value, "llama-cpp", 20, 16, None, 1,
                              model_file="model.gguf", mmproj_file="mmproj.gguf")
        self.assertEqual(report["assessment"], "unknown")
        self.assertTrue(any("vision encoder" in b for b in report["blockers"]))

    def test_explicit_gguf_selection_supports_multiple_quantizations_without_inventing_vision(self):
        value = metadata([{"rfilename": name, "size": GIB} for name in ["q4.gguf", "q8.gguf"]])
        report = planner.plan(value, "llama-cpp", 20, 16, None, 1, model_file="q4.gguf")
        self.assertEqual(report["assessment"], "candidate_requires_inference_test")
        self.assertEqual(report["weight_bytes"], GIB)
        self.assertIsNone(report["mmproj_file"])

    def test_invalid_or_unavailable_vision_artifacts_do_not_pass(self):
        value = metadata([{"rfilename": name, "size": GIB} for name in ["model.gguf", "mmproj.gguf"]])
        cases = [dict(mmproj_file="mmproj.gguf"), dict(model_file="missing.gguf"),
                 dict(model_file="model.gguf", mmproj_file="model.gguf"), dict(model_file="../model.gguf"),
                 dict(model_file="subdir/model.gguf"), dict(model_file="model.gguf", mmproj_file="projector.bin")]
        for kwargs in cases:
            with self.subTest(kwargs=kwargs), self.assertRaises(ValueError):
                planner.inventory(value, "llama-cpp", **kwargs)
        with self.assertRaises(ValueError):
            planner.inventory(value, "vllm", model_file="model.gguf")

    def test_vision_workspace_and_runtime_overhead_must_be_valid(self):
        for kwargs in [dict(vision_workspace_gib=-1), dict(vision_workspace_gib=float("nan")),
                       dict(runtime_overhead_gib=0), dict(runtime_overhead_gib=float("inf"))]:
            with self.subTest(kwargs=kwargs), self.assertRaises(ValueError):
                planner.plan(metadata(), "llama-cpp", 20, 16, None, 1, **kwargs)

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
                self.wfile.write(json.dumps(response(seen[-1]) if callable(response) else response).encode())

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

    def test_image_requests_check_contrasting_visual_answers_without_reporting_payloads(self):
        samples = {test_png(): "red", test_png((0, 0, 255)): "blue"}
        def respond(request):
            content = request[2]["messages"][0]["content"]
            self.assertEqual([part["type"] for part in content], ["text", "image_url"])
            data_url = content[1]["image_url"]["url"]
            self.assertTrue(data_url.startswith("data:image/png;base64,"))
            color = samples[base64.b64decode(data_url.split(",", 1)[1])]
            self.assertNotIn(color, content[0]["text"].lower())
            return {"choices": [{"message": {"content": color}}], "usage": {"completion_tokens": 1}}
        def check(origin, _seen):
            with tempfile.TemporaryDirectory() as directory:
                for module in self.modules:
                    for data, color in samples.items():
                        image = Path(directory) / "sample.png"; image.write_bytes(data)
                        report = module.benchmark(origin, "chat", 1, 5, "fixture-secret",
                                                  image_path=image, expected_text=color)
                        self.assertTrue(report["visual_answer_checked"])
                        self.assertEqual(report["input_type"], "image")
                        self.assertNotIn("base64", json.dumps(report))
                        self.assertNotIn("fixture-secret", json.dumps(report))
                        self.assertNotIn(color, json.dumps(report))
        self.run_server(respond, check)

    def test_generic_text_reply_does_not_pass_an_expected_visual_answer(self):
        def check(origin, _seen):
            with tempfile.TemporaryDirectory() as directory:
                image = Path(directory) / "sample.png"; image.write_bytes(test_png())
                with self.assertRaisesRegex(ValueError, "expected visual answer"):
                    self.modules[0].benchmark(origin, "chat", 1, 5, image_path=image, expected_text="red")
        self.run_server({"choices": [{"message": {"content": "Hello there!"}}]}, check)

    def test_image_byte_and_pixel_limits_are_checked_before_network_requests(self):
        module = self.modules[0]
        oversized_header = b"\x89PNG\r\n\x1a\n" + struct.pack(">I", 13) + b"IHDR" + struct.pack(">II", 10000, 10000)
        with tempfile.TemporaryDirectory() as directory:
            image = Path(directory) / "sample.png"
            for data in [b"not a png", b"x" * (2 * 1024**2 + 1), oversized_header]:
                image.write_bytes(data)
                with self.subTest(size=len(data)), patch.object(module.urllib.request, "build_opener") as opener:
                    with self.assertRaises(ValueError):
                        module.benchmark("http://127.0.0.1:1", "chat", image_path=image)
                    opener.assert_not_called()
        with self.assertRaises(ValueError):
            module.benchmark("http://127.0.0.1:1", "chat", expected_text="red")

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


class UsageExampleTests(unittest.TestCase):
    def test_documented_calls_preserve_auth_but_never_forward_it_on_redirect(self):
        examples = []
        for relative in ("inferops-model-usage/SKILL.md", "inferops-deploy/references/runbook.md",
                         "inferops-switchdeploy/references/deployment.md"):
            text = (SKILLS / relative).read_text()
            blocks = re.findall(r"~~~python\n(.*?)~~~|python3 - <<'PY'\n(.*?)\nPY", text, re.DOTALL)
            example = next(left or right for left, right in blocks if "INFEROPS_GATEWAY_TOKEN" in (left or right))
            examples.append((relative, example))

        received = []
        redirected = []
        reply = json.dumps({"choices": [{"message": {"content": "fixture answer"}}]}).encode()

        class Destination(BaseHTTPRequestHandler):
            def do_GET(self):
                redirected.append(self.headers.get("Authorization"))
                self.send_response(200)
                self.end_headers()
                self.wfile.write(reply)

            def log_message(self, *_args):
                pass

        class Gateway(BaseHTTPRequestHandler):
            def do_POST(self):
                self.rfile.read(int(self.headers["Content-Length"]))
                received.append(self.headers.get("Authorization"))
                self.send_response(302 if redirect else 200)
                if redirect:
                    self.send_header("Location", f"http://127.0.0.1:{destination.server_port}/untrusted")
                self.end_headers()
                if not redirect:
                    self.wfile.write(reply)

            def log_message(self, *_args):
                pass

        with HTTPServer(("127.0.0.1", 0), Destination) as destination, HTTPServer(("127.0.0.1", 0), Gateway) as gateway:
            threads = [threading.Thread(target=server.serve_forever, daemon=True) for server in (destination, gateway)]
            for thread in threads:
                thread.start()
            try:
                origin = f"http://127.0.0.1:{gateway.server_port}"
                for redirect in (False, True):
                    for relative, example in examples:
                        with self.subTest(example=relative, redirect=redirect):
                            source = example.replace("http://127.0.0.1:8080", origin)
                            output = io.StringIO()
                            with patch.dict(os.environ, {"MODEL": "chat", "GATEWAY_URL": origin,
                                                        "INFEROPS_GATEWAY_TOKEN": "fixture-secret"}, clear=True), redirect_stdout(output):
                                if redirect:
                                    with self.assertRaises(urllib.error.HTTPError):
                                        exec(compile(source, relative, "exec"), {})
                                else:
                                    exec(compile(source, relative, "exec"), {})
                                    self.assertEqual(output.getvalue().strip(), "fixture answer")
                            self.assertNotIn("fixture-secret", output.getvalue())
            finally:
                destination.shutdown()
                gateway.shutdown()
                for thread in threads:
                    thread.join()
        self.assertEqual(received, ["Bearer fixture-secret"] * len(examples) * 2)
        self.assertEqual(redirected, [])


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
