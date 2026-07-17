"""Tests for idempotent deploy behavior."""

from __future__ import annotations

import argparse
from contextlib import redirect_stderr, redirect_stdout
import io
import json
from pathlib import Path
import tempfile
import textwrap
import unittest

from inferops_cli import deploy
from inferops_cli.kube import ClusterTarget
from inferops_cli.state import load_state, state_path
from tests.python.fake_kube_client import FakeKubernetesClient


def make_args(**overrides) -> argparse.Namespace:
    """Build a namespace with common cluster arguments."""
    defaults = {
        "namespace": "default",
        "context": None,
        "kubeconfig": None,
        "output": "json",
        "activate": False,
        "when_full": None,
    }
    defaults.update(overrides)
    return argparse.Namespace(**defaults)


class DeployIdempotencyTest(unittest.TestCase):
    def test_repeated_deploy_is_no_op(self) -> None:
        source = textwrap.dedent(
            """
            import inferops

            app = inferops.App("support")

            @app.model(name="qwen-chat", model="Qwen/Qwen2.5-7B-Instruct")
            class QwenChat:
                pass
            """
        )

        with tempfile.TemporaryDirectory() as directory:
            app_path = Path(directory) / "app.py"
            app_path.write_text(source, encoding="utf-8")
            fake_client = FakeKubernetesClient()

            first_stdout, _, first_code = self._run(
                deploy.run,
                make_args(app=str(app_path)),
                fake_client,
            )
            second_stdout, _, second_code = self._run(
                deploy.run,
                make_args(app=str(app_path)),
                fake_client,
            )

        self.assertEqual(first_code, 0)
        self.assertEqual(second_code, 0)

        first = json.loads(first_stdout)
        second = json.loads(second_stdout)

        self.assertEqual(first["deployments"][0]["action"], "created")
        self.assertEqual(second["deployments"][0]["action"], "unchanged")

    def test_deploy_reapplies_when_local_state_is_stale(self) -> None:
        source = textwrap.dedent(
            """
            import inferops

            app = inferops.App("support")

            @app.model(name="qwen-chat", model="Qwen/Qwen2.5-7B-Instruct")
            class QwenChat:
                pass
            """
        )

        with tempfile.TemporaryDirectory() as directory:
            app_path = Path(directory) / "app.py"
            app_path.write_text(source, encoding="utf-8")
            fake_client = FakeKubernetesClient()

            first_stdout, _, first_code = self._run(
                deploy.run,
                make_args(app=str(app_path)),
                fake_client,
            )
            key = fake_client._resource_key(
                ClusterTarget(namespace="default"),
                "qwen-chat",
            )
            fake_client._deployments.pop(key)
            second_stdout, _, second_code = self._run(
                deploy.run,
                make_args(app=str(app_path)),
                fake_client,
            )

        self.assertEqual(first_code, 0)
        self.assertEqual(second_code, 0)
        self.assertEqual(json.loads(first_stdout)["deployments"][0]["action"], "created")
        self.assertEqual(json.loads(second_stdout)["deployments"][0]["action"], "created")

    def test_deploy_stores_spec_hash_not_secrets(self) -> None:
        source = textwrap.dedent(
            """
            import inferops

            app = inferops.App("support")

            @app.model(
                name="qwen-chat",
                model="Qwen/Qwen2.5-7B-Instruct",
                hugging_face_token_secret_name="hf-token",
            )
            class QwenChat:
                pass
            """
        )

        with tempfile.TemporaryDirectory() as directory:
            app_path = Path(directory) / "app.py"
            app_path.write_text(source, encoding="utf-8")
            fake_client = FakeKubernetesClient()

            self._run(deploy.run, make_args(app=str(app_path)), fake_client)

            state = load_state(directory)
            stored = state["deployments"]["default/qwen-chat"]

            self.assertIn("last_applied_hash", stored)
            self.assertEqual(stored["name"], "qwen-chat")
            self.assertNotIn("secrets", stored)
            self.assertNotIn("token", json.dumps(stored).lower())

    def test_deploy_activate_override_changes_hash(self) -> None:
        source = textwrap.dedent(
            """
            import inferops

            app = inferops.App("support")

            @app.model(name="qwen-chat", model="Qwen/Qwen2.5-7B-Instruct")
            class QwenChat:
                pass
            """
        )

        with tempfile.TemporaryDirectory() as directory:
            app_path = Path(directory) / "app.py"
            app_path.write_text(source, encoding="utf-8")
            fake_client = FakeKubernetesClient()

            inactive_stdout, _, _ = self._run(
                deploy.run,
                make_args(app=str(app_path), activate=False),
                fake_client,
            )
            active_stdout, _, _ = self._run(
                deploy.run,
                make_args(app=str(app_path), activate=True),
                fake_client,
            )
            repeat_active_stdout, _, _ = self._run(
                deploy.run,
                make_args(app=str(app_path), activate=True),
                fake_client,
            )

        inactive = json.loads(inactive_stdout)
        active = json.loads(active_stdout)
        repeat_active = json.loads(repeat_active_stdout)

        self.assertEqual(inactive["deployments"][0]["phase"], "Inactive")
        self.assertEqual(active["deployments"][0]["phase"], "Active")
        self.assertEqual(repeat_active["deployments"][0].get("action"), "unchanged")

    def test_deploy_when_full_override(self) -> None:
        source = textwrap.dedent(
            """
            import inferops

            app = inferops.App("support")

            @app.model(name="qwen-chat", model="Qwen/Qwen2.5-7B-Instruct")
            class QwenChat:
                pass
            """
        )

        with tempfile.TemporaryDirectory() as directory:
            app_path = Path(directory) / "app.py"
            app_path.write_text(source, encoding="utf-8")
            fake_client = FakeKubernetesClient()

            first_stdout, _, _ = self._run(
                deploy.run,
                make_args(app=str(app_path)),
                fake_client,
            )
            second_stdout, _, _ = self._run(
                deploy.run,
                make_args(app=str(app_path), activate=True, when_full="ReplaceOldest"),
                fake_client,
            )

        first = json.loads(first_stdout)
        second = json.loads(second_stdout)

        self.assertEqual(first["deployments"][0]["action"], "created")
        self.assertEqual(second["deployments"][0]["phase"], "Active")

    def test_state_file_created_next_to_app(self) -> None:
        source = textwrap.dedent(
            """
            import inferops

            app = inferops.App("support")

            @app.model(name="qwen-chat", model="Qwen/Qwen2.5-7B-Instruct")
            class QwenChat:
                pass
            """
        )

        with tempfile.TemporaryDirectory() as directory:
            app_path = Path(directory) / "app.py"
            app_path.write_text(source, encoding="utf-8")
            fake_client = FakeKubernetesClient()

            self._run(deploy.run, make_args(app=str(app_path)), fake_client)

            self.assertTrue(state_path(directory).exists())

    def test_default_deploy_never_activates(self) -> None:
        source = textwrap.dedent(
            """
            import inferops

            app = inferops.App("support")

            @app.model(name="qwen-chat", model="Qwen/Qwen2.5-7B-Instruct", activation="Inactive")
            class QwenChat:
                pass
            """
        )

        with tempfile.TemporaryDirectory() as directory:
            app_path = Path(directory) / "app.py"
            app_path.write_text(source, encoding="utf-8")
            fake_client = FakeKubernetesClient()

            stdout, _, _ = self._run(
                deploy.run,
                make_args(app=str(app_path)),
                fake_client,
            )

        result = json.loads(stdout)
        self.assertEqual(result["deployments"][0]["phase"], "Inactive")

    def _run(self, func, args: argparse.Namespace, fake_client: FakeKubernetesClient) -> tuple[str, str, int]:
        stdout = io.StringIO()
        stderr = io.StringIO()
        with redirect_stdout(stdout), redirect_stderr(stderr):
            exit_code = func(args, fake_client)
        return stdout.getvalue(), stderr.getvalue(), exit_code


if __name__ == "__main__":
    unittest.main()
