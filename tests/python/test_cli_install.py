"""Tests for Helm-backed installation."""

from __future__ import annotations

from dataclasses import replace
from pathlib import Path
import subprocess
import tempfile
import unittest

from inferops_cli.errors import CLIError
from inferops_cli.helm import HelmInstaller
from inferops_cli.kube import ClusterTarget, InstallRequest, UpgradeRequest


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

        self.assertEqual(len(commands), 3)
        crd_command, operator_command, gateway_command = commands
        self.assertEqual(crd_command[0], "kubectl")
        self.assertIn("--server-side", crd_command)
        self.assertIn("--field-manager=inferops-cli", crd_command)
        self.assertIn("--kubeconfig", crd_command)
        self.assertIn("--context", crd_command)
        self.assertEqual(crd_command[-2], "--filename")
        self.assertTrue(crd_command[-1].endswith("inferops-operator/crds"))

        for command in (operator_command, gateway_command):
            self.assertEqual(command[:3], ["helm", "upgrade", "--install"])
            self.assertIn("--atomic", command)
            self.assertIn("--wait", command)
            self.assertIn("--create-namespace", command)
            self.assertIn("--kubeconfig", command)
            self.assertIn("--kube-context", command)
            self.assertIn("--values", command)

        self.assertIn("cache.root=/mnt/nvme/models", operator_command)
        self.assertIn("profile=homelab", operator_command)
        self.assertIn("gpu.required=false", operator_command)
        self.assertIn("cache.requiredNodeResources=[]", operator_command)
        self.assertIn("tailscale.enabled=true", gateway_command)
        self.assertIn("tailscale.hostname=inferops", gateway_command)
        self.assertEqual(response["install"]["cachePath"], "/mnt/nvme/models")
        self.assertEqual(response["install"]["computeProfile"], "cpu")
        self.assertEqual(response["install"]["tailscaleHostname"], "inferops")
        self.assertIn("modelruntime/llama-cpp", response["install"]["resources"])

    def test_nvidia_gpu_compute_profile_requires_gpu_cache_nodes(self) -> None:
        commands: list[list[str]] = []

        def run(command):
            commands.append(list(command))
            return subprocess.CompletedProcess(command, 0, stdout="", stderr="")

        with tempfile.TemporaryDirectory() as directory:
            charts_dir = self._make_charts(Path(directory), include_homelab_values=True)
            response = HelmInstaller(runner=run).install(
                InstallRequest(
                    cluster=ClusterTarget(namespace="inferops-system"),
                    profile="homelab",
                    compute_profile="nvidia-gpu",
                    charts_dir=str(charts_dir),
                )
            )

        operator_command = commands[1]
        self.assertIn("gpu.required=true", operator_command)
        self.assertIn(
            'cache.requiredNodeResources=["nvidia.com/gpu"]', operator_command
        )
        self.assertEqual(response["install"]["computeProfile"], "nvidia-gpu")

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
        self.assertIn("profile=default", commands[1])
        self.assertIn("gpu.required=false", commands[1])
        self.assertIn("cache.requiredNodeResources=[]", commands[1])
        self.assertNotIn("tailscale.enabled=true", commands[2])
        self.assertEqual(response["install"]["crds"]["status"], "applied")
        self.assertEqual(response["install"]["exposure"], "cluster-ip")
        self.assertEqual(response["install"]["computeProfile"], "cpu")

    def test_builds_each_portable_gateway_exposure(self) -> None:
        cases = (
            (
                InstallRequest(
                    cluster=ClusterTarget(namespace="inferops-system"),
                    profile="default",
                    exposure="load-balancer",
                    load_balancer_class="example.com/internal",
                    gateway_auth_secret="inferops-gateway-token",
                ),
                (
                    "service.type=LoadBalancer",
                    "service.loadBalancerClass=example.com/internal",
                    "auth.enabled=true",
                    "auth.secretName=inferops-gateway-token",
                ),
                "load-balancer",
            ),
            (
                InstallRequest(
                    cluster=ClusterTarget(namespace="inferops-system"),
                    profile="default",
                    exposure="ingress",
                    ingress_class="nginx",
                    ingress_hostname="models.example.com",
                    gateway_auth_secret="inferops-gateway-token",
                ),
                (
                    "ingress.enabled=true",
                    "ingress.className=nginx",
                    "ingress.hostname=models.example.com",
                    "auth.enabled=true",
                    "auth.secretName=inferops-gateway-token",
                ),
                "ingress",
            ),
            (
                InstallRequest(
                    cluster=ClusterTarget(namespace="inferops-system"),
                    profile="default",
                    exposure="gateway-api",
                    gateway_name="public",
                    gateway_namespace="ingress-system",
                    gateway_section_name="https",
                    gateway_hostname="models.example.com",
                    gateway_auth_secret="inferops-gateway-token",
                ),
                (
                    "gatewayAPI.enabled=true",
                    "gatewayAPI.parentRefs[0].name=public",
                    "gatewayAPI.parentRefs[0].namespace=ingress-system",
                    "gatewayAPI.parentRefs[0].sectionName=https",
                    "gatewayAPI.hostnames[0]=models.example.com",
                    "auth.enabled=true",
                    "auth.secretName=inferops-gateway-token",
                ),
                "gateway-api",
            ),
        )

        for request, expected_values, expected_exposure in cases:
            with self.subTest(exposure=expected_exposure):
                commands: list[list[str]] = []

                def run(command):
                    commands.append(list(command))
                    return subprocess.CompletedProcess(command, 0, stdout="", stderr="")

                with tempfile.TemporaryDirectory() as directory:
                    charts_dir = self._make_charts(Path(directory))
                    request = replace(request, charts_dir=str(charts_dir))
                    response = HelmInstaller(runner=run).install(request)

                gateway_command = commands[2]
                for value in expected_values:
                    self.assertIn(value, gateway_command)
                self.assertEqual(response["install"]["exposure"], expected_exposure)
                self.assertTrue(response["install"]["authEnabled"])

    def test_external_exposure_requires_authentication_or_acknowledgement(self) -> None:
        unauthenticated = InstallRequest(
            cluster=ClusterTarget(namespace="inferops-system"),
            profile="default",
            exposure="ingress",
            ingress_class="nginx",
        )
        with self.assertRaisesRegex(CLIError, "requires --gateway-auth-secret"):
            HelmInstaller(
                runner=lambda command: self.fail("Helm should not run")
            ).install(unauthenticated)

        commands: list[list[str]] = []

        def run(command):
            commands.append(list(command))
            return subprocess.CompletedProcess(command, 0, stdout="", stderr="")

        with tempfile.TemporaryDirectory() as directory:
            charts_dir = self._make_charts(Path(directory))
            response = HelmInstaller(runner=run).install(
                replace(
                    unauthenticated,
                    allow_unauthenticated_exposure=True,
                    charts_dir=str(charts_dir),
                )
            )

        self.assertFalse(response["install"]["authEnabled"])
        self.assertNotIn("auth.enabled=true", commands[2])

    def test_rejects_incomplete_or_conflicting_exposure_options(self) -> None:
        requests = (
            InstallRequest(
                cluster=ClusterTarget(),
                profile="default",
                exposure="ingress",
            ),
            InstallRequest(
                cluster=ClusterTarget(),
                profile="default",
                exposure="gateway-api",
            ),
            InstallRequest(
                cluster=ClusterTarget(),
                profile="default",
                exposure="cluster-ip",
                load_balancer_class="example.com/internal",
            ),
            InstallRequest(
                cluster=ClusterTarget(),
                profile="default",
                exposure="ingress",
                ingress_class="nginx",
                tailscale_hostname="inferops",
            ),
            InstallRequest(
                cluster=ClusterTarget(),
                profile="default",
                allow_unauthenticated_exposure=True,
            ),
            InstallRequest(
                cluster=ClusterTarget(),
                profile="default",
                exposure="load-balancer",
                gateway_auth_secret="Invalid_Secret",
            ),
        )
        for request in requests:
            with self.subTest(request=request), self.assertRaises(CLIError):
                HelmInstaller(
                    runner=lambda command: self.fail("Helm should not run")
                ).install(request)

    def test_crd_apply_failure_stops_before_helm(self) -> None:
        commands: list[list[str]] = []

        def run(command):
            commands.append(list(command))
            raise subprocess.CalledProcessError(
                1, command, stderr="server-side apply conflict"
            )

        with tempfile.TemporaryDirectory() as directory:
            charts_dir = self._make_charts(Path(directory))
            with self.assertRaisesRegex(CLIError, "CRD apply failed"):
                HelmInstaller(runner=run).install(
                    InstallRequest(
                        cluster=ClusterTarget(namespace="inferops-system"),
                        profile="default",
                        charts_dir=str(charts_dir),
                    )
                )

        self.assertEqual(len(commands), 1)
        self.assertEqual(commands[0][0], "kubectl")

    def test_repeated_install_reapplies_crds_before_each_upgrade(self) -> None:
        commands: list[list[str]] = []

        def run(command):
            commands.append(list(command))
            return subprocess.CompletedProcess(command, 0, stdout="", stderr="")

        with tempfile.TemporaryDirectory() as directory:
            charts_dir = self._make_charts(Path(directory))
            request = InstallRequest(
                cluster=ClusterTarget(namespace="inferops-system"),
                profile="default",
                charts_dir=str(charts_dir),
            )
            installer = HelmInstaller(runner=run)
            installer.install(request)
            installer.install(request)

        self.assertEqual(len(commands), 6)
        self.assertEqual([command[0] for command in commands], [
            "kubectl",
            "helm",
            "helm",
            "kubectl",
            "helm",
            "helm",
        ])
        self.assertEqual(commands[:3], commands[3:])

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

    def test_rejects_invalid_compute_profile_before_running_helm(self) -> None:
        with self.assertRaisesRegex(CLIError, "unsupported compute profile"):
            HelmInstaller(runner=lambda command: self.fail("Helm should not run")).install(
                InstallRequest(
                    cluster=ClusterTarget(namespace="inferops-system"),
                    profile="homelab",
                    compute_profile="amd-gpu",
                    charts_dir="/not-used",
                )
            )

    def test_reports_helm_stderr_without_exposing_command_arguments(self) -> None:
        def fail(command):
            if command[0] == "kubectl":
                return subprocess.CompletedProcess(command, 0, stdout="", stderr="")
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

    def test_upgrade_builds_operator_and_dashboard_commands(self) -> None:
        commands: list[list[str]] = []

        def run(command):
            commands.append(list(command))
            return subprocess.CompletedProcess(command, 0, stdout="upgraded\n", stderr="")

        with tempfile.TemporaryDirectory() as directory:
            charts_dir = self._make_charts(Path(directory), include_dashboard=True)
            response = HelmInstaller(runner=run).upgrade(
                UpgradeRequest(
                    cluster=ClusterTarget(
                        namespace="inferops-system",
                        context="prod",
                        kubeconfig="/tmp/kubeconfig",
                    ),
                    tag="v0.2.0",
                    enable_observability=True,
                    charts_dir=str(charts_dir),
                )
            )

        self.assertEqual(len(commands), 3)
        crd_command, operator_command, dashboard_command = commands
        self.assertEqual(crd_command[0], "kubectl")
        self.assertIn("--server-side", crd_command)
        self.assertIn("--context", crd_command)
        self.assertEqual(operator_command[:3], ["helm", "upgrade", "inferops-operator"])
        self.assertIn("--reuse-values", operator_command)
        self.assertIn("image.repository=ghcr.io/brassinai/inferops-operator", operator_command)
        self.assertIn("image.tag=v0.2.0", operator_command)
        self.assertIn("serviceMonitor.enabled=true", operator_command)
        self.assertIn("dashboards.enabled=true", operator_command)
        self.assertEqual(dashboard_command[:3], ["helm", "upgrade", "inferops-dashboard"])
        self.assertIn("--reuse-values", dashboard_command)
        self.assertIn("image.repository=ghcr.io/brassinai/inferops-dashboard", dashboard_command)
        self.assertIn("image.tag=v0.2.0", dashboard_command)
        self.assertEqual(response["upgrade"]["tag"], "v0.2.0")
        self.assertTrue(response["upgrade"]["dashboardIncluded"])
        self.assertTrue(response["upgrade"]["observabilityEnabled"])

    def test_upgrade_can_skip_dashboard(self) -> None:
        commands: list[list[str]] = []

        def run(command):
            commands.append(list(command))
            return subprocess.CompletedProcess(command, 0, stdout="", stderr="")

        with tempfile.TemporaryDirectory() as directory:
            charts_dir = self._make_charts(Path(directory))
            response = HelmInstaller(runner=run).upgrade(
                UpgradeRequest(
                    cluster=ClusterTarget(namespace="inferops-system"),
                    tag="v0.2.0",
                    include_dashboard=False,
                    charts_dir=str(charts_dir),
                )
            )

        self.assertEqual(len(commands), 2)
        self.assertEqual(commands[1][:3], ["helm", "upgrade", "inferops-operator"])
        self.assertFalse(response["upgrade"]["dashboardIncluded"])

    def test_upgrade_rejects_tagged_repository(self) -> None:
        with self.assertRaisesRegex(CLIError, "without a tag"):
            HelmInstaller(runner=lambda command: self.fail("Helm should not run")).upgrade(
                UpgradeRequest(
                    cluster=ClusterTarget(namespace="inferops-system"),
                    tag="v0.2.0",
                    operator_image_repository="ghcr.io/brassinai/inferops-operator:v0.1.0",
                    charts_dir="/not-used",
                )
            )

    @staticmethod
    def _make_charts(
        root: Path,
        include_homelab_values: bool = False,
        include_dashboard: bool = False,
    ) -> Path:
        charts = ["inferops-operator", "inferops-gateway"]
        if include_dashboard:
            charts.append("inferops-dashboard")
        for chart in charts:
            chart_dir = root / chart
            chart_dir.mkdir()
            (chart_dir / "Chart.yaml").write_text(
                "apiVersion: v2\nname: test\nversion: 0.0.0\n"
            )
            if include_homelab_values:
                (chart_dir / "values-homelab.yaml").write_text("{}\n")
        crds_dir = root / "inferops-operator" / "crds"
        crds_dir.mkdir()
        (crds_dir / "modeldeployments.yaml").write_text(
            "apiVersion: apiextensions.k8s.io/v1\n"
            "kind: CustomResourceDefinition\n"
        )
        return root
