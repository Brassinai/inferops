"""Focused tests for the live Kubernetes inspection boundary."""

from __future__ import annotations

from types import SimpleNamespace
import unittest

from kubernetes.client.rest import ApiException

from inferops_cli import k8s_client
from inferops_cli.cluster_resources import gpu_inventory
from inferops_cli.contracts import CheckStatus, DoctorCheck
from inferops_cli.errors import CLIError
from inferops_cli.k8s_client import LiveKubernetesClient
from inferops_cli.kube import CacheDeleteRequest, ClusterTarget, DeployRequest, DoctorRequest


def obj(**values):
    return SimpleNamespace(**values)


def node(
    name: str = "node-a",
    capacity: dict | None = None,
    allocatable: dict | None = None,
    ready: bool = True,
):
    return obj(
        metadata=obj(name=name, labels={}),
        status=obj(
            capacity=capacity or {},
            allocatable=allocatable or {},
            conditions=[obj(type="Ready", status="True" if ready else "False")],
        ),
    )


def pod(
    node_name: str,
    requests: dict | None = None,
    *,
    phase: str = "Running",
    init_requests: dict | None = None,
    init_restart_policy: str | None = None,
    runtime_class_name: str | None = None,
):
    container = obj(resources=obj(requests=requests or {}, limits={}))
    init = obj(
        resources=obj(requests=init_requests or {}, limits={}),
        restart_policy=init_restart_policy,
    )
    return obj(
        metadata=obj(name="pod-a", namespace="default", labels={}),
        status=obj(phase=phase, container_statuses=[]),
        spec=obj(
            node_name=node_name,
            containers=[container],
            init_containers=[init] if init_requests else [],
            runtime_class_name=runtime_class_name,
        ),
    )


def live_client() -> LiveKubernetesClient:
    client = LiveKubernetesClient.__new__(LiveKubernetesClient)
    client._cluster = ClusterTarget(namespace="inferops-system")
    client._custom_api = None
    client._core_api = None
    client._apps_api = None
    client._batch_api = None
    client._node_api = None
    client._networking_api = None
    client._discovery_api = None
    return client


class GPUInventoryTest(unittest.TestCase):
    def test_inventory_supports_intel_and_effective_init_requests(self) -> None:
        nodes = [
            node(
                capacity={"gpu.intel.com/i915": "2"},
                allocatable={"gpu.intel.com/i915": "2"},
            )
        ]
        pods = [
            pod(
                "node-a",
                {"gpu.intel.com/i915": "1"},
                init_requests={"gpu.intel.com/i915": "2"},
            ),
            pod("node-a", {"gpu.intel.com/i915": "1"}, phase="Succeeded"),
        ]

        inventory = gpu_inventory(nodes, pods)

        self.assertEqual(inventory[0]["vendor"], "intel")
        self.assertEqual(inventory[0]["occupied"], 2)
        self.assertEqual(inventory[0]["available"], 0)

    def test_unknown_occupancy_is_not_rendered_as_zero(self) -> None:
        nodes = [
            node(
                capacity={"nvidia.com/gpu": "1"},
                allocatable={"nvidia.com/gpu": "1"},
            )
        ]

        inventory = gpu_inventory(nodes, None)

        self.assertIsNone(inventory[0]["occupied"])
        self.assertIsNone(inventory[0]["available"])

    def test_restartable_init_sidecar_is_added_to_steady_state(self) -> None:
        nodes = [
            node(
                capacity={"nvidia.com/gpu": "3"},
                allocatable={"nvidia.com/gpu": "3"},
            )
        ]
        pods = [
            pod(
                "node-a",
                {"nvidia.com/gpu": "1"},
                init_requests={"nvidia.com/gpu": "1"},
                init_restart_policy="Always",
            )
        ]

        inventory = gpu_inventory(nodes, pods)

        self.assertEqual(inventory[0]["occupied"], 2)


class LiveClientSafetyTest(unittest.TestCase):
    def test_deploy_reports_missing_crds_actionably(self) -> None:
        client = live_client()
        client._cluster = ClusterTarget(namespace="default", context="orbstack")

        class CustomObjects:
            def get_namespaced_custom_object(self, **_kwargs):
                raise ApiException(status=404, reason="Not Found")

            def create_namespaced_custom_object(self, **_kwargs):
                raise ApiException(status=404, reason="Not Found")

        client._custom_api = CustomObjects()
        manifest = {
            "apiVersion": "inference.inferops.dev/v1alpha1",
            "kind": "ModelDeployment",
            "metadata": {"name": "cpu-smollm"},
            "spec": {"activation": {"desiredState": "Inactive"}},
        }

        with self.assertRaisesRegex(CLIError, "InferOps CRDs are not available"):
            client.deploy(
                DeployRequest(
                    cluster=client._cluster,
                    app_path="app.py",
                    manifests=[manifest],
                )
            )

    def test_deploy_replace_includes_existing_resource_version(self) -> None:
        client = live_client()
        client._cluster = ClusterTarget(namespace="default", context="default")

        class CustomObjects:
            def __init__(self):
                self.replaced_body = None

            def get_namespaced_custom_object(self, **_kwargs):
                return {
                    "metadata": {
                        "name": "gpu-vllm-qwen",
                        "resourceVersion": "12345",
                    }
                }

            def replace_namespaced_custom_object(self, **kwargs):
                self.replaced_body = kwargs["body"]

        custom_api = CustomObjects()
        client._custom_api = custom_api
        manifest = {
            "apiVersion": "inference.inferops.dev/v1alpha1",
            "kind": "ModelDeployment",
            "metadata": {"name": "gpu-vllm-qwen"},
            "spec": {"activation": {"desiredState": "Inactive"}},
        }

        result = client.deploy(
            DeployRequest(
                cluster=client._cluster,
                app_path="app.py",
                manifests=[manifest],
            )
        )

        self.assertEqual(result["deployments"][0]["action"], "replaced")
        self.assertEqual(
            custom_api.replaced_body["metadata"]["resourceVersion"],
            "12345",
        )

    def test_gpu_list_reports_unknown_when_cluster_pod_list_is_forbidden(self) -> None:
        client = live_client()

        class Core:
            def list_node(self):
                return obj(
                    items=[
                        node(
                            capacity={"nvidia.com/gpu": "1"},
                            allocatable={"nvidia.com/gpu": "1"},
                        )
                    ]
                )

            def list_pod_for_all_namespaces(self):
                raise ApiException(status=403, reason="forbidden")

        client._core_api = Core()

        result = client.gpu_list(client._cluster)

        self.assertFalse(result["occupancyKnown"])
        self.assertIsNone(result["gpus"][0]["occupied"])

    def test_cache_list_survives_restricted_deployment_rbac(self) -> None:
        client = live_client()

        class Custom:
            def list_namespaced_custom_object(self, *, plural, **_):
                if plural == "modeldeployments":
                    raise ApiException(status=403, reason="forbidden")
                return {
                    "items": [
                        {
                            "metadata": {"name": "cache-a"},
                            "spec": {
                                "modelRepo": "repo/model",
                                "storage": {"path": "/cache/a", "size": "1Gi"},
                            },
                            "status": {},
                        }
                    ]
                }

        client._custom_api = Custom()

        result = client.cache_list(client._cluster)

        self.assertFalse(result["referencesKnown"])
        self.assertFalse(result["caches"][0]["referencesKnown"])

    def test_forced_cache_delete_annotates_and_deletes_resource(self) -> None:
        client = live_client()

        class Custom:
            def __init__(self):
                self.patched = False
                self.deleted = False

            def get_namespaced_custom_object(self, **_):
                return {"spec": {"storage": {"path": "/cache/a"}}}

            def list_namespaced_custom_object(self, **_):
                return {
                    "items": [
                        {
                            "metadata": {"name": "model-a"},
                            "status": {"cache": {"path": "/cache/a"}},
                        }
                    ]
                }

            def patch_namespaced_custom_object(self, **_):
                self.patched = True

            def delete_namespaced_custom_object(self, **_):
                self.deleted = True

        custom = Custom()
        client._custom_api = custom
        client._core_api = obj(list_namespaced_pod=lambda **_: obj(items=[]))

        result = client.cache_delete(
            CacheDeleteRequest(cluster=client._cluster, name="cache-a", force=True)
        )

        self.assertTrue(custom.patched)
        self.assertTrue(custom.deleted)
        self.assertTrue(result["deleted"])
        self.assertFalse(result["nodeFilesModified"])

    def test_cache_delete_fails_closed_for_ambiguous_reference(self) -> None:
        client = live_client()

        class Custom:
            def get_namespaced_custom_object(self, **_):
                return {"spec": {"storage": {"path": "/cache/a"}}}

            def list_namespaced_custom_object(self, **_):
                return {
                    "items": [
                        {
                            "metadata": {"name": "model-a"},
                            "status": {"cache": {"state": "Ready"}},
                        }
                    ]
                }

        client._custom_api = Custom()
        client._core_api = obj(list_namespaced_pod=lambda **_: obj(items=[]))

        with self.assertRaisesRegex(CLIError, "cannot safely delete"):
            client.cache_delete(
                CacheDeleteRequest(cluster=client._cluster, name="cache-a")
            )

    def test_cache_delete_refuses_pending_matching_deployment(self) -> None:
        client = live_client()

        class Custom:
            def get_namespaced_custom_object(self, **_):
                return {
                    "metadata": {"name": "shared-cache"},
                    "spec": {
                        "modelRepo": "repo/model",
                        "revision": "main",
                        "storage": {"path": "/cache/model"},
                    },
                }

            def list_namespaced_custom_object(self, **_):
                return {
                    "items": [
                        {
                            "metadata": {"name": "pending-model"},
                            "spec": {
                                "model": {"repo": "repo/model", "revision": "main"},
                                "cache": {"enabled": True},
                            },
                            "status": {},
                        }
                    ]
                }

        client._custom_api = Custom()
        client._core_api = obj(list_namespaced_pod=lambda **_: obj(items=[]))

        with self.assertRaisesRegex(CLIError, "pending-model"):
            client.cache_delete(
                CacheDeleteRequest(cluster=client._cluster, name="shared-cache")
            )

    def test_cache_delete_refuses_live_pod_mount(self) -> None:
        client = live_client()

        class Custom:
            def get_namespaced_custom_object(self, **_):
                return {
                    "metadata": {"name": "cache-a"},
                    "spec": {"storage": {"path": "/cache/a"}},
                }

            def list_namespaced_custom_object(self, **_):
                return {"items": []}

        mounted_pod = obj(
            metadata=obj(name="runtime-a", uid="pod-1", resource_version="1"),
            status=obj(phase="Running"),
            spec=obj(volumes=[obj(host_path=obj(path="/cache/a"))]),
        )
        client._custom_api = Custom()
        client._core_api = obj(list_namespaced_pod=lambda **_: obj(items=[mounted_pod]))

        with self.assertRaisesRegex(CLIError, "mounted by live Pods"):
            client.cache_delete(
                CacheDeleteRequest(cluster=client._cluster, name="cache-a")
            )

    def test_doctor_contains_unexpected_check_failures(self) -> None:
        client = live_client()
        client._check_kubernetes_api = lambda: DoctorCheck(
            id="kubernetes-api",
            status=CheckStatus.PASS,
            message="ok",
        )
        client._check_runtime_class = lambda: (_ for _ in ()).throw(
            AttributeError("bad client method")
        )

        result = client.doctor(
            DoctorRequest(
                cluster=client._cluster,
                checks=["kubernetes-api", "runtime-class"],
            )
        )

        self.assertEqual(
            [item["status"] for item in result["checks"]], ["PASS", "FAIL"]
        )

    def test_doctor_adds_remediation_to_every_non_pass_result(self) -> None:
        client = live_client()
        client._check_gpu_capacity = lambda: DoctorCheck(
            id="gpu-capacity",
            status=CheckStatus.WARN,
            message="capacity unknown",
        )

        result = client.doctor(
            DoctorRequest(cluster=client._cluster, checks=["gpu-capacity"])
        )

        self.assertTrue(result["checks"][0]["remediation"])


class DoctorCheckTest(unittest.TestCase):
    def test_runtime_class_uses_node_api_and_only_checks_references(self) -> None:
        client = live_client()
        client._core_api = obj(
            list_namespaced_pod=lambda **_: obj(
                items=[pod("node-a", runtime_class_name="nvidia")]
            )
        )
        client._node_api = obj(
            list_runtime_class=lambda: obj(items=[obj(metadata=obj(name="nvidia"))])
        )
        client._apps_api = obj(
            list_namespaced_deployment=lambda **_: obj(items=[]),
            list_daemon_set_for_all_namespaces=lambda: obj(items=[]),
        )

        check = client._check_runtime_class()

        self.assertEqual(check.status, CheckStatus.PASS)

    def test_cache_probe_parses_free_space_and_always_deletes(self) -> None:
        client = live_client()

        class Core:
            def __init__(self):
                self.pod = obj(
                    metadata=obj(name="probe-pod"),
                    status=obj(phase="Succeeded", container_statuses=[]),
                )

            def list_namespaced_pod(self, **_):
                return obj(items=[self.pod])

            def read_namespaced_pod_log(self, **_):
                return (
                    "Filesystem 1024-blocks Used Available Capacity Mounted on\n"
                    "/dev/nvme0n1 1000 400 600 40% /cache\n"
                )

        class Batch:
            def __init__(self):
                self.body = None
                self.deleted = False

            def create_namespaced_job(self, *, body, **_):
                self.body = body

            def delete_namespaced_job(self, **_):
                self.deleted = True

        core = Core()
        batch = Batch()
        client._core_api = core
        client._batch_api = batch

        result = client._run_cache_probe(
            "node-a",
            "/var/lib/inferops/models",
            "busybox:1.36.1",
        )

        self.assertEqual(result["freeBytes"], 600 * 1024)
        self.assertEqual(
            batch.body["spec"]["template"]["spec"]["containers"][0]["command"],
            ["df", "-Pk", "/cache"],
        )
        self.assertEqual(batch.body["spec"]["activeDeadlineSeconds"], 45)
        self.assertEqual(batch.body["spec"]["ttlSecondsAfterFinished"], 60)
        self.assertTrue(batch.deleted)

    def test_cache_probe_cleans_up_after_api_failure(self) -> None:
        client = live_client()

        class Core:
            def list_namespaced_pod(self, **_):
                raise ApiException(status=500, reason="read failed")

        class Batch:
            def __init__(self):
                self.deleted = False

            def create_namespaced_job(self, **_):
                return None

            def delete_namespaced_job(self, **_):
                self.deleted = True

        core = Core()
        batch = Batch()
        client._core_api = core
        client._batch_api = batch

        result = client._run_cache_probe(
            "node-a",
            "/var/lib/inferops/models",
            "busybox:1.36.1",
        )

        self.assertEqual(result["status"], "error")
        self.assertTrue(batch.deleted)

    def test_device_plugin_discovers_healthy_daemonset_in_any_namespace(self) -> None:
        client = live_client()
        gpu_node = node(
            capacity={"nvidia.com/gpu": "1"},
            allocatable={"nvidia.com/gpu": "1"},
        )
        client._core_api = obj(
            list_node=lambda: obj(items=[gpu_node]),
            list_namespaced_config_map=lambda **_: obj(items=[]),
        )
        daemon_set = obj(
            metadata=obj(
                name="nvdp-nvidia-device-plugin", namespace="nvidia-device-plugin"
            ),
            spec=obj(
                template=obj(
                    spec=obj(
                        containers=[
                            obj(image="nvcr.io/nvidia/k8s-device-plugin:v0.17.0")
                        ]
                    )
                )
            ),
            status=obj(desired_number_scheduled=1, number_ready=1),
        )
        client._apps_api = obj(
            list_daemon_set_for_all_namespaces=lambda: obj(items=[daemon_set])
        )

        check = client._check_device_plugin()

        self.assertEqual(check.status, CheckStatus.PASS)

    def test_device_plugin_warns_when_workload_cannot_be_identified(self) -> None:
        client = live_client()
        gpu_node = node(
            capacity={"nvidia.com/gpu": "1"},
            allocatable={"nvidia.com/gpu": "1"},
        )
        client._core_api = obj(
            list_node=lambda: obj(items=[gpu_node]),
            list_namespaced_config_map=lambda **_: obj(items=[]),
        )
        client._apps_api = obj(list_daemon_set_for_all_namespaces=lambda: obj(items=[]))

        check = client._check_device_plugin()

        self.assertEqual(check.status, CheckStatus.WARN)

    def test_runtime_class_checks_device_plugin_daemonsets(self) -> None:
        client = live_client()
        client._core_api = obj(list_namespaced_pod=lambda **_: obj(items=[]))
        daemon_set = obj(
            metadata=obj(
                name="vendor-device-plugin",
                labels={},
            ),
            spec=obj(
                template=obj(
                    spec=obj(
                        runtime_class_name="vendor-runtime",
                        containers=[obj(image="vendor/device-plugin:v1")],
                    )
                )
            ),
        )
        client._apps_api = obj(
            list_namespaced_deployment=lambda **_: obj(items=[]),
            list_daemon_set_for_all_namespaces=lambda: obj(items=[daemon_set]),
        )
        client._node_api = obj(list_runtime_class=lambda: obj(items=[]))

        check = client._check_runtime_class()

        self.assertEqual(check.status, CheckStatus.FAIL)
        self.assertIn("vendor-runtime", check.message)

    def test_cache_check_probes_ready_cpu_node(self) -> None:
        client = live_client()
        cpu_node = node()
        cpu_node.spec = obj(unschedulable=False)
        config_map = obj(
            metadata=obj(name="inferops-diagnostics"),
            data={
                "cache.root": "/var/lib/inferops/models",
                "cache.probeImage": (
                    "busybox@sha256:"
                    "73aaf090f3d85aa34ee199857f03fa3a95c8ede2ffd4cc2cdb5b94e566b11662"
                ),
            },
        )
        client._core_api = obj(
            list_namespaced_config_map=lambda **_: obj(items=[config_map]),
            list_node=lambda: obj(items=[cpu_node]),
        )
        client._custom_api = obj(
            list_namespaced_custom_object=lambda **_: {"items": []}
        )
        probed: list[str] = []
        client._run_cache_probe = lambda node_name, *_: (
            probed.append(node_name) or {"status": "ok", "freeBytes": 1024}
        )

        check = client._check_cache()

        self.assertEqual(check.status, CheckStatus.PASS)
        self.assertEqual(probed, ["node-a"])

    def test_gateway_uses_ready_endpoints_and_authenticated_service_proxy(self) -> None:
        client = live_client()
        deployment = obj(
            status=obj(ready_replicas=1),
            spec=obj(replicas=1),
        )
        service = obj(
            metadata=obj(name="inferops-gateway"),
            spec=obj(ports=[obj(name="http", port=80)]),
        )

        class Core:
            def __init__(self):
                self.proxy_called = False

            def list_namespaced_service(self, **_):
                return obj(items=[service])

            def connect_get_namespaced_service_proxy_with_path(self, **_):
                self.proxy_called = True
                return "ok"

        core = Core()
        client._core_api = core
        client._apps_api = obj(
            list_namespaced_deployment=lambda **_: obj(items=[deployment])
        )
        endpoint = obj(conditions=obj(ready=True))
        client._discovery_api = obj(
            list_namespaced_endpoint_slice=lambda **_: obj(
                items=[obj(endpoints=[endpoint])]
            )
        )

        check = client._check_gateway()

        self.assertEqual(check.status, CheckStatus.PASS)
        self.assertTrue(core.proxy_called)

    def test_tailscale_reads_tls_and_status_and_checks_reachability(self) -> None:
        client = live_client()
        ingress = obj(
            spec=obj(
                ingress_class_name="tailscale",
                tls=[obj(hosts=["inferops.example.ts.net"])],
            ),
            status=obj(
                load_balancer=obj(
                    ingress=[obj(hostname="inferops.example.ts.net", ip=None)]
                )
            ),
        )
        client._networking_api = obj(
            list_namespaced_ingress=lambda **_: obj(items=[ingress]),
            read_ingress_class=lambda **_: obj(metadata=obj(name="tailscale")),
        )
        requested: list[str] = []
        original = k8s_client._https_get
        k8s_client._https_get = lambda url, timeout: requested.append(url)
        try:
            check = client._check_tailscale()
        finally:
            k8s_client._https_get = original

        self.assertEqual(check.status, CheckStatus.PASS)
        self.assertEqual(requested, ["https://inferops.example.ts.net/readyz"])


if __name__ == "__main__":
    unittest.main()
