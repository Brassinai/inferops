"""HTTP client helpers for the built-in inference API."""

from __future__ import annotations

from collections.abc import Iterator, Mapping
from dataclasses import dataclass
import json
from typing import Any, Protocol
import urllib.error
import urllib.request


DEFAULT_TIMEOUT_SECONDS = 30.0


class ClientError(RuntimeError):
    """Raised when the InferOps client cannot complete one request."""


@dataclass(frozen=True, slots=True)
class APIRequest:
    """One outbound HTTP request."""

    method: str
    url: str
    headers: Mapping[str, str]
    json_body: Any = None
    stream: bool = False


@dataclass(frozen=True, slots=True)
class APIResponse:
    """One buffered HTTP response."""

    status: int
    headers: Mapping[str, str]
    body: bytes


class StreamingAPIResponse(Protocol):
    """Streaming HTTP response boundary."""

    status: int
    headers: Mapping[str, str]

    def iter_lines(self) -> Iterator[bytes]:
        """Yield raw response lines."""

    def close(self) -> None:
        """Close the response stream."""


class Transport(Protocol):
    """Pluggable HTTP transport for tests and real requests."""

    def send(self, request: APIRequest) -> APIResponse | StreamingAPIResponse:
        """Send one HTTP request."""


class Client:
    """Small HTTP client for the InferOps built-in inference API."""

    def __init__(
        self,
        *,
        base_url: str,
        api_key: str | None = None,
        timeout: float = DEFAULT_TIMEOUT_SECONDS,
        transport: Transport | None = None,
    ) -> None:
        normalized_base_url = base_url.strip().rstrip("/")
        if not normalized_base_url:
            raise ValueError("base_url is required")
        self._base_url = normalized_base_url
        self._api_key = api_key
        self._transport = transport if transport is not None else _UrllibTransport(timeout=timeout)
        self.responses = ResponsesAPI(self)
        self.chat = ChatAPI(self)

    def post_json(self, path: str, payload: Mapping[str, Any]) -> Any:
        """Send one JSON request and decode the JSON response."""
        response = self._transport.send(self._build_request(path=path, payload=payload, stream=False))
        if not isinstance(response, APIResponse):
            response.close()
            raise ClientError("transport returned a streaming response for a buffered request")
        _raise_for_status(response.status, response.body)
        return _decode_json_body(response.body)

    def post_stream(self, path: str, payload: Mapping[str, Any]) -> "SSEStream":
        """Send one streaming request and decode the SSE response."""
        response = self._transport.send(self._build_request(path=path, payload=payload, stream=True))
        if isinstance(response, APIResponse):
            _raise_for_status(response.status, response.body)
            raise ClientError("transport returned a buffered response for a streaming request")
        _raise_for_status(response.status, b"")
        return SSEStream(response)

    def _build_request(self, *, path: str, payload: Mapping[str, Any], stream: bool) -> APIRequest:
        headers = {
            "Accept": "text/event-stream" if stream else "application/json",
            "Content-Type": "application/json",
            "User-Agent": "inferops-python-sdk/0.0.0",
        }
        if self._api_key:
            headers["Authorization"] = f"Bearer {self._api_key}"
        return APIRequest(
            method="POST",
            url=_join_url(self._base_url, path),
            headers=headers,
            json_body=payload,
            stream=stream,
        )

    def _versioned_path(self, relative_path: str) -> str:
        if self._base_url.endswith("/v1"):
            return relative_path
        return f"/v1{relative_path}"


class ResponsesAPI:
    """InferOps model-lane client methods for responses-style calls."""

    def __init__(self, client: Client) -> None:
        self._client = client

    def create(self, *, model: str, input: Any, **params: Any) -> Any:
        """Create one non-streaming response."""
        payload = {"model": model, "input": input}
        payload.update(params)
        return self._client.post_json(self._client._versioned_path("/responses"), payload)

    def stream(self, *, model: str, input: Any, **params: Any) -> "SSEStream":
        """Create one streaming response."""
        payload = {"model": model, "input": input, "stream": True}
        payload.update(params)
        return self._client.post_stream(self._client._versioned_path("/responses"), payload)


class ChatAPI:
    """Namespace for OpenAI-compatible chat completions."""

    def __init__(self, client: Client) -> None:
        self.completions = ChatCompletionsAPI(client)


class ChatCompletionsAPI:
    """OpenAI-compatible chat completions client methods."""

    def __init__(self, client: Client) -> None:
        self._client = client

    def create(self, *, model: str, messages: list[dict[str, Any]], stream: bool = False, **params: Any) -> Any:
        """Create one chat completion request."""
        payload = {"model": model, "messages": messages}
        payload.update(params)
        if stream:
            payload["stream"] = True
            return self._client.post_stream(self._client._versioned_path("/chat/completions"), payload)
        return self._client.post_json(self._client._versioned_path("/chat/completions"), payload)


class SSEStream:
    """Iterator that parses server-sent events into Python values."""

    def __init__(self, response: StreamingAPIResponse) -> None:
        self._response = response
        self._lines = iter(response.iter_lines())
        self._closed = False

    def __iter__(self) -> "SSEStream":
        return self

    def __next__(self) -> Any:
        event_type = None
        data_lines: list[str] = []

        for raw_line in self._lines:
            line = raw_line.decode("utf-8").rstrip("\r\n")
            if not line:
                if data_lines:
                    return self._build_event(event_type, data_lines)
                continue
            if line.startswith(":"):
                continue
            field, _, raw_value = line.partition(":")
            value = raw_value.lstrip(" ")
            if field == "event":
                event_type = value
                continue
            if field == "data":
                data_lines.append(value)

        if data_lines:
            return self._build_event(event_type, data_lines)

        self.close()
        raise StopIteration

    def close(self) -> None:
        """Close the underlying streaming response."""
        if not self._closed:
            self._response.close()
            self._closed = True

    def _build_event(self, event_type: str | None, data_lines: list[str]) -> Any:
        data = "\n".join(data_lines)
        if data == "[DONE]":
            self.close()
            raise StopIteration
        payload = _decode_event_payload(data)
        if event_type is None:
            return payload
        return {"event": event_type, "data": payload}


class _UrllibTransport:
    """Default urllib-based transport."""

    def __init__(self, *, timeout: float) -> None:
        self._timeout = timeout

    def send(self, request: APIRequest) -> APIResponse | StreamingAPIResponse:
        encoded_body = None
        if request.json_body is not None:
            encoded_body = json.dumps(request.json_body).encode("utf-8")
        native_request = urllib.request.Request(
            request.url,
            data=encoded_body,
            headers=dict(request.headers),
            method=request.method,
        )
        try:
            response = urllib.request.urlopen(native_request, timeout=self._timeout)
        except urllib.error.HTTPError as exc:
            raise ClientError(f"request failed with status {exc.code}: {_decode_error_body(exc.read())}") from exc
        except urllib.error.URLError as exc:
            raise ClientError(f"request failed: {exc.reason}") from exc

        if request.stream:
            return _UrllibStreamingResponse(response)
        return APIResponse(status=response.status, headers=dict(response.headers.items()), body=response.read())


class _UrllibStreamingResponse:
    """Streaming response adapter for urllib."""

    def __init__(self, response: Any) -> None:
        self._response = response
        self.status = response.status
        self.headers = dict(response.headers.items())

    def iter_lines(self) -> Iterator[bytes]:
        while True:
            line = self._response.readline()
            if not line:
                break
            yield line

    def close(self) -> None:
        self._response.close()


def _join_url(base_url: str, path: str) -> str:
    normalized_path = path if path.startswith("/") else f"/{path}"
    return f"{base_url}{normalized_path}"


def _decode_json_body(body: bytes) -> Any:
    if not body:
        return None
    try:
        return json.loads(body.decode("utf-8"))
    except json.JSONDecodeError as exc:
        raise ClientError("response body was not valid JSON") from exc


def _decode_event_payload(payload: str) -> Any:
    try:
        return json.loads(payload)
    except json.JSONDecodeError:
        return payload


def _raise_for_status(status: int, body: bytes) -> None:
    if status < 400:
        return
    raise ClientError(f"request failed with status {status}: {_decode_error_body(body)}")


def _decode_error_body(body: bytes) -> str:
    if not body:
        return "no response body"
    try:
        payload = json.loads(body.decode("utf-8"))
    except json.JSONDecodeError:
        return body.decode("utf-8", errors="replace")
    if isinstance(payload, dict):
        message = payload.get("error") or payload.get("message")
        if isinstance(message, str) and message.strip():
            return message
    return json.dumps(payload, sort_keys=True)
