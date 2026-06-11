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

    def test_registered_runtime_is_preserved(self) -> None:
        app = App("support")

        @app.model(name="qwen-chat", engine="vllm", model="Qwen/Qwen2.5-7B-Instruct", gpu=2)
        class QwenChat:
            pass

        manifest = build_manifest(app)

        self.assertEqual(manifest["spec"]["runtime"]["ref"], "vllm")
        self.assertEqual(manifest["spec"]["resources"]["gpu"]["count"], 2)

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
