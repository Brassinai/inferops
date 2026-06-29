"""Tests for the expanded Python SDK surface."""

from __future__ import annotations

import asyncio
from collections.abc import AsyncIterator, Iterator
import json
import unittest

from inferops import App, Client, invoke_web_endpoint, web_endpoint
from inferops.client import APIResponse


class SDKClientTest(unittest.TestCase):
    def test_responses_create_posts_json_request(self) -> None:
        transport = _FakeTransport(
            responses=[
                APIResponse(
                    status=200,
                    headers={"Content-Type": "application/json"},
                    body=json.dumps({"id": "resp_123", "status": "completed"}).encode("utf-8"),
                )
            ]
        )
        client = Client(base_url="https://api.example.com", api_key="secret", transport=transport)

        response = client.responses.create(model="qwen-chat", input="hello", temperature=0.2)

        self.assertEqual(response["id"], "resp_123")
        self.assertEqual(transport.requests[0].url, "https://api.example.com/v1/responses")
        self.assertEqual(transport.requests[0].headers["Authorization"], "Bearer secret")
        self.assertEqual(
            transport.requests[0].json_body,
            {"model": "qwen-chat", "input": "hello", "temperature": 0.2},
        )

    def test_responses_stream_parses_server_sent_events(self) -> None:
        streaming_response = _FakeStreamingResponse(
            lines=[
                b'data: {"type":"response.output_text.delta","delta":"Hel"}\n',
                b"\n",
                b'event: response.completed\n',
                b'data: {"type":"response.completed"}\n',
                b"\n",
                b"data: [DONE]\n",
                b"\n",
            ]
        )
        transport = _FakeTransport(responses=[streaming_response])
        client = Client(base_url="https://api.example.com/v1", transport=transport)

        stream = client.responses.stream(model="qwen-chat", input="hello")
        events = list(stream)

        self.assertEqual(
            events[0],
            {"type": "response.output_text.delta", "delta": "Hel"},
        )
        self.assertEqual(
            events[1],
            {"event": "response.completed", "data": {"type": "response.completed"}},
        )
        self.assertTrue(streaming_response.closed)
        self.assertEqual(transport.requests[0].url, "https://api.example.com/v1/responses")
        self.assertTrue(transport.requests[0].json_body["stream"])

    def test_chat_completions_supports_openai_compatible_streaming(self) -> None:
        streaming_response = _FakeStreamingResponse(
            lines=[
                b'data: {"choices":[{"delta":{"content":"Hi"}}]}\n',
                b"\n",
                b"data: [DONE]\n",
                b"\n",
            ]
        )
        transport = _FakeTransport(responses=[streaming_response])
        client = Client(base_url="https://api.example.com", transport=transport)

        events = list(
            client.chat.completions.create(
                model="qwen-chat",
                messages=[{"role": "user", "content": "hello"}],
                stream=True,
            )
        )

        self.assertEqual(events[0]["choices"][0]["delta"]["content"], "Hi")
        self.assertEqual(transport.requests[0].url, "https://api.example.com/v1/chat/completions")
        self.assertTrue(transport.requests[0].json_body["stream"])


class SDKCustomEndpointTest(unittest.TestCase):
    def test_decorated_models_gain_runtime_helpers(self) -> None:
        app = App("support")

        @app.model(name="qwen-chat", model="repo/chat")
        class QwenChat:
            pass

        with self.assertRaisesRegex(RuntimeError, "bind_runtime"):
            asyncio.run(QwenChat().generate("hello"))

    def test_custom_endpoint_semantics_cover_response_and_streaming(self) -> None:
        app = App("support")

        @app.model(name="qwen-chat", model="repo/chat")
        class QwenChat:
            @web_endpoint(method="POST", path="/chat")
            async def chat(self, request):
                return await self.generate(request["prompt"], temperature=0.2)

            @web_endpoint(method="POST", path="/chat/stream")
            async def stream_chat(self, request):
                async for chunk in self.generate_stream(request["prompt"], temperature=0.2):
                    yield chunk

        runtime = _FakeRuntime()
        model = QwenChat().bind_runtime(runtime)

        response_invocation = asyncio.run(invoke_web_endpoint(model, "chat", {"prompt": "hello"}))
        streaming_invocation = asyncio.run(invoke_web_endpoint(model, "stream_chat", {"prompt": "hello"}))
        streamed_chunks = asyncio.run(_collect_async(streaming_invocation.stream))

        self.assertFalse(response_invocation.streaming)
        self.assertEqual(response_invocation.response, {"text": "reply:hello", "temperature": 0.2})
        self.assertTrue(streaming_invocation.streaming)
        self.assertEqual(
            streamed_chunks,
            [
                {"delta": "hello", "temperature": 0.2},
                {"done": True},
            ],
        )

        endpoint_streaming = {
            endpoint.name: endpoint.streaming for endpoint in getattr(QwenChat, "__inferops_model__").endpoints
        }
        self.assertEqual(endpoint_streaming, {"chat": False, "stream_chat": True})
        self.assertEqual(
            runtime.calls,
            [
                ("generate", "qwen-chat", "hello", {"temperature": 0.2}),
                ("generate_stream", "qwen-chat", "hello", {"temperature": 0.2}),
            ],
        )

    def test_streaming_override_supports_async_iterator_handlers(self) -> None:
        app = App("support")

        @app.model(name="qwen-chat", model="repo/chat")
        class QwenChat:
            @web_endpoint(method="POST", path="/chat/stream", streaming=True)
            async def stream_chat(self, request):
                return self.generate_stream(request["prompt"], temperature=0.4)

        runtime = _FakeRuntime()
        model = QwenChat().bind_runtime(runtime)

        invocation = asyncio.run(invoke_web_endpoint(model, "stream_chat", {"prompt": "hello"}))
        streamed_chunks = asyncio.run(_collect_async(invocation.stream))

        self.assertTrue(invocation.streaming)
        self.assertEqual(
            streamed_chunks,
            [
                {"delta": "hello", "temperature": 0.4},
                {"done": True},
            ],
        )

    def test_streaming_async_iterator_requires_explicit_contract(self) -> None:
        app = App("support")

        @app.model(name="qwen-chat", model="repo/chat")
        class QwenChat:
            @web_endpoint(method="POST", path="/chat/stream")
            async def stream_chat(self, request):
                return self.generate_stream(request["prompt"])

        runtime = _FakeRuntime()
        model = QwenChat().bind_runtime(runtime)

        with self.assertRaisesRegex(TypeError, "add streaming=True"):
            asyncio.run(invoke_web_endpoint(model, "stream_chat", {"prompt": "hello"}))

    def test_bind_runtime_rejects_incomplete_runtime_objects(self) -> None:
        app = App("support")

        @app.model(name="qwen-chat", model="repo/chat")
        class QwenChat:
            pass

        class MissingStreamRuntime:
            async def generate(self, model, request, **kwargs):
                return request

        with self.assertRaisesRegex(TypeError, "generate_stream"):
            QwenChat().bind_runtime(MissingStreamRuntime())

    def test_reserved_runtime_helper_names_are_rejected(self) -> None:
        app = App("support")

        with self.assertRaisesRegex(ValueError, "reserved helper name"):

            @app.model(name="qwen-chat", model="repo/chat")
            class QwenChat:
                async def generate(self, request):
                    return request


class _FakeTransport:
    def __init__(self, *, responses: list[APIResponse | _FakeStreamingResponse]) -> None:
        self.requests = []
        self._responses = list(responses)

    def send(self, request):
        self.requests.append(request)
        return self._responses.pop(0)


class _FakeStreamingResponse:
    def __init__(self, *, lines: list[bytes], status: int = 200) -> None:
        self.status = status
        self.headers = {"Content-Type": "text/event-stream"}
        self._lines = list(lines)
        self.closed = False

    def iter_lines(self) -> Iterator[bytes]:
        for line in self._lines:
            yield line

    def close(self) -> None:
        self.closed = True


class _FakeRuntime:
    def __init__(self) -> None:
        self.calls: list[tuple[str, str, str, dict[str, object]]] = []

    async def generate(self, model, request, **kwargs):
        self.calls.append(("generate", model.name, request, kwargs))
        return {"text": f"reply:{request}", "temperature": kwargs["temperature"]}

    def generate_stream(self, model, request, **kwargs) -> AsyncIterator[dict[str, object]]:
        self.calls.append(("generate_stream", model.name, request, kwargs))

        async def iterator() -> AsyncIterator[dict[str, object]]:
            yield {"delta": request, "temperature": kwargs["temperature"]}
            yield {"done": True}

        return iterator()


async def _collect_async(stream: AsyncIterator[object] | None) -> list[object]:
    if stream is None:
        return []
    return [item async for item in stream]


if __name__ == "__main__":
    unittest.main()
