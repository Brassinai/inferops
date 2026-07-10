"""Tests for the homelab acceptance helper."""

from __future__ import annotations

import unittest

from scripts.homelab_acceptance import (
    StepResult,
    redact_text,
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


if __name__ == "__main__":
    unittest.main()
