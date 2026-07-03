#!/usr/bin/env python3
"""Validate an InferOps runtime's externally observable HTTP contract."""

from __future__ import annotations

import argparse
import json
import re
import sys
import time
import urllib.error
import urllib.request
from dataclasses import dataclass
from typing import Any


PROMETHEUS_SAMPLE = re.compile(
    rb"(?m)^[a-zA-Z_:][a-zA-Z0-9_:]*(?:\{[^}\r\n]*\})?\s+"
    rb"(?:[-+]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][-+]?\d+)?|NaN|[+-]Inf)"
    rb"(?:\s+\d+)?\s*$"
)
MINIMUM_STREAM_GAP_SECONDS = 0.02


class ConformanceError(RuntimeError):
    """Raised when a runtime violates the shared contract."""


@dataclass(frozen=True)
class RuntimeContract:
    base_url: str
    model: str
    health_path: str = "/health"
    readiness_path: str = "/health"
    metrics_path: str = "/metrics"
    timeout: float = 10.0

    def url(self, path: str) -> str:
        return self.base_url.rstrip("/") + "/" + path.lstrip("/")


def request(
    contract: RuntimeContract,
    method: str,
    path: str,
    payload: dict | None = None,
) -> tuple[int, dict[str, str], bytes]:
    http_request = build_request(contract, method, path, payload)
    try:
        with urllib.request.urlopen(http_request, timeout=contract.timeout) as response:
            return response.status, response_headers(response), response.read()
    except urllib.error.HTTPError as exc:
        detail = exc.read(1024).decode("utf-8", errors="replace")
        raise ConformanceError(
            f"{method} {path} returned HTTP {exc.code}: {detail}"
        ) from exc
    except (urllib.error.URLError, TimeoutError) as exc:
        reason = getattr(exc, "reason", exc)
        raise ConformanceError(f"{method} {path} failed: {reason}") from exc


def build_request(
    contract: RuntimeContract,
    method: str,
    path: str,
    payload: dict | None = None,
) -> urllib.request.Request:
    body = None
    headers = {"Accept": "application/json"}
    if payload is not None:
        body = json.dumps(payload).encode("utf-8")
        headers["Content-Type"] = "application/json"
    return urllib.request.Request(
        contract.url(path),
        data=body,
        headers=headers,
        method=method,
    )


def response_headers(response: Any) -> dict[str, str]:
    return {
        key.lower(): value
        for key, value in response.headers.items()
    }


def require_success(contract: RuntimeContract, path: str, name: str) -> bytes:
    status, _, body = request(contract, "GET", path)
    if not 200 <= status < 300:
        raise ConformanceError(f"{name} returned HTTP {status}")
    return body


def decode_object(body: bytes, name: str) -> dict:
    try:
        value = json.loads(body)
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ConformanceError(f"{name} did not return valid JSON") from exc
    if not isinstance(value, dict):
        raise ConformanceError(f"{name} response must be a JSON object")
    return value


def validate_health(contract: RuntimeContract) -> None:
    require_success(contract, contract.health_path, "health endpoint")
    require_success(contract, contract.readiness_path, "readiness endpoint")


def validate_metrics(contract: RuntimeContract) -> None:
    status, headers, body = request(contract, "GET", contract.metrics_path)
    if not 200 <= status < 300:
        raise ConformanceError(f"metrics endpoint returned HTTP {status}")
    content_type = headers.get("content-type", "").lower()
    if not (
        content_type.startswith("text/plain")
        or content_type.startswith("application/openmetrics-text")
    ):
        raise ConformanceError(
            f"metrics endpoint Content-Type {content_type!r} is not Prometheus text"
        )
    if not PROMETHEUS_SAMPLE.search(body):
        raise ConformanceError("metrics endpoint contains no Prometheus sample")


def validate_models(contract: RuntimeContract) -> None:
    _, _, body = request(contract, "GET", "/v1/models")
    response = decode_object(body, "models endpoint")
    models = response.get("data")
    if not isinstance(models, list) or not models:
        raise ConformanceError("models endpoint must return a non-empty data array")
    if not any(
        isinstance(model, dict) and model.get("id") == contract.model
        for model in models
    ):
        raise ConformanceError(
            f"models endpoint does not advertise requested model {contract.model!r}"
        )


def validate_chat_completion(contract: RuntimeContract) -> None:
    payload = {
        "model": contract.model,
        "messages": [{"role": "user", "content": "Reply with one short sentence."}],
        "max_tokens": 16,
    }
    _, _, body = request(contract, "POST", "/v1/chat/completions", payload)
    response = decode_object(body, "chat completions endpoint")
    if not isinstance(response.get("choices"), list) or not response["choices"]:
        raise ConformanceError("chat completion must contain a non-empty choices array")

    payload["stream"] = True
    http_request = build_request(
        contract,
        "POST",
        "/v1/chat/completions",
        payload,
    )
    try:
        with urllib.request.urlopen(http_request, timeout=contract.timeout) as response:
            headers = response_headers(response)
            if response.status != 200:
                raise ConformanceError(
                    f"streaming chat completion returned HTTP {response.status}"
                )
            if not headers.get("content-type", "").lower().startswith(
                "text/event-stream"
            ):
                raise ConformanceError(
                    "streaming chat completion must use text/event-stream"
                )
            events: list[bytes] = []
            first_event_at: float | None = None
            for line in response:
                if not line.startswith(b"data:"):
                    continue
                if first_event_at is None:
                    first_event_at = time.monotonic()
                events.append(line.strip())
            completed_at = time.monotonic()
    except urllib.error.HTTPError as exc:
        detail = exc.read(1024).decode("utf-8", errors="replace")
        raise ConformanceError(
            f"streaming chat completion returned HTTP {exc.code}: {detail}"
        ) from exc
    except (urllib.error.URLError, TimeoutError) as exc:
        reason = getattr(exc, "reason", exc)
        raise ConformanceError(f"streaming chat completion failed: {reason}") from exc

    if first_event_at is None or len(events) < 2 or b"data: [DONE]" not in events:
        raise ConformanceError(
            "streaming chat completion is missing incremental SSE data or [DONE]"
        )
    if completed_at - first_event_at < MINIMUM_STREAM_GAP_SECONDS:
        raise ConformanceError(
            "streaming chat completion appears buffered instead of incremental"
        )


def validate_completion(contract: RuntimeContract) -> None:
    payload = {
        "model": contract.model,
        "prompt": "InferOps is",
        "max_tokens": 16,
    }
    _, _, body = request(contract, "POST", "/v1/completions", payload)
    response = decode_object(body, "completions endpoint")
    if not isinstance(response.get("choices"), list) or not response["choices"]:
        raise ConformanceError("completion must contain a non-empty choices array")


def validate_error_contract(contract: RuntimeContract) -> None:
    payload = {
        "model": f"{contract.model}-missing",
        "messages": [{"role": "user", "content": "hello"}],
        "max_tokens": 1,
    }
    http_request = build_request(
        contract,
        "POST",
        "/v1/chat/completions",
        payload,
    )
    try:
        with urllib.request.urlopen(http_request, timeout=contract.timeout) as response:
            body = response.read()
            raise ConformanceError(
                "unknown model request unexpectedly succeeded: "
                f"HTTP {response.status}, body={body[:200]!r}"
            )
    except urllib.error.HTTPError as exc:
        if not 400 <= exc.code < 500:
            raise ConformanceError(
                f"unknown model returned HTTP {exc.code}, expected 4xx"
            ) from exc
        response = decode_object(exc.read(), "OpenAI error response")
    except (urllib.error.URLError, TimeoutError) as exc:
        reason = getattr(exc, "reason", exc)
        raise ConformanceError(f"unknown model request failed: {reason}") from exc

    error = response.get("error")
    if not isinstance(error, dict):
        raise ConformanceError("error response must contain an error object")
    for field in ("message", "type"):
        if not isinstance(error.get(field), str) or not error[field]:
            raise ConformanceError(
                f"error response must contain a non-empty error.{field}"
            )
    code = error.get("code")
    if not isinstance(code, (str, int)) or code == "":
        raise ConformanceError(
            "error response must contain a non-empty string or integer error.code"
        )


def validate_runtime(contract: RuntimeContract) -> None:
    validate_health(contract)
    validate_metrics(contract)
    validate_models(contract)
    validate_chat_completion(contract)
    validate_completion(contract)
    validate_error_contract(contract)


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--model", required=True)
    parser.add_argument("--health-path", default="/health")
    parser.add_argument("--readiness-path", default="/health")
    parser.add_argument("--metrics-path", default="/metrics")
    parser.add_argument("--timeout", type=float, default=10.0)
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(sys.argv[1:] if argv is None else argv)
    if args.timeout <= 0:
        raise SystemExit("error: --timeout must be positive")
    contract = RuntimeContract(
        base_url=args.base_url,
        model=args.model,
        health_path=args.health_path,
        readiness_path=args.readiness_path,
        metrics_path=args.metrics_path,
        timeout=args.timeout,
    )
    try:
        validate_runtime(contract)
    except ConformanceError as exc:
        print(f"runtime conformance failed: {exc}", file=sys.stderr)
        return 1
    print("runtime conformance passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
