#!/usr/bin/env python3
"""Check basic chat behavior through a local gateway without authentication."""

from __future__ import annotations

import argparse
import http.client
import ipaddress
import json
import sys
import urllib.error
import urllib.parse
import urllib.request

from smoke_inference import NoRedirect, endpoint


class CheckFailed(ValueError):
    """A synthetic response failed a check; never include response content."""


def verify(gateway_url: str, model: str, timeout: float = 30) -> dict:
    url = endpoint(gateway_url, model)
    host = urllib.parse.urlsplit(url).hostname
    if host != "localhost" and not ipaddress.ip_address(host).is_loopback:
        raise ValueError("use the localhost gateway forward")
    if not 0 < timeout <= 120:
        raise ValueError("use a timeout of at most 120 seconds")

    # Avoid ambient proxy routing and ignore INFEROPS_GATEWAY_TOKEN deliberately.
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect)
    checks = (
        ("greeting", "Say hello in one word.", "hello"),
        ("simple_instruction", "What is 2 + 3? Reply with only the number.", "5"),
    )
    for name, prompt, expected in checks:
        payload = json.dumps({
            "model": model,
            "messages": [{"role": "user", "content": prompt}],
            "max_tokens": 32, "temperature": 0, "stream": False,
        }).encode()
        request = urllib.request.Request(
            url, data=payload, method="POST",
            headers={"Content-Type": "application/json", "Accept": "application/json"},
        )
        with opener.open(request, timeout=timeout) as response:
            if response.status != 200:
                raise CheckFailed(f"{name}: unexpected_http_status")
            body = response.read(64 * 1024 + 1)
        if len(body) > 64 * 1024:
            raise CheckFailed(f"{name}: response_too_large")
        result = json.loads(body)
        choices = result.get("choices") if isinstance(result, dict) else None
        if not isinstance(choices, list) or not choices or not isinstance(choices[0], dict):
            raise CheckFailed(f"{name}: invalid_choices")
        choice = choices[0]
        message = choice.get("message")
        if (not isinstance(message, dict) or message.get("role") != "assistant"
                or not isinstance(message.get("content"), str)):
            raise CheckFailed(f"{name}: invalid_message")
        if choice.get("finish_reason") != "stop":
            raise CheckFailed(f"{name}: incomplete_response")
        answer = message["content"].strip().casefold().strip(".!?").strip()
        if answer != expected:
            raise CheckFailed(f"{name}: unexpected_answer")
    return {
        "model": model, "requests_succeeded": len(checks),
        "authentication": "no_authorization_header",
        "checks_passed": [name for name, _prompt, _expected in checks],
        "scope": "basic synthetic chat checks; not a general quality assessment",
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gateway-url", required=True)
    parser.add_argument("--model", required=True)
    parser.add_argument("--timeout", type=float, default=30)
    args = parser.parse_args()
    try:
        report = verify(args.gateway_url, args.model, args.timeout)
    except urllib.error.HTTPError as exc:
        report = {"error": "chat check failed", "http_status": exc.code}
    except CheckFailed as exc:
        report = {"error": "chat check failed", "check": str(exc)}
    except (OSError, ValueError, TypeError, http.client.HTTPException) as exc:
        report = {"error": "chat check failed", "kind": type(exc).__name__}
    else:
        print(json.dumps(report, indent=2))
        return 0
    print(json.dumps(report))
    return 1


if __name__ == "__main__":
    sys.exit(main())
