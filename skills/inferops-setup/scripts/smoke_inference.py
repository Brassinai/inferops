#!/usr/bin/env python3
"""Run a bounded gateway inference benchmark without printing prompts or output."""

from __future__ import annotations

import argparse
import base64
import json
import os
from pathlib import Path
import re
import statistics
import struct
import sys
import time
import urllib.error
import urllib.parse
import urllib.request


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def endpoint(gateway_url: str, model: str) -> str:
    url = urllib.parse.urlsplit(gateway_url)
    if (url.scheme not in ("http", "https") or not url.hostname
            or url.username or url.password or url.query or url.fragment
            or url.path not in ("", "/")):
        raise ValueError("gateway URL must be an HTTP(S) origin without credentials")
    if not model or len(model) > 253 or "/" in model:
        raise ValueError("model must be a deployment name")
    return gateway_url.rstrip("/") + "/models/" + urllib.parse.quote(model, safe="") + "/v1/chat/completions"


def image_content(path: Path) -> list[dict]:
    """Bound a local PNG sample before sending it to the runtime."""
    with path.open("rb") as source:
        data = source.read(2 * 1024**2 + 1)
    if len(data) > 2 * 1024**2 or len(data) < 24 or data[:8] != b"\x89PNG\r\n\x1a\n" or data[12:16] != b"IHDR":
        raise ValueError("use a PNG image of at most 2MiB")
    width, height = struct.unpack(">II", data[16:24])
    if not width or not height or width * height > 1024**2:
        raise ValueError("use a PNG image of at most one megapixel")
    return [
        {"type": "text", "text": "What is the dominant color in this image? Reply with the color name."},
        {"type": "image_url", "image_url": {"url": "data:image/png;base64," + base64.b64encode(data).decode("ascii")}},
    ]


def benchmark(gateway_url: str, model: str, count: int = 3,
              timeout: float = 30, token: str | None = None, *,
              image_path: Path | None = None, expected_text: str | None = None) -> dict:
    if not 1 <= count <= 5 or not 0 < timeout <= 120:
        raise ValueError("use 1-5 requests and a timeout of at most 120 seconds")
    url = endpoint(gateway_url, model)
    headers = {"Content-Type": "application/json", "Accept": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    if expected_text is not None and (not image_path or not expected_text.strip() or len(expected_text) > 128):
        raise ValueError("an expected visual answer requires an image and 1-128 characters")
    payload = json.dumps({
        "model": model, "messages": [{"role": "user", "content": image_content(image_path) if image_path else "Reply with a short greeting."}],
        "max_tokens": 16, "stream": False,
    }).encode()
    durations = []
    completion_tokens = []
    opener = urllib.request.build_opener(NoRedirect)
    for _ in range(count):
        request = urllib.request.Request(url, data=payload, headers=headers, method="POST")
        started = time.monotonic()
        with opener.open(request, timeout=timeout) as response:
            body = response.read(2 * 1024**2 + 1)
        durations.append(time.monotonic() - started)
        if len(body) > 2 * 1024**2:
            raise ValueError("inference response exceeded limit")
        result = json.loads(body)
        choices = result.get("choices")
        if not isinstance(choices, list) or not choices or not isinstance(choices[0], dict):
            raise ValueError("inference response has no choices")
        message = choices[0].get("message", {})
        if not isinstance(message, dict) or not isinstance(message.get("content"), str) or not message["content"].strip():
            raise ValueError("inference returned no text")
        if expected_text is not None and not re.search(r"(?<!\w)" + re.escape(expected_text.strip()) + r"(?!\w)", message["content"], re.IGNORECASE):
            raise ValueError("inference did not match the expected visual answer")
        usage = result.get("usage")
        value = usage.get("completion_tokens") if isinstance(usage, dict) else None
        completion_tokens.append(value if type(value) is int and value >= 0 else None)
    measured_tokens = sum(completion_tokens) if all(v is not None for v in completion_tokens) else None
    elapsed = sum(durations)
    return {
        "model": model, "requests_succeeded": count,
        "input_type": "image" if image_path else "text",
        "visual_answer_checked": expected_text is not None,
        "latency_seconds": {"min": min(durations), "mean": statistics.mean(durations), "max": max(durations)},
        "completion_tokens": measured_tokens,
        "sequential_completion_tokens_per_second": measured_tokens / elapsed if measured_tokens is not None and elapsed > 0 else None,
        "scope": "small sequential inference sample; not capacity certification or streaming TTFT",
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gateway-url", required=True)
    parser.add_argument("--model", required=True)
    parser.add_argument("--requests", type=int, default=3)
    parser.add_argument("--timeout", type=float, default=30)
    parser.add_argument("--image", type=Path, help="public test PNG, at most 2MiB and one megapixel")
    parser.add_argument("--expect-text", help="expected visual answer; checked without printing response text")
    args = parser.parse_args()
    try:
        report = benchmark(args.gateway_url, args.model, args.requests,
                           args.timeout, os.environ.get("INFEROPS_GATEWAY_TOKEN"),
                           image_path=args.image, expected_text=args.expect_text)
    except urllib.error.HTTPError as exc:
        print(json.dumps({"error": f"inference HTTP {exc.code}; inspect auth, route, and model readiness"}))
        return 1
    except (OSError, ValueError, TypeError, AttributeError, urllib.error.URLError) as exc:
        print(json.dumps({"error": "inference check failed", "kind": type(exc).__name__}))
        return 1
    print(json.dumps(report, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
