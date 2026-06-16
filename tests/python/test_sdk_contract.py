"""Contract tests for Python SDK manifest generation."""

from __future__ import annotations

import unittest

from inferops import App
from inferops.deploy import build_manifest, build_manifests


class SDKContractTest(unittest.TestCase):
    def test_default_runtime_is_nano_vllm(self) -> None:
        app = App("support")

        @app.model(name="qwen-chat", model="Qwen/Qwen2.5-7B-Instruct")
        class QwenChat:
            pass

        manifest = build_manifest(app)

        self.assertEqual(manifest["spec"]["runtime"]["ref"], "nano-vllm")
        self.assertEqual(manifest["spec"]["routing"]["path"], "/models/qwen-chat")
        self.assertEqual(manifest["spec"]["activation"]["desiredState"], "Inactive")
        self.assertEqual(manifest["spec"]["resources"]["gpu"]["count"], 1)
        self.assertEqual(manifest["spec"]["runtime"]["gpuMemoryUtilization"], 0.85)

    def test_registered_model_without_gpu_key_keeps_gpu_default(self) -> None:
        app = App("support")
        app.register({"name": "qwen-chat", "engine": "vllm", "model": "Qwen/Qwen2.5-7B-Instruct"})

        manifest = build_manifest(app)

        self.assertEqual(manifest["spec"]["resources"]["gpu"]["count"], 1)

    def test_cpu_only_deployment_omits_gpu_settings(self) -> None:
        app = App("support")

        @app.model(name="qwen-chat", engine="vllm", model="Qwen/Qwen2.5-7B-Instruct", gpu=None)
        class QwenChat:
            pass

        manifest = build_manifest(app)

        self.assertNotIn("gpu", manifest["spec"]["resources"])
        self.assertEqual(manifest["spec"]["resources"], {"cpu": "4", "memory": "16Gi"})
        self.assertNotIn("tensorParallelSize", manifest["spec"]["runtime"])
        self.assertNotIn("gpuMemoryUtilization", manifest["spec"]["runtime"])
        self.assertEqual(manifest["spec"]["activation"]["desiredState"], "Inactive")
        self.assertEqual(manifest["spec"]["scaling"], {"minReplicas": 0, "maxReplicas": 1})
        self.assertTrue(manifest["spec"]["routing"]["enabled"])
        self.assertTrue(manifest["spec"]["cache"]["enabled"])

    def test_gpu_request_is_explicit(self) -> None:
        app = App("support")

        @app.model(name="qwen-chat", model="Qwen/Qwen2.5-7B-Instruct", gpu="L4")
        class QwenChat:
            pass

        manifest = build_manifest(app)

        self.assertEqual(manifest["spec"]["resources"]["gpu"], {"count": 1, "vendor": "nvidia", "type": "L4"})
        self.assertEqual(manifest["spec"]["runtime"]["gpuMemoryUtilization"], 0.85)

    def test_registered_runtime_is_preserved(self) -> None:
        app = App("support")

        @app.model(name="qwen-chat", engine="vllm", model="Qwen/Qwen2.5-7B-Instruct", gpu=2)
        class QwenChat:
            pass

        manifest = build_manifest(app)

        self.assertEqual(manifest["spec"]["runtime"]["ref"], "vllm")
        self.assertEqual(manifest["spec"]["resources"]["gpu"]["count"], 2)
        self.assertEqual(manifest["spec"]["runtime"]["tensorParallelSize"], 2)

    def test_invalid_gpu_count_is_rejected(self) -> None:
        app = App("support")

        @app.model(name="qwen-chat", model="Qwen/Qwen2.5-7B-Instruct", gpu=0)
        class QwenChat:
            pass

        with self.assertRaisesRegex(ValueError, "at least 1"):
            build_manifest(app)

    def test_multiple_models_produce_multiple_deployments(self) -> None:
        app = App("support")

        @app.model(name="chat", model="repo/chat")
        class Chat:
            pass

        @app.model(name="code", engine="sglang", model="repo/code")
        class Code:
            pass

        manifests = build_manifests(app)

        self.assertEqual([manifest["metadata"]["name"] for manifest in manifests], ["chat", "code"])
        self.assertEqual([manifest["spec"]["runtime"]["ref"] for manifest in manifests], ["nano-vllm", "sglang"])

    def test_single_manifest_rejects_multiple_models(self) -> None:
        app = App("support")
        app.models.extend(
            [
                {"name": "chat", "engine": "nano-vllm", "model": "repo/chat"},
                {"name": "code", "engine": "sglang", "model": "repo/code"},
            ]
        )

        with self.assertRaisesRegex(ValueError, "exactly one model"):
            build_manifest(app)


if __name__ == "__main__":
    unittest.main()
