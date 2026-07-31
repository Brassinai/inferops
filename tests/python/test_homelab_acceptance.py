"""Tests for the homelab acceptance helper."""

from __future__ import annotations

import argparse
import json
import unittest

from scripts.homelab_acceptance import (
    StepResult,
    Step,
    acceptance_steps,
    redact_text,
    run_step,
    validate_args,
    validate_shared_cache_references,
    validate_streaming_response,
)


class HomelabAcceptanceTests(unittest.TestCase):
    def test_redacts_bearer_tokens_without_destroying_log_shape(self) -> None:
        text = "Authorization: Bearer secret-token\nnext=line\ncurl --token abc123\n"

        redacted = redact_text(text)

        self.assertIn("Authorization: Bearer <redacted>", redacted)
        self.assertIn("next=line\n", redacted)
        self.assertIn("--token <redacted>", redacted)
        self.assertNotIn("secret-token", redacted)
        self.assertNotIn("abc123", redacted)

    def test_streaming_validator_requires_sse_content(self) -> None:
        result = StepResult(
            name="Streaming inference",
            command=[],
            required=True,
            returncode=0,
            duration_seconds=1,
            stdout='{"id":"not-streaming"}',
            stderr="",
        )

        self.assertIn("server-sent event", validate_streaming_response(result) or "")

    def test_streaming_validator_accepts_openai_stream(self) -> None:
        result = StepResult(
            name="Streaming inference",
            command=[],
            required=True,
            returncode=0,
            duration_seconds=1,
            stdout="data: {\"choices\":[]}\n\ndata: [DONE]\n\n",
            stderr="",
        )

        self.assertIsNone(validate_streaming_response(result))

    def test_shared_cache_validator_requires_one_cache_with_all_references(self) -> None:
        validator = validate_shared_cache_references(("cpu-smollm-a", "cpu-smollm-b"))
        result = StepResult(
            name="Cache state after deploy",
            command=[],
            required=True,
            returncode=0,
            duration_seconds=1,
            stdout=json.dumps(
                {
                    "caches": [
                        {
                            "name": "model-abc-cache",
                            "mode": "shared",
                            "referencedBy": ["cpu-smollm-a", "cpu-smollm-b"],
                        }
                    ]
                }
            ),
            stderr="",
        )

        self.assertIsNone(validator(result))

    def test_shared_cache_validator_rejects_split_references(self) -> None:
        validator = validate_shared_cache_references(("cpu-smollm-a", "cpu-smollm-b"))
        result = StepResult(
            name="Cache state after deploy",
            command=[],
            required=True,
            returncode=0,
            duration_seconds=1,
            stdout=json.dumps(
                {
                    "caches": [
                        {
                            "name": "cache-a",
                            "mode": "legacy",
                            "referencedBy": ["cpu-smollm-a"],
                        },
                        {
                            "name": "cache-b",
                            "mode": "legacy",
                            "referencedBy": ["cpu-smollm-b"],
                        },
                    ]
                }
            ),
            stderr="",
        )

        self.assertIn("expected exactly one shared cache", validator(result) or "")

    def test_acceptance_steps_include_shared_cache_delete_refusal(self) -> None:
        steps = acceptance_steps(
            argparse.Namespace(
                manifest="examples/yaml-deploy/modeldeployment-cpu-shared.yaml",
                model_name="cpu-smollm-a",
                gateway_url="http://gateway",
                namespace="inferops-system",
                context="orbstack",
                kubeconfig="/tmp/kubeconfig",
                cache_path="/var/lib/inferops/models",
                replacement_manifest=None,
                replacement_name=None,
                shared_cache_peer=["cpu-smollm-b"],
                skip_install=True,
            )
        )

        names = [step.name for step in steps]
        self.assertEqual(names[0], "Tool and cluster preflight")
        self.assertIn("Shared cache delete refusal", names)
        cache_steps = [step for step in steps if step.name.startswith("Cache state")]
        self.assertTrue(all(step.validator is not None for step in cache_steps))
        apply_step = next(step for step in steps if step.name == "Apply model manifest")
        self.assertIn("--context", apply_step.command)
        self.assertIn("orbstack", apply_step.command)
        self.assertIn("--kubeconfig", apply_step.command)
        self.assertIn("/tmp/kubeconfig", apply_step.command)
        doctor_step = next(step for step in steps if step.name == "Doctor")
        self.assertIn("--context", doctor_step.command)
        delete_step = next(step for step in steps if step.name == "Shared cache delete refusal")
        self.assertIn('"--context", "orbstack"', delete_step.command[-1])
        self.assertIn('"--kubeconfig", "/tmp/kubeconfig"', delete_step.command[-1])
        preflight_step = steps[0]
        self.assertIn('"--context", "orbstack"', preflight_step.command[-1])
        self.assertIn('"--kubeconfig", "/tmp/kubeconfig"', preflight_step.command[-1])

    def test_run_step_records_missing_command(self) -> None:
        result = run_step(
            Step(
                name="missing",
                command=["definitely-not-inferops-test-command"],
            ),
            timeout=1,
        )

        self.assertEqual(result.returncode, 127)
        self.assertIn("command not found", result.stderr)

    def test_acceptance_args_reject_duplicate_shared_cache_peer(self) -> None:
        parser = RaisingParser()
        args = acceptance_args(shared_cache_peer=["cpu-smollm-b", "cpu-smollm-b"])

        with self.assertRaisesRegex(ValueError, "repeated"):
            validate_args(args, parser)

    def test_acceptance_args_reject_model_name_as_shared_cache_peer(self) -> None:
        parser = RaisingParser()
        args = acceptance_args(shared_cache_peer=["cpu-smollm-a"])

        with self.assertRaisesRegex(ValueError, "must not repeat"):
            validate_args(args, parser)


class RaisingParser(argparse.ArgumentParser):
    def error(self, message: str) -> None:
        raise ValueError(message)


def acceptance_args(**overrides) -> argparse.Namespace:
    values = {
        "manifest": "examples/yaml-deploy/modeldeployment-cpu-shared.yaml",
        "model_name": "cpu-smollm-a",
        "gateway_url": "http://gateway",
        "namespace": "inferops-system",
        "replacement_manifest": None,
        "replacement_name": None,
        "shared_cache_peer": ["cpu-smollm-b"],
    }
    values.update(overrides)
    return argparse.Namespace(**values)


if __name__ == "__main__":
    unittest.main()
