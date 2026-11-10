#!/usr/bin/env python3
"""Run a bounded gateway inference benchmark without printing prompts or output."""

from __future__ import annotations

import argparse
import json
import os
import statistics
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


def benchmark(gateway_url: str, model: str, count: int = 3,
              timeout: float = 30, token: str | None = None) -> dict:
    if not 1 <= count <= 5 or not 0 < timeout <= 120:
        raise ValueError("use 1-5 requests and a timeout of at most 120 seconds")
    url = endpoint(gateway_url, model)
    headers = {"Content-Type": "application/json", "Accept": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    payload = json.dumps({
        "model": model, "messages": [{"role": "user", "content": "Reply with a short greeting."}],
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
        usage = result.get("usage")
        value = usage.get("completion_tokens") if isinstance(usage, dict) else None
        completion_tokens.append(value if type(value) is int and value >= 0 else None)
    measured_tokens = sum(completion_tokens) if all(v is not None for v in completion_tokens) else None
    elapsed = sum(durations)
    return {
        "model": model, "requests_succeeded": count,
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
    args = parser.parse_args()
    try:
        report = benchmark(args.gateway_url, args.model, args.requests,
                           args.timeout, os.environ.get("INFEROPS_GATEWAY_TOKEN"))
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
