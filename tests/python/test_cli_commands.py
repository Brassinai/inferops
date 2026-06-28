"""Tests for the placeholder Kubernetes CLI workflows."""

from __future__ import annotations

import argparse
from contextlib import redirect_stderr, redirect_stdout
import io
import json
from pathlib import Path
import tempfile
import textwrap
import unittest

from inferops_cli import activate, cache, deactivate, delete, deploy, gpu, init, install, logs, main, status
from inferops_cli.output import CommandResult, emit_result
from tests.python.fake_kube_client import FakeKubernetesClient


def make_args(**overrides) -> argparse.Namespace:
    """Build a namespace with common cluster arguments."""
    defaults = {
        "namespace": "team-a",
        "context": "kind-dev",
        "kubeconfig": "/tmp/kubeconfig",
        "output": "json",
        "activate": False,
        "when_full": None,
        "tail": 20,
        "force": False,
        "profile": "default",
        "cache_path": None,
    }
    defaults.update(overrides)
    return argparse.Namespace(**defaults)


class CLICommandParserTest(unittest.TestCase):
    def test_main_help_lists_mvp_302_commands(self) -> None:
        parser = main.build_parser()
        help_text = parser.format_help()

        for command in ("activate", "cache", "deactivate", "deploy", "gpu", "install", "logs", "status"):
            self.assertIn(command, help_text)

    def test_deploy_help_includes_shared_cluster_options(self) -> None:
        help_text = self._parse_help(["deploy", "--help"])

        for option in ("--namespace", "--context", "--kubeconfig", "--output"):
            self.assertIn(option, help_text)

    def test_group_commands_have_help_text(self) -> None:
        gpu_help = self._parse_help(["gpu", "list", "--help"])
        cache_help = self._parse_help(["cache", "delete", "--help"])

        self.assertIn("List placeholder GPU inventory", gpu_help)
        self.assertIn("Delete one placeholder cache entry", cache_help)

    def test_main_without_command_returns_usage_exit_code(self) -> None:
        stdout = io.StringIO()
        stderr = io.StringIO()
        with redirect_stdout(stdout), redirect_stderr(stderr):
            exit_code = main.main([])

        self.assertEqual(exit_code, 2)
        self.assertEqual(stdout.getvalue(), "")
        self.assertIn("usage: inferops", stderr.getvalue())

    def test_parse_errors_return_usage_exit_code_instead_of_system_exit(self) -> None:
        stdout = io.StringIO()
        stderr = io.StringIO()
        with redirect_stdout(stdout), redirect_stderr(stderr):
            exit_code = main.main(["status"])

        self.assertEqual(exit_code, 2)
        self.assertEqual(stdout.getvalue(), "")
        self.assertIn("inferops status: error:", stderr.getvalue())

    def _parse_help(self, argv: list[str]) -> str:
        parser = main.build_parser()
        stdout = io.StringIO()
        with self.assertRaises(SystemExit) as ctx, redirect_stdout(stdout):
            parser.parse_args(argv)
        self.assertEqual(ctx.exception.code, 0)
        return stdout.getvalue()


class CLICommandHandlerTest(unittest.TestCase):
    def test_command_lifecycle_uses_fake_client(self) -> None:
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

            deploy_stdout, _, exit_code = self._run(
                deploy.run,
                make_args(app=str(app_path), activate=False),
                fake_client,
            )
            status_stdout, _, _ = self._run(
                status.run,
                make_args(name="qwen-chat"),
                fake_client,
            )
            activate_stdout, _, _ = self._run(
                activate.run,
                make_args(name="qwen-chat"),
                fake_client,
            )
            logs_stdout, _, _ = self._run(
                logs.run,
                make_args(name="qwen-chat", tail=5),
                fake_client,
            )
            deactivate_stdout, _, _ = self._run(
                deactivate.run,
                make_args(name="qwen-chat"),
                fake_client,
            )
            cache_stdout, _, _ = self._run(
                cache.run_list,
                make_args(),
                fake_client,
            )
            cache_delete_stdout, _, _ = self._run(
                cache.run_delete,
                make_args(name="qwen-chat", force=True),
                fake_client,
            )
            gpu_stdout, _, _ = self._run(
                gpu.run_list,
                make_args(),
                fake_client,
            )
            install_stdout, _, _ = self._run(
                install.run,
                make_args(profile="homelab", cache_path="/var/lib/inferops/models"),
                fake_client,
            )
            delete_stdout, _, _ = self._run(
                delete.run,
                make_args(name="qwen-chat"),
                fake_client,
            )
            init_stdout, _, _ = self._run(
                init.run,
                make_args(output="json"),
                fake_client,
            )

        self.assertEqual(exit_code, 0)
        self.assertEqual(json.loads(deploy_stdout)["deployments"][0]["phase"], "Inactive")
        self.assertEqual(json.loads(status_stdout)["deployment"]["name"], "qwen-chat")
        self.assertEqual(json.loads(activate_stdout)["deployment"]["phase"], "Active")
        self.assertIn("placeholder log stream", json.loads(logs_stdout)["lines"][0])
        self.assertEqual(json.loads(deactivate_stdout)["deployment"]["phase"], "Inactive")
        self.assertEqual(json.loads(cache_stdout)["caches"][0]["name"], "qwen-chat")
        self.assertTrue(json.loads(cache_delete_stdout)["deleted"])
        self.assertEqual(json.loads(gpu_stdout)["gpus"], [])
        self.assertEqual(json.loads(install_stdout)["install"]["profile"], "homelab")
        self.assertTrue(json.loads(delete_stdout)["deleted"])
        self.assertEqual(json.loads(init_stdout)["mode"], "placeholder")

    def test_runtime_command_fails_without_real_kubernetes_client(self) -> None:
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

            stdout = io.StringIO()
            stderr = io.StringIO()
            with redirect_stdout(stdout), redirect_stderr(stderr):
                exit_code = deploy.run(make_args(app=str(app_path)))

        self.assertEqual(exit_code, 1)
        self.assertEqual(stdout.getvalue(), "")
        self.assertIn("real Kubernetes client not implemented yet", stderr.getvalue())

    def test_not_found_errors_use_stable_exit_code(self) -> None:
        fake_client = FakeKubernetesClient()

        stdout = io.StringIO()
        stderr = io.StringIO()
        with redirect_stdout(stdout), redirect_stderr(stderr):
            exit_code = status.run(make_args(name="missing-model"), fake_client)

        self.assertEqual(exit_code, 3)
        self.assertEqual(stdout.getvalue(), "")
        self.assertIn("deployment not found: missing-model", stderr.getvalue())

    def test_fake_client_is_namespace_safe(self) -> None:
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
            self._run(
                deploy.run,
                make_args(app=str(app_path), namespace="team-a"),
                fake_client,
            )
            self._run(
                deploy.run,
                make_args(app=str(app_path), namespace="team-b"),
                fake_client,
            )
            team_a_cache_stdout, _, _ = self._run(cache.run_list, make_args(namespace="team-a"), fake_client)
            team_b_cache_stdout, _, _ = self._run(cache.run_list, make_args(namespace="team-b"), fake_client)
            self._run(
                cache.run_delete,
                make_args(namespace="team-a", name="qwen-chat", force=True),
                fake_client,
            )
            remaining_team_b_cache_stdout, _, _ = self._run(
                cache.run_list,
                make_args(namespace="team-b"),
                fake_client,
            )

        self.assertEqual(json.loads(team_a_cache_stdout)["caches"][0]["namespace"], "team-a")
        self.assertEqual(json.loads(team_b_cache_stdout)["caches"][0]["namespace"], "team-b")
        self.assertEqual(json.loads(remaining_team_b_cache_stdout)["caches"][0]["namespace"], "team-b")

    def test_output_redacts_sensitive_fields(self) -> None:
        stdout = io.StringIO()
        with redirect_stdout(stdout):
            emit_result(
                "json",
                CommandResult(
                    summary="ignored",
                    payload={
                        "cluster": {"kubeconfigContents": "users:\n- token: abc123"},
                        "token": "abc123",
                        "secretData": {"password": "shh"},
                        "secret": {
                            "kind": "Secret",
                            "data": {"password": "c2ho"},
                            "stringData": {"token": "abc123"},
                        },
                    },
                ),
            )

        rendered = stdout.getvalue()
        self.assertNotIn("abc123", rendered)
        self.assertNotIn("password", rendered)
        self.assertNotIn("c2ho", rendered)
        self.assertIn("***REDACTED***", rendered)

    def _run(self, func, args: argparse.Namespace, fake_client: FakeKubernetesClient) -> tuple[str, str, int]:
        stdout = io.StringIO()
        stderr = io.StringIO()
        with redirect_stdout(stdout), redirect_stderr(stderr):
            exit_code = func(args, fake_client)
        return stdout.getvalue(), stderr.getvalue(), exit_code


if __name__ == "__main__":
    unittest.main()
