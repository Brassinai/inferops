"""Small OpenAI-compatible FastAPI runtime used for contract validation."""

from __future__ import annotations

import asyncio
from contextlib import asynccontextmanager
import json
import os
from pathlib import Path
import threading
import time
import uuid
from collections.abc import AsyncIterator

from fastapi import Body, FastAPI, HTTPException, Request, Response
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse, StreamingResponse


MODEL_NAME = os.getenv("MODEL_REPO", "custom-fastapi")
MODEL_PATH = Path(os.getenv("MODEL_PATH", "/models/model"))


class RuntimeState:
    def __init__(self) -> None:
        self.ready = False
        self._lock = threading.Lock()
        self._requests = {"chat_completions": 0, "completions": 0}

    def increment(self, endpoint: str) -> None:
        with self._lock:
            self._requests[endpoint] += 1

    def metrics(self) -> str:
        with self._lock:
            requests = dict(self._requests)
        lines = [
            "# HELP custom_runtime_ready Whether the runtime can accept requests.",
            "# TYPE custom_runtime_ready gauge",
            f"custom_runtime_ready {1 if self.ready else 0}",
            "# HELP custom_runtime_requests_total OpenAI requests by endpoint.",
            "# TYPE custom_runtime_requests_total counter",
        ]
        lines.extend(
            f'custom_runtime_requests_total{{endpoint="{endpoint}"}} {count}'
            for endpoint, count in sorted(requests.items())
        )
        return "\n".join(lines) + "\n"


state = RuntimeState()


@asynccontextmanager
async def lifespan(_: FastAPI) -> AsyncIterator[None]:
    if not MODEL_PATH.is_dir():
        raise RuntimeError(f"MODEL_PATH is not an accessible directory: {MODEL_PATH}")
    state.ready = True
    try:
        yield
    finally:
        state.ready = False


app = FastAPI(title="InferOps custom runtime", lifespan=lifespan)


def openai_error(status: int, message: str, error_type: str, code: str) -> JSONResponse:
    return JSONResponse(
        status_code=status,
        content={
            "error": {
                "message": message,
                "type": error_type,
                "code": code,
            }
        },
    )


@app.exception_handler(HTTPException)
async def http_exception_handler(_: Request, exc: HTTPException) -> JSONResponse:
    message = exc.detail if isinstance(exc.detail, str) else str(exc.detail)
    code = "model_not_found" if exc.status_code == 404 else "invalid_request"
    return openai_error(exc.status_code, message, "invalid_request_error", code)


@app.exception_handler(RequestValidationError)
async def validation_exception_handler(
    _: Request,
    exc: RequestValidationError,
) -> JSONResponse:
    return openai_error(
        422,
        str(exc),
        "invalid_request_error",
        "invalid_request",
    )


@app.get("/health")
async def health() -> dict[str, str]:
    return {"status": "ok"}


@app.get("/ready")
async def ready() -> dict[str, str]:
    if not state.ready:
        raise HTTPException(status_code=503, detail="runtime is not ready")
    return {"status": "ready"}


@app.get("/metrics")
async def metrics() -> Response:
    return Response(
        content=state.metrics(),
        media_type="text/plain; version=0.0.4",
    )


@app.get("/v1/models")
async def models() -> dict:
    return {
        "object": "list",
        "data": [
            {
                "id": MODEL_NAME,
                "object": "model",
                "created": 0,
                "owned_by": "inferops",
            }
        ],
    }


def validate_model(payload: dict) -> None:
    requested = payload.get("model")
    if requested != MODEL_NAME:
        raise HTTPException(
            status_code=404,
            detail=f"model {requested!r} is not served",
        )


def completion_id(prefix: str) -> str:
    return f"{prefix}-{uuid.uuid4().hex}"


@app.post("/v1/chat/completions", response_model=None)
async def chat_completions(payload: dict = Body(...)) -> Response | dict:
    validate_model(payload)
    state.increment("chat_completions")
    identifier = completion_id("chatcmpl")
    created = int(time.time())
    content = "InferOps custom runtime is ready."
    if payload.get("stream"):

        async def events() -> AsyncIterator[bytes]:
            chunks = [
                {
                    "id": identifier,
                    "object": "chat.completion.chunk",
                    "created": created,
                    "model": MODEL_NAME,
                    "choices": [
                        {
                            "index": 0,
                            "delta": {"role": "assistant", "content": content},
                            "finish_reason": None,
                        }
                    ],
                },
                {
                    "id": identifier,
                    "object": "chat.completion.chunk",
                    "created": created,
                    "model": MODEL_NAME,
                    "choices": [
                        {"index": 0, "delta": {}, "finish_reason": "stop"}
                    ],
                },
            ]
            for index, chunk in enumerate(chunks):
                yield f"data: {json.dumps(chunk)}\n\n".encode()
                if index == 0:
                    await asyncio.sleep(0.05)
            yield b"data: [DONE]\n\n"

        return StreamingResponse(events(), media_type="text/event-stream")

    return {
        "id": identifier,
        "object": "chat.completion",
        "created": created,
        "model": MODEL_NAME,
        "choices": [
            {
                "index": 0,
                "message": {"role": "assistant", "content": content},
                "finish_reason": "stop",
            }
        ],
        "usage": {"prompt_tokens": 1, "completion_tokens": 6, "total_tokens": 7},
    }


@app.post("/v1/completions")
async def completions(payload: dict = Body(...)) -> dict:
    validate_model(payload)
    state.increment("completions")
    return {
        "id": completion_id("cmpl"),
        "object": "text_completion",
        "created": int(time.time()),
        "model": MODEL_NAME,
        "choices": [
            {
                "index": 0,
                "text": " ready for conformance testing.",
                "finish_reason": "stop",
            }
        ],
        "usage": {"prompt_tokens": 2, "completion_tokens": 5, "total_tokens": 7},
    }
