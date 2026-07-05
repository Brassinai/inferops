"""Tests for activation and operational CLI behavior."""

from __future__ import annotations

import argparse
from contextlib import redirect_stderr, redirect_stdout
import io
import json
from types import SimpleNamespace
import unittest

from inferops_cli import activate, delete, endpoints, models
from inferops_cli.k8s_client import LiveKubernetesClient, _summarize_deployment
from inferops_cli.kube import (
    ActivationRequest,
    ClusterTarget,
    DeactivationRequest,
    LogsRequest,
)
from tests.python.fake_kube_client import FakeKubernetesClient


def args(**overrides) -> argparse.Namespace:
    values = {
        "namespace": "team-a",
        "context": "kind-dev",
        "kubeconfig": None,
        "output": "json",
        "name": "qwen-chat",
        "when_full": None,
        "timeout": 30,
        "no_wait": False,
    }
    values.update(overrides)
    return argparse.Namespace(**values)


def deployment(
    *,
    phase: str,
    generation: int = 2,
    observed_generation: int = 2,
    reason: str = "",
    message: str = "",
    when_full: str = "Queue",
) -> dict:
    conditions = []
    if reason or message:
        conditions.append(
            {
                "type": "Ready",
                "status": "True" if phase == "Active" else "False",
                "reason": reason,
                "message": message,
                "observedGeneration": observed_generation,
            }
        )
    return {
        "apiVersion": "inference.inferops.dev/v1alpha1",
        "kind": "ModelDeployment",
        "metadata": {
            "name": "qwen-chat",
            "namespace": "team-a",
            "generation": generation,
        },
        "spec": {
            "model": {"repo": "Qwen/Qwen2.5-7B-Instruct"},
            "runtime": {"ref": "nano-vllm"},
            "activation": {"desiredState": "Active", "whenFull": when_full},
            "routing": {"enabled": True},
            "secrets": {"huggingFaceTokenSecretName": "must-not-be-returned"},
        },
        "status": {
            "phase": phase,
            "observedGeneration": observed_generation,
            "endpoint": "/models/qwen-chat/v1",
            "serviceName": "qwen-chat-runtime",
            "conditions": conditions,
        },
    }


def live_client(custom_api) -> LiveKubernetesClient:
    client = LiveKubernetesClient.__new__(LiveKubernetesClient)
    client._cluster = ClusterTarget(namespace="team-a", context="kind-dev")
    client._custom_api = custom_api
    client._core_api = None
    client._apps_api = None
    client._batch_api = None
    client._node_api = None
    client._networking_api = None
    client._discovery_api = None
    return client


class FakeOperationalCommandsTest(unittest.TestCase):
    def setUp(self) -> None:
        self.client = FakeKubernetesClient()
        cluster = ClusterTarget(namespace="team-a", context="kind-dev")
        key = self.client._resource_key(cluster, "qwen-chat")
        self.key = key
        self.client._deployments[key] = {
            "name": "qwen-chat",
            "namespace": "team-a",
            "phase": "Cached",
            "desiredState": "Inactive",
            "whenFull": "Queue",
            "runtime": "nano-vllm",
            "model": "Qwen/Qwen2.5-7B-Instruct",
            "endpoint": "/models/qwen-chat/v1",
            "serviceName": "qwen-chat-runtime",
            "assignedNode": "",
            "assignedGPUs": [],
            "cache": {},
            "replicas": {"desired": 0, "ready": 0},
            "modelLoaded": False,
            "observedGeneration": 1,
            "generation": 1,
            "conditions": [],
        }

    def test_status_summary_exposes_replacement_without_secret_references(self) -> None:
        resource = deployment(phase="Draining")
        resource["status"]["drainStartedAt"] = "2026-07-04T12:00:00Z"
        resource["status"]["replacement"] = {
            "phase": "Draining",
            "requestGeneration": 7,
            "target": {
                "namespace": "team-a",
                "name": "old-model",
                "uid": "old-model-uid",
                "token": "must-not-be-returned",
            },
            "startedAt": "2026-07-04T12:00:00Z",
            "message": "draining old-model",
            "internalField": "must-not-be-returned",
        }

        summary = _summarize_deployment(resource)

        self.assertEqual(summary["replacement"]["phase"], "Draining")
        self.assertEqual(summary["replacement"]["requestGeneration"], 7)
        self.assertNotIn("internalField", summary["replacement"])
        self.assertNotIn("token", summary["replacement"]["target"])
        self.assertNotIn("secrets", summary)

    def test_activate_reports_active_and_explicit_replacement_policy(self) -> None:
        stdout, _, code = self._run(
            activate.run,
            args(when_full="ReplaceOldest"),
        )

        payload = json.loads(stdout)
        self.assertEqual(code, 0)
        self.assertEqual(payload["outcome"], "active")
        self.assertEqual(payload["deployment"]["whenFull"], "ReplaceOldest")
        self.assertEqual(payload["transitions"][-1]["phase"], "Active")

    def test_activate_reports_waiting_without_failure_exit(self) -> None:
        self.client._activation_outcomes[self.key] = "waiting"

        stdout, _, code = self._run(activate.run, args())

        payload = json.loads(stdout)
        self.assertEqual(code, 0)
        self.assertEqual(payload["outcome"], "waiting")
        self.assertEqual(payload["deployment"]["phase"], "WaitingForGPU")

    def test_replacement_does_not_report_waiting_as_queued_success(self) -> None:
        self.client._activation_outcomes[self.key] = "waiting"

        stdout, _, code = self._run(
            activate.run,
            args(when_full="ReplaceOldest"),
        )

        self.assertEqual(code, 1)
        self.assertEqual(json.loads(stdout)["outcome"], "timeout")

    def test_activate_reports_rejection_and_failure(self) -> None:
        for outcome in ("rejected", "failed"):
            with self.subTest(outcome=outcome):
                self.client._activation_outcomes[self.key] = outcome
                stdout, _, code = self._run(activate.run, args())
                payload = json.loads(stdout)
                self.assertEqual(code, 1)
                self.assertEqual(payload["outcome"], outcome)
                self.assertEqual(payload["deployment"]["phase"], "Failed")

    def test_activate_without_policy_does_not_enable_replacement(self) -> None:
        stdout, _, code = self._run(activate.run, args())

        self.assertEqual(code, 0)
        self.assertEqual(json.loads(stdout)["deployment"]["whenFull"], "Queue")

    def test_text_activation_prints_transition_before_result(self) -> None:
        stdout, _, code = self._run(activate.run, args(output="text"))

        lines = stdout.splitlines()
        self.assertEqual(code, 0)
        self.assertEqual(lines[0], "Active: runtime is ready")
        self.assertIn("Activation for qwen-chat is active", lines[1])

    def test_models_endpoints_and_delete_are_cache_explicit(self) -> None:
        self.client._caches[self.key] = {
            "name": "qwen-chat",
            "referencedBy": ["qwen-chat"],
        }
        models_stdout, _, _ = self._run(models.run, args())
        endpoints_stdout, _, _ = self._run(endpoints.run, args())
        delete_stdout, _, _ = self._run(delete.run, args())

        self.assertEqual(json.loads(models_stdout)["models"][0]["name"], "qwen-chat")
        self.assertEqual(
            json.loads(endpoints_stdout)["endpoints"][0]["serviceName"],
            "qwen-chat-runtime",
        )
        self.assertTrue(json.loads(delete_stdout)["cachePreserved"])
        self.assertEqual(self.client._caches[self.key]["referencedBy"], [])

    def _run(self, command, command_args) -> tuple[str, str, int]:
        stdout = io.StringIO()
        stderr = io.StringIO()
        with redirect_stdout(stdout), redirect_stderr(stderr):
            code = command(command_args, self.client)
        return stdout.getvalue(), stderr.getvalue(), code


class LiveOperationalClientTest(unittest.TestCase):
    def test_activate_ignores_stale_status_and_records_transitions(self) -> None:
        class CustomAPI:
            def __init__(self):
                self.patched_body = None
                self.field_manager = None
                self.responses = [
                    deployment(
                        phase="WaitingForGPU",
                        reason="ReplacementPending",
                        message="waiting for the selected workload to drain",
                        when_full="ReplaceLowestPriority",
                    ),
                    deployment(
                        phase="Activating",
                        reason="RuntimeStarting",
                        message="runtime Pod is starting",
                        when_full="ReplaceLowestPriority",
                    ),
                    deployment(
                        phase="Active",
                        reason="RuntimeReady",
                        message="runtime is ready",
                        when_full="ReplaceLowestPriority",
                    ),
                ]

            def patch_namespaced_custom_object(self, **kwargs):
                self.patched_body = kwargs["body"]
                self.field_manager = kwargs["field_manager"]
                return deployment(
                    phase="Active",
                    observed_generation=1,
                    reason="RuntimeReady",
                    message="stale ready status",
                )

            def get_namespaced_custom_object(self, **_kwargs):
                return self.responses.pop(0)

        api = CustomAPI()
        client = live_client(api)

        response = client.activate(
            ActivationRequest(
                cluster=client._cluster,
                name="qwen-chat",
                when_full="ReplaceLowestPriority",
                timeout_seconds=1,
                poll_interval_seconds=0,
            )
        )

        self.assertEqual(
            api.patched_body,
            {
                "spec": {
                    "activation": {
                        "desiredState": "Active",
                        "whenFull": "ReplaceLowestPriority",
                    }
                }
            },
        )
        self.assertEqual(api.field_manager, "inferops-cli")
        self.assertEqual(response["outcome"], "active")
        self.assertEqual(
            [item["phase"] for item in response["transitions"]],
            ["WaitingForGPU", "Activating", "Active"],
        )
        self.assertNotIn("stale ready status", json.dumps(response))

    def test_activate_classifies_capacity_rejection(self) -> None:
        rejected = deployment(
            phase="Failed",
            reason="CapacityRejected",
            message="capacity policy rejected activation",
        )

        class CustomAPI:
            def patch_namespaced_custom_object(self, **_kwargs):
                return rejected

        client = live_client(CustomAPI())
        response = client.activate(
            ActivationRequest(
                cluster=client._cluster,
                name="qwen-chat",
                timeout_seconds=1,
                poll_interval_seconds=0,
            )
        )

        self.assertEqual(response["outcome"], "rejected")

    def test_deactivate_uses_cli_field_manager(self) -> None:
        class CustomAPI:
            def __init__(self):
                self.patched_body = None
                self.field_manager = None

            def patch_namespaced_custom_object(self, **kwargs):
                self.patched_body = kwargs["body"]
                self.field_manager = kwargs["field_manager"]
                result = deployment(phase="Cached")
                result["spec"]["activation"]["desiredState"] = "Inactive"
                return result

        api = CustomAPI()
        client = live_client(api)

        response = client.deactivate(
            DeactivationRequest(
                cluster=client._cluster,
                name="qwen-chat",
                wait=False,
            )
        )

        self.assertEqual(
            api.patched_body,
            {"spec": {"activation": {"desiredState": "Inactive"}}},
        )
        self.assertEqual(api.field_manager, "inferops-cli")
        self.assertEqual(response["outcome"], "requested")

    def test_model_and_endpoint_lists_whitelist_fields(self) -> None:
        item = deployment(phase="Active")
        item["status"]["endpoint"] = ""

        class CustomAPI:
            def list_namespaced_custom_object(self, **_kwargs):
                return {"items": [item]}

        client = live_client(CustomAPI())
        model_response = client.models(client._cluster)
        endpoint_response = client.endpoints(client._cluster)

        rendered = json.dumps((model_response, endpoint_response))
        self.assertNotIn("must-not-be-returned", rendered)
        self.assertEqual(
            endpoint_response["endpoints"][0]["endpoint"],
            "/models/qwen-chat/v1",
        )

    def test_logs_select_current_runtime_pod_and_container(self) -> None:
        class CustomAPI:
            def get_namespaced_custom_object(self, **_kwargs):
                return deployment(phase="Active")

        class CoreAPI:
            def __init__(self):
                self.selector = ""
                self.log_request = {}

            def list_namespaced_pod(self, *, namespace, label_selector):
                self.selector = label_selector
                return SimpleNamespace(
                    items=[
                        self._pod(
                            "old-runtime",
                            phase="Running",
                            created="2026-01-01T00:00:00Z",
                            deleting=True,
                        ),
                        self._pod(
                            "current-runtime",
                            phase="Running",
                            created="2026-01-02T00:00:00Z",
                        ),
                    ]
                )

            def read_namespaced_pod_log(self, **kwargs):
                self.log_request = kwargs
                return "runtime ready\nrequest complete"

            @staticmethod
            def _pod(name, *, phase, created, deleting=False):
                return SimpleNamespace(
                    metadata=SimpleNamespace(
                        name=name,
                        creation_timestamp=created,
                        deletion_timestamp="now" if deleting else None,
                    ),
                    status=SimpleNamespace(phase=phase),
                )

        core_api = CoreAPI()
        client = live_client(CustomAPI())
        client._core_api = core_api

        response = client.logs(
            LogsRequest(
                cluster=client._cluster,
                name="qwen-chat",
                tail=20,
            )
        )

        self.assertEqual(
            core_api.selector, "inferops.dev/modeldeployment=qwen-chat"
        )
        self.assertEqual(core_api.log_request["name"], "current-runtime")
        self.assertEqual(core_api.log_request["container"], "runtime")
        self.assertEqual(response["lines"], ["runtime ready", "request complete"])


if __name__ == "__main__":
    unittest.main()
