"""OpenAI-compatible ASGI server for InferOps nano-vLLM runtimes."""

from __future__ import annotations

import argparse
import asyncio
import json
import math
import os
import threading
import time
import uuid
from collections.abc import AsyncIterator, Iterator
from dataclasses import dataclass
from typing import Any

MAX_REQUEST_BYTES = 1024 * 1024


class RequestError(ValueError):
    """An error that can be safely returned to an API client."""

    def __init__(self, message: str, status: int = 400, error_type: str = "invalid_request_error"):
        super().__init__(message)
        self.status = status
        self.error_type = error_type


@dataclass
class GenerationRequest:
    prompt: str
    model: str
    max_tokens: int
    temperature: float
    stream: bool


class Metrics:
    """Small dependency-free runtime metrics collector."""

    def __init__(self):
        self._lock = threading.Lock()
        self.requests = 0
        self.errors = 0
        self.generated_chunks = 0

    def record_request(self) -> None:
        with self._lock:
            self.requests += 1

    def record_error(self) -> None:
        with self._lock:
            self.errors += 1

    def record_chunk(self) -> None:
        with self._lock:
            self.generated_chunks += 1

    def render(self, runtime: Any) -> bytes:
        with self._lock:
            requests = self.requests
            errors = self.errors
            chunks = self.generated_chunks
        ready = 1 if runtime.readiness() else 0
        return (
            "# HELP inferops_runtime_ready Whether the runtime accepts requests.\n"
            "# TYPE inferops_runtime_ready gauge\n"
            f"inferops_runtime_ready {ready}\n"
            "# HELP inferops_runtime_inflight_requests Active generation requests.\n"
            "# TYPE inferops_runtime_inflight_requests gauge\n"
            f"inferops_runtime_inflight_requests {runtime.inflight}\n"
            "# HELP inferops_runtime_requests_total Generation requests received.\n"
            "# TYPE inferops_runtime_requests_total counter\n"
            f"inferops_runtime_requests_total {requests}\n"
            "# HELP inferops_runtime_request_errors_total Generation request errors.\n"
            "# TYPE inferops_runtime_request_errors_total counter\n"
            f"inferops_runtime_request_errors_total {errors}\n"
            "# HELP inferops_runtime_generated_chunks_total Stream chunks emitted.\n"
            "# TYPE inferops_runtime_generated_chunks_total counter\n"
            f"inferops_runtime_generated_chunks_total {chunks}\n"
        ).encode()


def create_app(runtime: Any):
    """Create a dependency-free ASGI application for a runtime backend."""
    metrics = Metrics()
    model_name = os.environ.get("MODEL_REPO") or os.environ.get("MODEL_PATH") or "inferops-model"

    async def app(scope, receive, send):
        if scope["type"] != "http":
            return

        method = scope.get("method", "GET").upper()
        path = scope.get("path", "/")

        if path == "/health":
            if method != "GET":
                await _method_not_allowed(send, "GET")
                return
            healthy = runtime.health()
            await _json(send, 200 if healthy else 503, {"status": "ok" if healthy else "failed"})
            return

        if path == "/readiness":
            if method != "GET":
                await _method_not_allowed(send, "GET")
                return
            ready = runtime.readiness()
            await _json(send, 200 if ready else 503, {"ready": ready})
            return

        if path == "/metrics":
            if method != "GET":
                await _method_not_allowed(send, "GET")
                return
            await _response(send, 200, metrics.render(runtime), b"text/plain; version=0.0.4; charset=utf-8")
            return

        if path == "/v1/models":
            if method != "GET":
                await _method_not_allowed(send, "GET")
                return
            await _json(send, 200, {"object": "list", "data": [{"id": model_name, "object": "model"}]})
            return

        if path not in ("/v1/completions", "/v1/chat/completions"):
            await _error(send, RequestError("Not found", 404, "not_found"))
            return
        if method != "POST":
            await _method_not_allowed(send, "POST")
            return
        if not runtime.readiness():
            await _error(send, RequestError("Runtime is not ready to accept requests", 503, "service_unavailable"))
            return

        metrics.record_request()
        try:
            payload = await _read_json(receive)
            request = _parse_generation_request(payload, path, model_name)
            if request.stream:
                await _stream_completion(send, runtime, request, path, metrics)
            else:
                await _complete(send, runtime, request, path, metrics)
        except RequestError as error:
            metrics.record_error()
            await _error(send, error)
        except Exception:
            metrics.record_error()
            await _error(send, RequestError("Generation failed", 500, "server_error"))

    return app


async def _read_json(receive) -> dict[str, Any]:
    body = bytearray()
    more_body = True
    while more_body:
        message = await receive()
        if message["type"] == "http.disconnect":
            raise RequestError("Client disconnected", 499, "client_disconnected")
        if message["type"] != "http.request":
            continue
        body.extend(message.get("body", b""))
        if len(body) > MAX_REQUEST_BYTES:
            raise RequestError("Request body is too large", 413)
        more_body = message.get("more_body", False)
    try:
        payload = json.loads(body)
    except (json.JSONDecodeError, UnicodeDecodeError):
        raise RequestError("Request body must be valid JSON") from None
    if not isinstance(payload, dict):
        raise RequestError("Request body must be a JSON object")
    return payload


def _parse_generation_request(payload: dict[str, Any], path: str, default_model: str) -> GenerationRequest:
    if path == "/v1/chat/completions":
        prompt = _chat_prompt(payload.get("messages"))
    else:
        prompt = payload.get("prompt")
        if not isinstance(prompt, str) or not prompt:
            raise RequestError("prompt must be a non-empty string")

    max_tokens = payload.get("max_tokens", 32)
    if isinstance(max_tokens, bool) or not isinstance(max_tokens, int) or max_tokens < 1:
        raise RequestError("max_tokens must be a positive integer")

    temperature = payload.get("temperature", 0.7)
    if isinstance(temperature, bool) or not isinstance(temperature, (int, float)):
        raise RequestError("temperature must be a positive number")
    temperature = float(temperature)
    if not math.isfinite(temperature) or temperature <= 1e-10:
        raise RequestError("temperature must be a positive number")

    stream = payload.get("stream", False)
    if not isinstance(stream, bool):
        raise RequestError("stream must be a boolean")

    model = payload.get("model", default_model)
    if not isinstance(model, str) or not model:
        raise RequestError("model must be a non-empty string")
    return GenerationRequest(prompt, model, max_tokens, temperature, stream)


def _chat_prompt(messages: Any) -> str:
    if not isinstance(messages, list) or not messages:
        raise RequestError("messages must be a non-empty array")
    lines = []
    for message in messages:
        if not isinstance(message, dict):
            raise RequestError("each message must be an object")
        role = message.get("role")
        content = message.get("content")
        if role not in ("system", "user", "assistant") or not isinstance(content, str):
            raise RequestError("each message requires a valid role and string content")
        lines.append(f"{role}: {content}")
    lines.append("assistant:")
    return "\n".join(lines)


async def _complete(send, runtime: Any, request: GenerationRequest, path: str, metrics: Metrics) -> None:
    try:
        chunks = await asyncio.to_thread(
            list,
            runtime.generate_stream(request.prompt, request.max_tokens, request.temperature),
        )
    except RuntimeError as error:
        raise RequestError(str(error), 503, "service_unavailable") from error
    for _ in chunks:
        metrics.record_chunk()
    text = "".join(chunks)
    completion_id = f"cmpl-{uuid.uuid4().hex}"
    if path == "/v1/chat/completions":
        choice = {"index": 0, "message": {"role": "assistant", "content": text}, "finish_reason": "stop"}
        object_name = "chat.completion"
    else:
        choice = {"index": 0, "text": text, "finish_reason": "stop"}
        object_name = "text_completion"
    await _json(
        send,
        200,
        {
            "id": completion_id,
            "object": object_name,
            "created": int(time.time()),
            "model": request.model,
            "choices": [choice],
        },
    )


async def _stream_completion(send, runtime: Any, request: GenerationRequest, path: str, metrics: Metrics) -> None:
    completion_id = f"cmpl-{uuid.uuid4().hex}"
    created = int(time.time())
    iterator = iter(runtime.generate_stream(request.prompt, request.max_tokens, request.temperature))
    await send(
        {
            "type": "http.response.start",
            "status": 200,
            "headers": [
                (b"content-type", b"text/event-stream; charset=utf-8"),
                (b"cache-control", b"no-cache"),
                (b"x-accel-buffering", b"no"),
            ],
        }
    )
    cancelled = False
    try:
        async for text in _async_iterator(iterator):
            metrics.record_chunk()
            if path == "/v1/chat/completions":
                choice = {"index": 0, "delta": {"content": text}, "finish_reason": None}
                object_name = "chat.completion.chunk"
            else:
                choice = {"index": 0, "text": text, "finish_reason": None}
                object_name = "text_completion"
            payload = {
                "id": completion_id,
                "object": object_name,
                "created": created,
                "model": request.model,
                "choices": [choice],
            }
            await send({"type": "http.response.body", "body": _sse(payload), "more_body": True})
        if path == "/v1/chat/completions":
            choice = {"index": 0, "delta": {}, "finish_reason": "stop"}
            object_name = "chat.completion.chunk"
        else:
            choice = {"index": 0, "text": "", "finish_reason": "stop"}
            object_name = "text_completion"
        final_payload = {
            "id": completion_id,
            "object": object_name,
            "created": created,
            "model": request.model,
            "choices": [choice],
        }
        await send({"type": "http.response.body", "body": _sse(final_payload), "more_body": True})
        await send({"type": "http.response.body", "body": b"data: [DONE]\n\n", "more_body": False})
    except asyncio.CancelledError:
        # A bounded shutdown may cancel a stream after the drain timeout. Do
        # not attempt further ASGI writes or close a generator while its
        # blocking next() call may still be running in the worker thread.
        cancelled = True
    except Exception:
        metrics.record_error()
        error = {"error": {"message": "Generation failed", "type": "server_error"}}
        await send({"type": "http.response.body", "body": _sse(error), "more_body": True})
        await send({"type": "http.response.body", "body": b"data: [DONE]\n\n", "more_body": False})
    finally:
        if not cancelled:
            close = getattr(iterator, "close", None)
            if close is not None:
                await asyncio.to_thread(close)


async def _async_iterator(iterator: Iterator[str]) -> AsyncIterator[str]:
    sentinel = object()

    def next_item():
        return next(iterator, sentinel)

    while True:
        item = await asyncio.to_thread(next_item)
        if item is sentinel:
            return
        yield str(item)


def _sse(payload: dict[str, Any]) -> bytes:
    return b"data: " + json.dumps(payload, separators=(",", ":")).encode() + b"\n\n"


async def _json(send, status: int, payload: dict[str, Any]) -> None:
    await _response(send, status, json.dumps(payload, separators=(",", ":")).encode(), b"application/json")


async def _error(send, error: RequestError) -> None:
    await _json(
        send,
        error.status,
        {"error": {"message": str(error), "type": error.error_type, "code": None}},
    )


async def _method_not_allowed(send, allowed: str) -> None:
    body = json.dumps({"error": {"message": "Method not allowed", "type": "invalid_request_error"}}).encode()
    await send(
        {
            "type": "http.response.start",
            "status": 405,
            "headers": [(b"content-type", b"application/json"), (b"allow", allowed.encode())],
        }
    )
    await send({"type": "http.response.body", "body": body})


async def _response(send, status: int, body: bytes, content_type: bytes) -> None:
    await send(
        {
            "type": "http.response.start",
            "status": status,
            "headers": [(b"content-type", content_type), (b"content-length", str(len(body)).encode())],
        }
    )
    await send({"type": "http.response.body", "body": body})


def make_runtime_from_env(*, load: bool = True):
    """Create the configured fake or real runtime implementation."""
    fake_mode = _env_bool("FAKE_MODE", True)
    if fake_mode:
        from .fake_runtime import FakeRuntime

        runtime = FakeRuntime()
    else:
        from .real_runtime import RealRuntime

        model_path = os.environ.get("MODEL_PATH")
        if not model_path:
            raise ValueError("MODEL_PATH environment variable is required when FAKE_MODE=false")
        runtime = RealRuntime(
            model_path=model_path,
            tensor_parallel_size=_env_int("TENSOR_PARALLEL_SIZE", 1, minimum=1),
            enforce_eager=_env_bool("ENFORCE_EAGER", False),
            dtype=os.environ.get("MODEL_DTYPE", "float16"),
            max_num_seqs=_env_int("MAX_NUM_SEQS", 256, minimum=1),
            max_model_len=_env_int("MAX_MODEL_LEN", 8192, minimum=1),
        )
    if load:
        runtime.load()
    return runtime


def _env_bool(name: str, default: bool) -> bool:
    raw = os.environ.get(name)
    if raw is None:
        return default
    normalized = raw.strip().lower()
    if normalized in ("1", "true", "yes", "on"):
        return True
    if normalized in ("0", "false", "no", "off"):
        return False
    raise ValueError(f"{name} must be a boolean")


def _env_int(name: str, default: int, *, minimum: int) -> int:
    raw = os.environ.get(name, str(default))
    try:
        value = int(raw)
    except ValueError:
        raise ValueError(f"{name} must be an integer") from None
    if value < minimum:
        raise ValueError(f"{name} must be at least {minimum}")
    return value


def parse_duration(value: str) -> float:
    """Parse a positive duration using s, m, or h suffixes."""
    units = {"s": 1, "m": 60, "h": 3600}
    if not value or value[-1] not in units:
        raise ValueError("duration must end in s, m, or h")
    try:
        duration = float(value[:-1]) * units[value[-1]]
    except ValueError:
        raise ValueError("duration must contain a number") from None
    if not math.isfinite(duration) or duration <= 0:
        raise ValueError("duration must be positive")
    return duration


def run_server(runtime: Any, host: str, port: int, drain_timeout: float) -> None:
    """Run uvicorn and begin draining as soon as termination is requested."""
    try:
        import uvicorn
    except ImportError as error:
        raise RuntimeError("uvicorn is required to run the runtime server") from error

    class DrainServer(uvicorn.Server):
        def handle_exit(self, sig, frame):
            runtime.start_drain()
            super().handle_exit(sig, frame)

        async def shutdown(self, sockets=None):
            runtime.start_drain()
            await super().shutdown(sockets=sockets)

    config = uvicorn.Config(
        create_app(runtime),
        host=host,
        port=port,
        log_level=os.environ.get("LOG_LEVEL", "info"),
        timeout_graceful_shutdown=math.ceil(drain_timeout),
        server_header=False,
    )
    try:
        DrainServer(config).run()
    finally:
        runtime.start_drain()
        close = getattr(runtime, "close", None)
        if close is not None:
            close()


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Run the InferOps nano-vLLM runtime server")
    parser.add_argument("--host", default=os.environ.get("HOST", "0.0.0.0"))
    parser.add_argument("--port", type=int, default=_env_int("PORT", 8000, minimum=1))
    args = parser.parse_args(argv)
    if args.port > 65535:
        parser.error("port must be at most 65535")
    drain_timeout = parse_duration(os.environ.get("INFEROPS_DRAIN_TIMEOUT", "5m"))
    runtime = make_runtime_from_env()
    run_server(runtime, args.host, args.port, drain_timeout)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
