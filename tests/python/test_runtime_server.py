"""HTTP contract tests for the runtime ASGI application."""

from __future__ import annotations

import asyncio
import json
import os
import unittest
from unittest import mock

from runtimes.nano_vllm.fake_runtime import FakeRuntime
from runtimes.nano_vllm.server import create_app, make_runtime_from_env, parse_duration


async def run_inline(function, *args, **kwargs):
    """Execute worker calls inline to keep dependency-free ASGI tests deterministic."""
    return function(*args, **kwargs)


async def request(app, method: str, path: str, payload=None):
    messages = []
    body = b"" if payload is None else json.dumps(payload).encode()
    received = False

    async def receive():
        nonlocal received
        if received:
            return {"type": "http.disconnect"}
        received = True
        return {"type": "http.request", "body": body, "more_body": False}

    async def send(message):
        messages.append(message)

    await app({"type": "http", "method": method, "path": path}, receive, send)
    return messages


def response(messages):
    start = next(message for message in messages if message["type"] == "http.response.start")
    body = b"".join(message.get("body", b"") for message in messages if message["type"] == "http.response.body")
    return start, body


class RuntimeServerTest(unittest.TestCase):
    def setUp(self) -> None:
        self.to_thread = mock.patch("runtimes.nano_vllm.server.asyncio.to_thread", run_inline)
        self.to_thread.start()

    def tearDown(self) -> None:
        self.to_thread.stop()

    def test_readiness_is_false_until_loaded(self) -> None:
        runtime = FakeRuntime()
        app = create_app(runtime)

        start, body = response(asyncio.run(request(app, "GET", "/readiness")))
        self.assertEqual(start["status"], 503)
        self.assertEqual(json.loads(body), {"ready": False})

        runtime.load()
        start, _ = response(asyncio.run(request(app, "GET", "/readiness")))
        self.assertEqual(start["status"], 200)

    def test_non_streaming_completion(self) -> None:
        runtime = FakeRuntime()
        runtime.load()
        messages = asyncio.run(
            request(
                create_app(runtime),
                "POST",
                "/v1/completions",
                {"model": "test", "prompt": "hi", "max_tokens": 3},
            )
        )
        start, body = response(messages)
        payload = json.loads(body)

        self.assertEqual(start["status"], 200)
        self.assertEqual(payload["object"], "text_completion")
        self.assertEqual(payload["choices"][0]["text"], "hi<2>")

    def test_streaming_completion_sends_each_chunk_without_buffering(self) -> None:
        runtime = FakeRuntime()
        runtime.load()
        messages = asyncio.run(
            request(
                create_app(runtime),
                "POST",
                "/v1/completions",
                {"prompt": "abc", "max_tokens": 3, "stream": True},
            )
        )
        start, body = response(messages)
        chunks = [message for message in messages if message["type"] == "http.response.body"]
        headers = dict(start["headers"])

        self.assertEqual(start["status"], 200)
        self.assertEqual(headers[b"x-accel-buffering"], b"no")
        self.assertEqual(len(chunks), 5)
        self.assertTrue(all(chunk.get("more_body") for chunk in chunks[:-1]))
        self.assertIn(b'data: {"id":', body)
        self.assertTrue(body.endswith(b"data: [DONE]\n\n"))

    def test_chat_completion_and_metrics(self) -> None:
        runtime = FakeRuntime()
        runtime.load()
        app = create_app(runtime)
        _, chat_body = response(
            asyncio.run(
                request(
                    app,
                    "POST",
                    "/v1/chat/completions",
                    {"messages": [{"role": "user", "content": "hello"}], "max_tokens": 2},
                )
            )
        )
        start, metrics_body = response(asyncio.run(request(app, "GET", "/metrics")))

        self.assertEqual(json.loads(chat_body)["object"], "chat.completion")
        self.assertEqual(start["status"], 200)
        self.assertIn(b"inferops_runtime_requests_total 1", metrics_body)
        self.assertIn(b"inferops_runtime_inflight_requests 0", metrics_body)

    def test_invalid_request_and_drain_return_errors(self) -> None:
        runtime = FakeRuntime()
        runtime.load()
        app = create_app(runtime)
        start, body = response(
            asyncio.run(request(app, "POST", "/v1/completions", {"prompt": ""}))
        )
        self.assertEqual(start["status"], 400)
        self.assertIn("prompt", json.loads(body)["error"]["message"])

        runtime.start_drain()
        start, _ = response(
            asyncio.run(request(app, "POST", "/v1/completions", {"prompt": "hello"}))
        )
        self.assertEqual(start["status"], 503)

    def test_fake_mode_and_environment_validation(self) -> None:
        with mock.patch.dict(os.environ, {"FAKE_MODE": "true"}, clear=True):
            runtime = make_runtime_from_env(load=False)
        self.assertIsInstance(runtime, FakeRuntime)
        self.assertFalse(runtime.readiness())

        with mock.patch.dict(os.environ, {"FAKE_MODE": "sometimes"}, clear=True):
            with self.assertRaisesRegex(ValueError, "boolean"):
                make_runtime_from_env()

    def test_duration_parser(self) -> None:
        self.assertEqual(parse_duration("5m"), 300)
        self.assertEqual(parse_duration("0.5s"), 0.5)
        with self.assertRaises(ValueError):
            parse_duration("forever")


if __name__ == "__main__":
    unittest.main()
