"""Tests for the placeholder Kubernetes CLI workflows."""

from __future__ import annotations

import argparse
from contextlib import redirect_stderr, redirect_stdout
import io
import json
from pathlib import Path
import subprocess
import tempfile
import textwrap
import unittest

from inferops_cli import (
    activate,
    cache,
    deactivate,
    delete,
    deploy,
    doctor,
    endpoints,
    gateway,
    gpu,
    init,
    install,
    logs,
    main,
    models,
    status,
)
from inferops_cli.kube import ClusterTarget
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
        "compute_profile": "cpu",
        "cache_path": None,
        "tailscale_hostname": None,
        "exposure": None,
        "ingress_class": None,
        "ingress_hostname": None,
        "gateway_name": None,
        "gateway_namespace": None,
        "gateway_section_name": None,
        "gateway_hostname": None,
        "load_balancer_class": None,
        "gateway_auth_secret": None,
        "allow_unauthenticated_exposure": False,
        "charts_dir": None,
        "checks": None,
        "no_wait": False,
        "timeout": 300,
        "watch": False,
    }
    defaults.update(overrides)
    return argparse.Namespace(**defaults)


class CLICommandParserTest(unittest.TestCase):
    def test_main_help_lists_mvp_302_commands(self) -> None:
        parser = main.build_parser()
        help_text = parser.format_help()

        for command in (
            "activate",
            "cache",
            "deactivate",
            "deploy",
            "doctor",
            "endpoints",
            "gateway",
            "gpu",
            "install",
            "logs",
            "models",
            "status",
        ):
            self.assertIn(command, help_text)

    def test_deploy_help_includes_shared_cluster_options(self) -> None:
        help_text = self._parse_help(["deploy", "--help"])

        for option in ("--namespace", "--context", "--kubeconfig", "--output"):
            self.assertIn(option, help_text)

    def test_group_commands_have_help_text(self) -> None:
        gpu_help = self._parse_help(["gpu", "list", "--help"])
        gateway_help = self._parse_help(["gateway", "forward", "--help"])
        cache_help = self._parse_help(["cache", "delete", "--help"])
        doctor_help = self._parse_help(["doctor", "--help"])

        self.assertIn("List GPU capacity, occupancy, and availability", gpu_help)
        self.assertIn("Forward the InferOps gateway Service", gateway_help)
        self.assertIn("Delete one ModelCache", cache_help)
        self.assertIn("Check Kubernetes API, GPUs, cache, gateway", doctor_help)

    def test_install_help_documents_profile_configuration(self) -> None:
        install_help = self._parse_help(["install", "--help"])

        for option in (
            "--profile",
            "--cache-path",
            "--tailscale-hostname",
            "--exposure",
            "--ingress-class",
            "--gateway-name",
            "--load-balancer-class",
            "--gateway-auth-secret",
            "--allow-unauthenticated-exposure",
            "--charts-dir",
        ):
            self.assertIn(option, install_help)

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

    def test_doctor_rejects_unknown_check_name(self) -> None:
        stdout = io.StringIO()
        stderr = io.StringIO()
        with redirect_stdout(stdout), redirect_stderr(stderr):
            exit_code = main.main(["doctor", "--check", "not-a-check"])

        self.assertEqual(exit_code, 2)
        self.assertIn("invalid choice", stderr.getvalue())

    def _parse_help(self, argv: list[str]) -> str:
        parser = main.build_parser()
        stdout = io.StringIO()
        with self.assertRaises(SystemExit) as ctx, redirect_stdout(stdout):
            parser.parse_args(argv)
        self.assertEqual(ctx.exception.code, 0)
        return stdout.getvalue()


class CLICommandHandlerTest(unittest.TestCase):
    def test_gateway_forward_builds_kubectl_port_forward_command(self) -> None:
        commands: list[list[str]] = []

        def run(command):
            commands.append(list(command))
            return subprocess.CompletedProcess(command, 0)

        stdout = io.StringIO()
        args = make_args(
            namespace="inferops-system",
            context="orbstack",
            kubeconfig="/tmp/kubeconfig",
            service="inferops-gateway",
            address="127.0.0.1",
            local_port=8080,
            remote_port=80,
        )
        with redirect_stdout(stdout):
            exit_code = gateway.run_forward(args, runner=run)

        self.assertEqual(exit_code, 0)
        self.assertEqual(
            commands,
            [
                [
                    "kubectl",
                    "--kubeconfig",
                    "/tmp/kubeconfig",
                    "--context",
                    "orbstack",
                    "--namespace",
                    "inferops-system",
                    "port-forward",
                    "--address",
                    "127.0.0.1",
                    "svc/inferops-gateway",
                    "8080:80",
                ]
            ],
        )
        self.assertIn("http://127.0.0.1:8080", stdout.getvalue())

    def test_gateway_forward_rejects_invalid_service_name(self) -> None:
        stdout = io.StringIO()
        stderr = io.StringIO()
        args = make_args(
            service="../inferops-gateway",
            address="127.0.0.1",
            local_port=8080,
            remote_port=80,
        )
        with redirect_stdout(stdout), redirect_stderr(stderr):
            exit_code = gateway.run_forward(
                args,
                runner=lambda command: self.fail("kubectl should not run"),
            )

        self.assertEqual(exit_code, 1)
        self.assertEqual(stdout.getvalue(), "")
        self.assertIn("gateway service name is invalid", stderr.getvalue())

    def test_main_runs_full_lifecycle_replacement_and_failure_workflow(self) -> None:
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
            common = [
                "--namespace",
                "team-a",
                "--context",
                "kind-dev",
                "--output",
                "json",
            ]

            deployed, _, deploy_code = self._run_main(
                ["deploy", str(app_path), *common], fake_client
            )
            activated, _, activate_code = self._run_main(
                [
                    "activate",
                    "qwen-chat",
                    "--when-full",
                    "ReplaceOldest",
                    *common,
                ],
                fake_client,
            )
            status_output, _, status_code = self._run_main(
                ["status", "qwen-chat", *common], fake_client
            )
            deactivated, _, deactivate_code = self._run_main(
                ["deactivate", "qwen-chat", *common], fake_client
            )

            key = fake_client._resource_key(
                ClusterTarget(namespace="team-a", context="kind-dev"),
                "qwen-chat",
            )
            fake_client._activation_outcomes[key] = "rejected"
            rejected, rejected_error, rejected_code = self._run_main(
                ["activate", "qwen-chat", "--when-full", "Reject", *common],
                fake_client,
            )

        self.assertEqual(
            (deploy_code, activate_code, status_code, deactivate_code),
            (0, 0, 0, 0),
        )
        self.assertEqual(json.loads(deployed)["deployments"][0]["phase"], "Inactive")
        self.assertEqual(json.loads(activated)["outcome"], "active")
        self.assertEqual(
            json.loads(activated)["deployment"]["whenFull"], "ReplaceOldest"
        )
        self.assertEqual(
            [item["reason"] for item in json.loads(activated)["transitions"]],
            [
                "ReplacementSelected",
                "ReplacementDraining",
                "RuntimeStarting",
                "RuntimeReady",
            ],
        )
        status_deployment = json.loads(status_output)["deployment"]
        self.assertEqual(status_deployment["phase"], "Active")
        self.assertEqual(status_deployment["replacement"]["phase"], "Completed")
        self.assertEqual(
            status_deployment["replacement"]["target"]["name"], "previous-model"
        )
        self.assertEqual(json.loads(deactivated)["outcome"], "inactive")
        self.assertEqual(rejected_code, 1)
        self.assertEqual(json.loads(rejected)["outcome"], "rejected")
        self.assertEqual(rejected_error, "")

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
            models_stdout, _, _ = self._run(
                models.run,
                make_args(),
                fake_client,
            )
            endpoints_stdout, _, _ = self._run(
                endpoints.run,
                make_args(),
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
                make_args(
                    profile="homelab",
                    compute_profile="nvidia-gpu",
                    cache_path="/var/lib/inferops/models",
                ),
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
        self.assertEqual(
            json.loads(deploy_stdout)["deployments"][0]["phase"], "Inactive"
        )
        self.assertEqual(json.loads(status_stdout)["deployment"]["name"], "qwen-chat")
        self.assertEqual(json.loads(activate_stdout)["deployment"]["phase"], "Active")
        self.assertIn("runtime log stream", json.loads(logs_stdout)["lines"][0])
        self.assertEqual(
            json.loads(deactivate_stdout)["deployment"]["phase"], "Cached"
        )
        self.assertEqual(json.loads(models_stdout)["models"][0]["name"], "qwen-chat")
        self.assertEqual(
            json.loads(endpoints_stdout)["endpoints"][0]["endpoint"],
            "/models/qwen-chat/v1",
        )
        self.assertEqual(json.loads(cache_stdout)["caches"][0]["name"], "qwen-chat")
        self.assertTrue(json.loads(cache_delete_stdout)["deleted"])
        self.assertEqual(json.loads(gpu_stdout)["gpus"], [])
        self.assertEqual(json.loads(install_stdout)["install"]["profile"], "homelab")
        self.assertEqual(
            json.loads(install_stdout)["install"]["computeProfile"], "nvidia-gpu"
        )
        self.assertTrue(json.loads(delete_stdout)["deleted"])
        self.assertTrue(json.loads(delete_stdout)["cachePreserved"])
        self.assertEqual(json.loads(init_stdout)["mode"], "placeholder")

    def test_runtime_command_reports_invalid_kubeconfig(self) -> None:
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
        self.assertIn("failed to load kubeconfig", stderr.getvalue())

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
            team_a_cache_stdout, _, _ = self._run(
                cache.run_list, make_args(namespace="team-a"), fake_client
            )
            team_b_cache_stdout, _, _ = self._run(
                cache.run_list, make_args(namespace="team-b"), fake_client
            )
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

        self.assertEqual(
            json.loads(team_a_cache_stdout)["caches"][0]["namespace"], "team-a"
        )
        self.assertEqual(
            json.loads(team_b_cache_stdout)["caches"][0]["namespace"], "team-b"
        )
        self.assertEqual(
            json.loads(remaining_team_b_cache_stdout)["caches"][0]["namespace"],
            "team-b",
        )

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

    def test_doctor_returns_checks_and_exits_on_failure(self) -> None:
        fake_client = FakeKubernetesClient()
        fake_client._doctor_checks = [
            {"id": "kubernetes-api", "status": "PASS", "message": "ok"},
            {
                "id": "gateway",
                "status": "FAIL",
                "message": "not ready",
                "remediation": "install",
            },
        ]

        stdout = io.StringIO()
        stderr = io.StringIO()
        with redirect_stdout(stdout), redirect_stderr(stderr):
            exit_code = doctor.run(make_args(), fake_client)

        self.assertEqual(exit_code, 1)
        payload = json.loads(stdout.getvalue())
        self.assertEqual(len(payload["checks"]), 2)
        self.assertEqual(payload["checks"][0]["status"], "PASS")
        self.assertEqual(payload["checks"][1]["status"], "FAIL")

    def test_doctor_filters_by_check_name(self) -> None:
        fake_client = FakeKubernetesClient()
        fake_client._doctor_checks = [
            {"id": "kubernetes-api", "status": "PASS", "message": "ok"},
            {"id": "gateway", "status": "PASS", "message": "ok"},
        ]

        stdout = io.StringIO()
        stderr = io.StringIO()
        with redirect_stdout(stdout), redirect_stderr(stderr):
            exit_code = doctor.run(make_args(checks=["gateway"]), fake_client)

        self.assertEqual(exit_code, 0)
        payload = json.loads(stdout.getvalue())
        self.assertEqual(len(payload["checks"]), 1)
        self.assertEqual(payload["checks"][0]["id"], "gateway")

    def test_gpu_list_shows_inventory(self) -> None:
        fake_client = FakeKubernetesClient()
        fake_client._gpus = [
            {
                "node": "node-1",
                "resourceName": "nvidia.com/gpu",
                "vendor": "nvidia",
                "product": "A100",
                "capacity": 2,
                "allocatable": 2,
                "occupied": 1,
                "available": 1,
            }
        ]

        stdout = io.StringIO()
        stderr = io.StringIO()
        with redirect_stdout(stdout), redirect_stderr(stderr):
            exit_code = gpu.run_list(make_args(), fake_client)

        self.assertEqual(exit_code, 0)
        payload = json.loads(stdout.getvalue())
        self.assertEqual(payload["gpus"][0]["node"], "node-1")
        self.assertEqual(payload["gpus"][0]["available"], 1)

    def test_cache_delete_refuses_without_force(self) -> None:
        fake_client = FakeKubernetesClient()
        key = fake_client._resource_key(
            ClusterTarget(namespace="team-a", context="kind-dev"), "qwen-chat"
        )
        fake_client._caches[key] = {
            "name": "qwen-chat",
            "namespace": "team-a",
            "referencedBy": ["other-deploy"],
        }

        stdout = io.StringIO()
        stderr = io.StringIO()
        with redirect_stdout(stdout), redirect_stderr(stderr):
            exit_code = cache.run_delete(
                make_args(name="qwen-chat", force=False), fake_client
            )

        self.assertEqual(exit_code, 1)
        self.assertIn("referenced by deployments", stderr.getvalue())

    def test_cache_delete_forces_resource_deletion(self) -> None:
        fake_client = FakeKubernetesClient()
        key = fake_client._resource_key(
            ClusterTarget(namespace="team-a", context="kind-dev"), "qwen-chat"
        )
        fake_client._caches[key] = {
            "name": "qwen-chat",
            "namespace": "team-a",
            "referencedBy": ["other-deploy"],
        }

        stdout = io.StringIO()
        stderr = io.StringIO()
        with redirect_stdout(stdout), redirect_stderr(stderr):
            exit_code = cache.run_delete(
                make_args(name="qwen-chat", force=True), fake_client
            )

        self.assertEqual(exit_code, 0)
        payload = json.loads(stdout.getvalue())
        self.assertTrue(payload.get("annotated"))
        self.assertTrue(payload.get("deleted"))
        self.assertFalse(payload.get("nodeFilesModified"))

    def _run(
        self, func, args: argparse.Namespace, fake_client: FakeKubernetesClient
    ) -> tuple[str, str, int]:
        stdout = io.StringIO()
        stderr = io.StringIO()
        with redirect_stdout(stdout), redirect_stderr(stderr):
            exit_code = func(args, fake_client)
        return stdout.getvalue(), stderr.getvalue(), exit_code

    def _run_main(
        self, argv: list[str], fake_client: FakeKubernetesClient
    ) -> tuple[str, str, int]:
        stdout = io.StringIO()
        stderr = io.StringIO()
        with redirect_stdout(stdout), redirect_stderr(stderr):
            exit_code = main.main(argv, client=fake_client)
        return stdout.getvalue(), stderr.getvalue(), exit_code


if __name__ == "__main__":
    unittest.main()
