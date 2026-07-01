"""Tests for Helm-backed installation."""

from __future__ import annotations

from pathlib import Path
import subprocess
import tempfile
import unittest

from inferops_cli.errors import CLIError
from inferops_cli.helm import HelmInstaller
from inferops_cli.kube import ClusterTarget, InstallRequest


class HelmInstallerTest(unittest.TestCase):
    def test_homelab_install_builds_repeatable_upgrade_commands(self) -> None:
        commands: list[list[str]] = []

        def run(command):
            commands.append(list(command))
            return subprocess.CompletedProcess(
                command, 0, stdout="Release upgraded\n", stderr=""
            )

        with tempfile.TemporaryDirectory() as directory:
            charts_dir = self._make_charts(Path(directory), include_homelab_values=True)
            response = HelmInstaller(runner=run).install(
                InstallRequest(
                    cluster=ClusterTarget(
                        namespace="inferops-system",
                        context="homelab",
                        kubeconfig="/tmp/kubeconfig",
                    ),
                    profile="homelab",
                    cache_path="/mnt/nvme/models",
                    tailscale_hostname="inferops",
                    charts_dir=str(charts_dir),
                )
            )

        self.assertEqual(len(commands), 2)
        for command in commands:
            self.assertEqual(command[:3], ["helm", "upgrade", "--install"])
            self.assertIn("--atomic", command)
            self.assertIn("--wait", command)
            self.assertIn("--create-namespace", command)
            self.assertIn("--kubeconfig", command)
            self.assertIn("--kube-context", command)
            self.assertIn("--values", command)

        operator_command, gateway_command = commands
        self.assertIn("cache.root=/mnt/nvme/models", operator_command)
        self.assertIn("profile=homelab", operator_command)
        self.assertIn("tailscale.enabled=true", gateway_command)
        self.assertIn("tailscale.hostname=inferops", gateway_command)
        self.assertEqual(response["install"]["cachePath"], "/mnt/nvme/models")
        self.assertEqual(response["install"]["tailscaleHostname"], "inferops")
        self.assertIn("modelruntime/llama-cpp", response["install"]["resources"])

    def test_default_profile_uses_default_cache_root_without_tailscale(self) -> None:
        commands: list[list[str]] = []

        def run(command):
            commands.append(list(command))
            return subprocess.CompletedProcess(command, 0, stdout="", stderr="")

        with tempfile.TemporaryDirectory() as directory:
            charts_dir = self._make_charts(Path(directory))
            response = HelmInstaller(runner=run).install(
                InstallRequest(
                    cluster=ClusterTarget(namespace="inferops-system"),
                    profile="default",
                    charts_dir=str(charts_dir),
                )
            )

        self.assertEqual(response["install"]["cachePath"], "/var/lib/inferops/models")
        self.assertIn("profile=default", commands[0])
        self.assertNotIn("tailscale.enabled=true", commands[1])

    def test_rejects_unsafe_cache_roots_before_running_helm(self) -> None:
        for path in (
            "/",
            "relative/models",
            "/var/lib/../models",
            "/var/lib/models/",
            "/var/lib/models\ninjected",
        ):
            with self.subTest(path=path), self.assertRaises(CLIError):
                HelmInstaller(
                    runner=lambda command: self.fail("Helm should not run")
                ).install(
                    InstallRequest(
                        cluster=ClusterTarget(namespace="inferops-system"),
                        profile="homelab",
                        cache_path=path,
                        charts_dir="/not-used",
                    )
                )

    def test_explicit_charts_directory_is_authoritative(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaisesRegex(CLIError, "--charts-dir does not contain"):
                HelmInstaller(
                    runner=lambda command: self.fail("Helm should not run")
                ).install(
                    InstallRequest(
                        cluster=ClusterTarget(namespace="inferops-system"),
                        profile="default",
                        charts_dir=directory,
                    )
                )

    def test_rejects_invalid_tailscale_hostname_before_running_helm(self) -> None:
        for hostname in ("Not_A_Host", "1inferops", "inferops1", "inferops.example"):
            with (
                self.subTest(hostname=hostname),
                self.assertRaisesRegex(CLIError, "start and end"),
            ):
                HelmInstaller(
                    runner=lambda command: self.fail("Helm should not run")
                ).install(
                    InstallRequest(
                        cluster=ClusterTarget(namespace="inferops-system"),
                        profile="homelab",
                        tailscale_hostname=hostname,
                        charts_dir="/not-used",
                    )
                )

    def test_reports_helm_stderr_without_exposing_command_arguments(self) -> None:
        def fail(command):
            raise subprocess.CalledProcessError(
                1, command, stderr="cluster unreachable"
            )

        with tempfile.TemporaryDirectory() as directory:
            charts_dir = self._make_charts(Path(directory))
            with self.assertRaisesRegex(CLIError, "cluster unreachable"):
                HelmInstaller(runner=fail).install(
                    InstallRequest(
                        cluster=ClusterTarget(namespace="inferops-system"),
                        profile="default",
                        charts_dir=str(charts_dir),
                    )
                )

    @staticmethod
    def _make_charts(root: Path, include_homelab_values: bool = False) -> Path:
        for chart in ("inferops-operator", "inferops-gateway"):
            chart_dir = root / chart
            chart_dir.mkdir()
            (chart_dir / "Chart.yaml").write_text(
                "apiVersion: v2\nname: test\nversion: 0.0.0\n"
            )
            if include_homelab_values:
                (chart_dir / "values-homelab.yaml").write_text("{}\n")
        return root
