"""HTTP server for SDK web endpoints declared with ``@inferops.web_endpoint``."""

from __future__ import annotations

import asyncio
from collections.abc import Callable
import hashlib
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib.util
import json
from pathlib import Path
import sys
from typing import Any
from urllib.parse import urlsplit

from .app import App
from .endpoints import invoke_web_endpoint
from .gateway_runtime import GatewayRuntime
from .spec import ModelConfig


RuntimeFactory = Callable[[ModelConfig], Any]
MAX_REQUEST_BODY_BYTES = 1_048_576


def load_app(app_path: str) -> App:
    """Load one InferOps app from a Python file."""
    path = Path(app_path).expanduser().resolve()
    if not path.exists():
        raise FileNotFoundError(f"application file does not exist: {path}")
    if not path.is_file():
        raise ValueError(f"application path must be a file: {path}")

    module_name = f"inferops_endpoint_app_{hashlib.sha256(str(path).encode('utf-8')).hexdigest()[:12]}"
    spec = importlib.util.spec_from_file_location(module_name, path)
    if spec is None or spec.loader is None:
        raise ValueError(f"could not load Python module from {path}")

    module = importlib.util.module_from_spec(spec)
    sys.path.insert(0, str(path.parent))
    try:
        spec.loader.exec_module(module)
    finally:
        sys.path.pop(0)

    explicit_app = getattr(module, "app", None)
    if isinstance(explicit_app, App):
        return explicit_app

    discovered = [value for value in module.__dict__.values() if isinstance(value, App)]
    if not discovered:
        raise ValueError("no inferops.App instance found; define one as `app = inferops.App(...)`")
    if len(discovered) > 1:
        raise ValueError("multiple inferops.App instances found; export the intended one as `app`")
    return discovered[0]


class EndpointApplication:
    """Dispatch table for SDK web endpoints."""

    def __init__(self, app: App, *, runtime_factory: RuntimeFactory) -> None:
        self._routes: dict[tuple[str, str], tuple[Any, str]] = {}
        for model in app.models:
            if model.model_class is None:
                continue
            instance = model.model_class().bind_runtime(runtime_factory(model))
            for endpoint in model.endpoints:
                key = (endpoint.method, endpoint.path)
                if key in self._routes:
                    raise ValueError(f"duplicate SDK endpoint route: {endpoint.method} {endpoint.path}")
                self._routes[key] = (instance, endpoint.name)
        if not self._routes:
            raise ValueError("app does not declare any @inferops.web_endpoint handlers")

    @property
    def routes(self) -> tuple[tuple[str, str], ...]:
        """Return registered routes sorted for display."""
        return tuple(sorted(self._routes))

    async def handle(self, *, method: str, path: str, body: bytes) -> tuple[int, str, Any]:
        """Invoke one route and return status, media type, and payload."""
        route = self._routes.get((method.upper(), path))
        if route is None:
            allowed = sorted(route_method for route_method, route_path in self._routes if route_path == path)
            if allowed:
                return HTTPStatus.METHOD_NOT_ALLOWED, "application/json", {
                    "error": {
                        "message": f"method {method} is not allowed for {path}",
                        "type": "invalid_request_error",
                        "code": "method_not_allowed",
                    }
                }
            return HTTPStatus.NOT_FOUND, "application/json", {
                "error": {
                    "message": f"endpoint route not found: {path}",
                    "type": "invalid_request_error",
                    "code": "endpoint_not_found",
                }
            }
        try:
            request = _decode_json_body(body)
        except ValueError as exc:
            return HTTPStatus.BAD_REQUEST, "application/json", {
                "error": {
                    "message": str(exc),
                    "type": "invalid_request_error",
                    "code": "invalid_json",
                }
            }
        model, endpoint_name = route
        invocation = await invoke_web_endpoint(model, endpoint_name, request)
        if invocation.streaming:
            return HTTPStatus.OK, "text/event-stream", invocation.stream
        return HTTPStatus.OK, "application/json", invocation.response


def make_handler(endpoint_app: EndpointApplication) -> type[BaseHTTPRequestHandler]:
    """Build a request handler class bound to one endpoint application."""

    class EndpointRequestHandler(BaseHTTPRequestHandler):
        server_version = "InferOpsEndpointServer/0.1"

        def do_POST(self) -> None:  # noqa: N802 - stdlib handler API
            self._handle_method("POST")

        def do_GET(self) -> None:  # noqa: N802 - stdlib handler API
            if self.path == "/health":
                self._write_json(HTTPStatus.OK, {"status": "ok"})
                return
            self._handle_method("GET")

        def log_message(self, format: str, *args: Any) -> None:
            return

        def _handle_method(self, method: str) -> None:
            try:
                try:
                    length = int(self.headers.get("Content-Length", "0"))
                except ValueError:
                    self._write_json(
                        HTTPStatus.BAD_REQUEST,
                        {
                            "error": {
                                "message": "Content-Length must be an integer",
                                "type": "invalid_request_error",
                                "code": "invalid_content_length",
                            }
                        },
                    )
                    return
                if length > MAX_REQUEST_BODY_BYTES:
                    self._write_json(
                        HTTPStatus.REQUEST_ENTITY_TOO_LARGE,
                        {
                            "error": {
                                "message": "request body is too large",
                                "type": "invalid_request_error",
                                "code": "request_body_too_large",
                            }
                        },
                    )
                    return
                body = self.rfile.read(length) if length > 0 else b"{}"
                path = urlsplit(self.path).path
                status, media_type, payload = asyncio.run(
                    endpoint_app.handle(method=method, path=path, body=body)
                )
                if media_type == "text/event-stream":
                    self._write_stream(status, payload)
                    return
                self._write_json(status, payload)
            except Exception as exc:
                self._write_json(
                    HTTPStatus.INTERNAL_SERVER_ERROR,
                    {
                        "error": {
                            "message": str(exc),
                            "type": "api_error",
                            "code": "endpoint_error",
                        }
                    },
                )

        def _write_json(self, status: int, payload: Any) -> None:
            body = json.dumps(payload).encode("utf-8")
            self.send_response(int(status))
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def _write_stream(self, status: int, stream: Any) -> None:
            self.send_response(int(status))
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Cache-Control", "no-cache")
            self.send_header("X-Accel-Buffering", "no")
            self.end_headers()

            async def write_events() -> None:
                async for item in stream:
                    self.wfile.write(f"data: {json.dumps(item)}\n\n".encode("utf-8"))
                    self.wfile.flush()
                self.wfile.write(b"data: [DONE]\n\n")
                self.wfile.flush()

            asyncio.run(write_events())

    return EndpointRequestHandler


def serve(
    app_path: str,
    *,
    gateway_url: str = "http://127.0.0.1:8080",
    host: str = "127.0.0.1",
    port: int = 9000,
    api_key: str | None = None,
) -> None:
    """Serve SDK endpoints from an app file until interrupted."""
    app = load_app(app_path)
    endpoint_app = EndpointApplication(
        app,
        runtime_factory=lambda _model: GatewayRuntime(gateway_url=gateway_url, api_key=api_key),
    )
    handler = make_handler(endpoint_app)
    server = ThreadingHTTPServer((host, port), handler)
    try:
        server.serve_forever()
    finally:
        server.server_close()


def _decode_json_body(body: bytes) -> Any:
    try:
        return json.loads(body.decode("utf-8") if body else "{}")
    except json.JSONDecodeError as exc:
        raise ValueError(f"request body must be valid JSON: {exc}") from exc
