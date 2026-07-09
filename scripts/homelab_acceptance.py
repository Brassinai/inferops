#!/usr/bin/env python3
"""Run and record the single-node GPU homelab acceptance workflow."""

from __future__ import annotations

import argparse
from collections.abc import Callable
from dataclasses import dataclass
from datetime import datetime, timezone
import json
from pathlib import Path
import re
import shlex
import subprocess
import sys
import time
from urllib.parse import urlparse


@dataclass(frozen=True)
class Step:
    name: str
    command: list[str]
    required: bool = True
    validator: Callable[["StepResult"], str | None] | None = None


@dataclass
class StepResult:
    name: str
    command: list[str]
    required: bool
    returncode: int
    duration_seconds: float
    stdout: str
    stderr: str
    validation_error: str | None = None

    @property
    def passed(self) -> bool:
        return self.returncode == 0 and self.validation_error is None


def main() -> int:
    args = parse_args()
    steps = acceptance_steps(args)
    results: list[StepResult] = []
    for step in steps:
        result = run_step(step, timeout=args.step_timeout_seconds)
        results.append(result)
        write_report(args.output, args, results)
        print_step(result)
        if step.required and not result.passed and not args.keep_going:
            break
    failed_required = [result for result in results if result.required and not result.passed]
    return 1 if failed_required else 0


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Run the InferOps single-node GPU homelab acceptance workflow.",
    )
    parser.add_argument("--manifest", required=True, help="ModelDeployment manifest to apply.")
    parser.add_argument("--model-name", required=True, help="ModelDeployment name to activate.")
    parser.add_argument("--gateway-url", required=True, help="Base URL for the InferOps gateway.")
    parser.add_argument("--namespace", default="inferops-system", help="Kubernetes namespace.")
    parser.add_argument("--cache-path", default="/var/lib/inferops/models", help="Homelab cache root.")
    parser.add_argument("--output", default="homelab-acceptance.md", help="Markdown report path.")
    parser.add_argument("--replacement-manifest", help="Optional replacement ModelDeployment manifest.")
    parser.add_argument("--replacement-name", help="Optional replacement ModelDeployment name.")
    parser.add_argument("--skip-install", action="store_true", help="Do not run inferops install.")
    parser.add_argument("--keep-going", action="store_true", help="Continue after required step failures.")
    parser.add_argument("--step-timeout-seconds", type=float, default=900)
    args = parser.parse_args()
    validate_args(args, parser)
    return args


def validate_args(args: argparse.Namespace, parser: argparse.ArgumentParser) -> None:
    parsed_gateway = urlparse(args.gateway_url)
    if parsed_gateway.scheme not in {"http", "https"} or not parsed_gateway.netloc:
        parser.error("--gateway-url must be an absolute http or https URL")
    if not Path(args.manifest).is_file():
        parser.error(f"--manifest does not exist or is not a file: {args.manifest}")
    if args.replacement_manifest and not Path(args.replacement_manifest).is_file():
        parser.error(
            f"--replacement-manifest does not exist or is not a file: {args.replacement_manifest}"
        )
    if args.replacement_manifest and not args.replacement_name:
        parser.error("--replacement-name is required with --replacement-manifest")
    if args.replacement_name and not args.replacement_manifest:
        parser.error("--replacement-manifest is required with --replacement-name")


def acceptance_steps(args: argparse.Namespace) -> list[Step]:
    steps: list[Step] = []
    if not args.skip_install:
        steps.append(
            Step(
                "Install homelab profile",
                [
                    "inferops",
                    "install",
                    "--profile",
                    "homelab",
                    "--namespace",
                    args.namespace,
                    "--cache-path",
                    args.cache_path,
                ],
            )
        )
    steps.extend(
        [
            Step("Doctor", ["inferops", "doctor", "--namespace", args.namespace]),
            Step("GPU inventory before deploy", json_command("inferops", "gpu", "list", "--namespace", args.namespace)),
            Step("Apply model manifest", ["kubectl", "apply", "-n", args.namespace, "-f", args.manifest]),
            Step("Cache state after deploy", json_command("inferops", "cache", "list", "--namespace", args.namespace)),
            Step("Activate model", ["inferops", "activate", args.model_name, "--namespace", args.namespace]),
            Step("Status after activation", json_command("inferops", "status", args.model_name, "--namespace", args.namespace)),
            Step(
                "Streaming inference",
                inference_command(args.gateway_url, args.model_name),
                validator=validate_streaming_response,
            ),
            Step("Deactivate model", ["inferops", "deactivate", args.model_name, "--namespace", args.namespace]),
            Step("GPU inventory after deactivate", json_command("inferops", "gpu", "list", "--namespace", args.namespace)),
            Step("Cache state after deactivate", json_command("inferops", "cache", "list", "--namespace", args.namespace)),
            Step("Re-activate model", ["inferops", "activate", args.model_name, "--namespace", args.namespace]),
            Step("Cache state after re-activate", json_command("inferops", "cache", "list", "--namespace", args.namespace)),
        ]
    )
    if args.replacement_manifest:
        steps.extend(
            [
                Step(
                    "Apply replacement manifest",
                    ["kubectl", "apply", "-n", args.namespace, "-f", args.replacement_manifest],
                ),
                Step(
                    "Activate replacement with explicit replacement policy",
                    [
                        "inferops",
                        "activate",
                        args.replacement_name,
                        "--namespace",
                        args.namespace,
                        "--when-full",
                        "ReplaceOldest",
                    ],
                ),
                Step(
                    "Replacement status",
                    json_command("inferops", "status", args.replacement_name, "--namespace", args.namespace),
                ),
            ]
        )
    return steps


def json_command(*command: str) -> list[str]:
    return [*command, "--output", "json"]


def inference_command(gateway_url: str, model_name: str) -> list[str]:
    payload = {
        "model": model_name,
        "stream": True,
        "messages": [{"role": "user", "content": "Say hello from InferOps homelab acceptance."}],
    }
    return [
        "curl",
        "--fail-with-body",
        "--no-buffer",
        "--show-error",
        "--silent",
        "--max-time",
        "180",
        "-X",
        "POST",
        gateway_url.rstrip("/") + f"/models/{model_name}/v1/chat/completions",
        "-H",
        "Content-Type: application/json",
        "-d",
        json.dumps(payload, separators=(",", ":")),
    ]


def run_step(step: Step, *, timeout: float) -> StepResult:
    started = time.monotonic()
    try:
        completed = subprocess.run(
            step.command,
            check=False,
            capture_output=True,
            text=True,
            timeout=timeout,
        )
        result = StepResult(
            name=step.name,
            command=step.command,
            required=step.required,
            returncode=completed.returncode,
            duration_seconds=time.monotonic() - started,
            stdout=completed.stdout,
            stderr=completed.stderr,
        )
        if result.returncode == 0 and step.validator is not None:
            result.validation_error = step.validator(result)
        return result
    except subprocess.TimeoutExpired as exc:
        return StepResult(
            name=step.name,
            command=step.command,
            required=step.required,
            returncode=124,
            duration_seconds=time.monotonic() - started,
            stdout=exc.stdout or "",
            stderr=(exc.stderr or "") + f"\nstep timed out after {timeout:.0f}s",
        )


def print_step(result: StepResult) -> None:
    status = "PASS" if result.passed else "FAIL"
    print(f"{status} {result.name} ({result.duration_seconds:.1f}s)")
    if result.validation_error:
        print(f"  validation: {result.validation_error}")


def write_report(path: str, args: argparse.Namespace, results: list[StepResult]) -> None:
    report_path = Path(path)
    lines = [
        "# Homelab Acceptance Report",
        "",
        f"- Generated: {datetime.now(timezone.utc).isoformat()}",
        f"- Namespace: `{args.namespace}`",
        f"- Model: `{args.model_name}`",
        f"- Manifest: `{args.manifest}`",
        f"- Gateway URL: `{args.gateway_url}`",
        "",
        "## Acceptance Criteria",
        "",
        "- Streaming gateway inference: `Streaming inference` must pass and contain an SSE event.",
        "- GPU release after deactivation: compare `GPU inventory before deploy` with `GPU inventory after deactivate`.",
        "- Cache preservation: compare cache entries after deploy, deactivate, and re-activate.",
        "- Explicit replacement: replacement steps must pass when replacement arguments are supplied.",
        "",
        "## Results",
        "",
        "| Step | Required | Status | Seconds |",
        "| --- | --- | --- | ---: |",
    ]
    for result in results:
        status = "PASS" if result.passed else "FAIL"
        lines.append(
            f"| {result.name} | {str(result.required).lower()} | {status} | {result.duration_seconds:.1f} |"
        )
    lines.extend(["", "## Command Logs", ""])
    for result in results:
        lines.extend(
            [
                f"### {result.name}",
                "",
                f"- Status: {'PASS' if result.passed else 'FAIL'}",
                f"- Exit code: `{result.returncode}`",
                f"- Duration: `{result.duration_seconds:.1f}s`",
                *([f"- Validation: {result.validation_error}"] if result.validation_error else []),
                "",
                "```bash",
                redact_command(result.command),
                "```",
                "",
            ]
        )
        if result.stdout:
            lines.extend(["stdout:", "", "```text", redact_text(result.stdout), "```", ""])
        if result.stderr:
            lines.extend(["stderr:", "", "```text", redact_text(result.stderr), "```", ""])
    report_path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def redact_command(command: list[str]) -> str:
    return redact_text(" ".join(shlex.quote(part) for part in command))


def redact_text(value: str) -> str:
    redacted = re.sub(
        r"(?im)^(authorization:\s*bearer\s+).+$",
        r"\1<redacted>",
        value,
    )
    redacted = re.sub(r"(?i)\bbearer\s+[A-Za-z0-9._~+/=-]+", "Bearer <redacted>", redacted)
    redacted = re.sub(r"(?i)(--token(?:=|\s+))[^\s]+", r"\1<redacted>", redacted)
    redacted = re.sub(r"\bhf_[A-Za-z0-9_=-]{8,}\b", "hf_<redacted>", redacted)
    return redacted


def validate_streaming_response(result: StepResult) -> str | None:
    output = result.stdout.strip()
    if not output:
        return "streaming request returned an empty body"
    if "data:" not in output:
        return "streaming response did not contain a server-sent event"
    if "[DONE]" not in output and not output.endswith("\n\n"):
        return "streaming response did not contain a completion marker or SSE frame boundary"
    return None


if __name__ == "__main__":
    sys.exit(main())
