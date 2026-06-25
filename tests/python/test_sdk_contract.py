"""Contract tests for Python SDK manifest generation."""

from __future__ import annotations

from pathlib import Path
import unittest

import yaml

from inferops import App, web_endpoint
from inferops.deploy import build_manifest, build_manifests, render_yaml


ROOT = Path(__file__).resolve().parents[2]


def load_yaml_fixture(relative_path: str) -> dict:
    """Load one YAML fixture from the repository."""
    with (ROOT / relative_path).open(encoding="utf-8") as stream:
        return yaml.safe_load(stream)


class SDKContractTest(unittest.TestCase):
    def test_default_manifest_matches_inactive_contract_fixture(self) -> None:
        app = App("support")

        @app.model(name="qwen-inactive", model="Qwen/Qwen2.5-7B-Instruct")
        class QwenInactive:
            pass

        manifest = build_manifest(app)
        expected = load_yaml_fixture("deploy/manifests/examples/contracts/modeldeployment-inactive.yaml")

        self.assertEqual(manifest, {key: expected[key] for key in ("apiVersion", "kind", "metadata", "spec")})

    def test_active_manifest_matches_active_contract_fixture(self) -> None:
        app = App("support")

        @app.model(name="qwen-active", model="Qwen/Qwen2.5-7B-Instruct", activation="active")
        class QwenActive:
            pass

        manifest = build_manifest(app)
        expected = load_yaml_fixture("deploy/manifests/examples/contracts/modeldeployment-active.yaml")

        self.assertEqual(manifest, {key: expected[key] for key in ("apiVersion", "kind", "metadata", "spec")})

    def test_cpu_only_deployment_omits_gpu_runtime_fields(self) -> None:
        app = App("support")

        @app.model(name="cpu-qwen", engine="vllm", model="Qwen/Qwen2.5-0.5B-Instruct", gpu=None, max_model_len=2048)
        class CPUQwen:
            pass

        manifest = build_manifest(app)

        self.assertNotIn("gpu", manifest["spec"]["resources"])
        self.assertNotIn("tensorParallelSize", manifest["spec"]["runtime"])
        self.assertNotIn("gpuMemoryUtilization", manifest["spec"]["runtime"])
        self.assertEqual(manifest["spec"]["resources"], {"cpu": "8", "memory": "32Gi"})
        self.assertEqual(manifest["spec"]["runtime"], {"ref": "vllm", "maxModelLen": 2048})

    def test_gpu_count_and_type_are_rendered(self) -> None:
        app = App("support")

        @app.model(name="qwen-chat", model="Qwen/Qwen2.5-7B-Instruct", gpu=2, gpu_type="L4")
        class QwenChat:
            pass

        manifest = build_manifest(app)

        self.assertEqual(
            manifest["spec"]["resources"]["gpu"],
            {"count": 2, "vendor": "nvidia", "type": "L4"},
        )
        self.assertEqual(manifest["spec"]["runtime"]["tensorParallelSize"], 2)

    def test_registered_model_uses_same_defaults(self) -> None:
        app = App("support")
        app.register({"name": "qwen-inactive", "engine": "nano-vllm", "model": "Qwen/Qwen2.5-7B-Instruct"})

        manifest = build_manifest(app)

        self.assertEqual(manifest["spec"]["activation"]["desiredState"], "Inactive")
        self.assertEqual(manifest["spec"]["cache"]["size"], "100Gi")
        self.assertEqual(manifest["spec"]["resources"]["cpu"], "8")

    def test_multiple_models_are_sorted_for_deterministic_output(self) -> None:
        app = App("support")

        @app.model(name="code", engine="sglang", model="repo/code")
        class Code:
            pass

        @app.model(name="chat", model="repo/chat")
        class Chat:
            pass

        manifests = build_manifests(app)

        self.assertEqual([manifest["metadata"]["name"] for manifest in manifests], ["chat", "code"])
        self.assertEqual([manifest["spec"]["runtime"]["ref"] for manifest in manifests], ["nano-vllm", "sglang"])

    def test_render_yaml_is_deterministic(self) -> None:
        app = App("support")

        @app.model(name="code", engine="sglang", model="repo/code")
        class Code:
            pass

        @app.model(name="chat", model="repo/chat")
        class Chat:
            pass

        first = render_yaml(app)
        second = render_yaml(app)
        parsed = list(yaml.safe_load_all(first))

        self.assertEqual(first, second)
        self.assertEqual([document["metadata"]["name"] for document in parsed], ["chat", "code"])

    def test_invalid_gpu_count_is_rejected_early(self) -> None:
        app = App("support")

        with self.assertRaisesRegex(ValueError, "gpu count must be at least 1"):

            @app.model(name="qwen-chat", model="Qwen/Qwen2.5-7B-Instruct", gpu=0)
            class QwenChat:
                pass

    def test_invalid_activation_is_rejected_early(self) -> None:
        app = App("support")

        with self.assertRaisesRegex(ValueError, "activation must be 'inactive' or 'active'"):

            @app.model(name="qwen-chat", model="Qwen/Qwen2.5-7B-Instruct", activation="paused")
            class QwenChat:
                pass

    def test_invalid_when_full_is_rejected_early(self) -> None:
        app = App("support")

        with self.assertRaisesRegex(ValueError, "when_full must be one of"):

            @app.model(name="qwen-chat", model="Qwen/Qwen2.5-7B-Instruct", when_full="evict")
            class QwenChat:
                pass

    def test_invalid_scaling_bounds_are_rejected_early(self) -> None:
        app = App("support")

        with self.assertRaisesRegex(ValueError, "max_replicas must be greater than or equal to min_replicas"):

            @app.model(name="qwen-chat", model="Qwen/Qwen2.5-7B-Instruct", min_replicas=2, max_replicas=1)
            class QwenChat:
                pass

    def test_invalid_route_path_is_rejected_early(self) -> None:
        app = App("support")

        with self.assertRaisesRegex(ValueError, "route_path must start with '/'"):

            @app.model(name="qwen-chat", model="Qwen/Qwen2.5-7B-Instruct", route_path="models/qwen-chat")
            class QwenChat:
                pass

    def test_blank_cpu_is_rejected_instead_of_silently_defaulting(self) -> None:
        app = App("support")

        with self.assertRaisesRegex(ValueError, "cpu must be a non-empty Kubernetes resource quantity"):

            @app.model(name="qwen-chat", model="Qwen/Qwen2.5-7B-Instruct", cpu="")
            class QwenChat:
                pass

    def test_duplicate_endpoint_metadata_is_rejected_early(self) -> None:
        app = App("support")

        with self.assertRaisesRegex(ValueError, "duplicate endpoint declaration"):

            @app.model(name="qwen-chat", model="Qwen/Qwen2.5-7B-Instruct")
            class QwenChat:
                @web_endpoint(method="POST", path="/chat")
                def chat(self, request):
                    return request

                @web_endpoint(method="POST", path="/chat")
                def duplicate(self, request):
                    return request

    def test_build_manifest_rejects_multiple_models(self) -> None:
        app = App("support")
        app.register({"name": "chat", "engine": "nano-vllm", "model": "repo/chat"})
        app.register({"name": "code", "engine": "sglang", "model": "repo/code"})

        with self.assertRaisesRegex(ValueError, "exactly one model"):
            build_manifest(app)


if __name__ == "__main__":
    unittest.main()
