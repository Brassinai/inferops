"""End-to-end conformance test for the custom FastAPI runtime."""

from __future__ import annotations

import http.server
import importlib.util
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import sys
import tempfile
import threading
import time
import unittest
import urllib.error
import urllib.request


ROOT = Path(__file__).resolve().parents[2]
CUSTOM_RUNTIME = ROOT / "examples/custom-runtime"
CONFORMANCE_RUNNER = ROOT / "scripts/runtime_conformance.py"


def load_conformance_module():
    spec = importlib.util.spec_from_file_location(
        "inferops_runtime_conformance",
        CONFORMANCE_RUNNER,
    )
    if spec is None or spec.loader is None:
        raise RuntimeError("could not load runtime conformance module")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


CONFORMANCE = load_conformance_module()


class RuntimeConformanceTest(unittest.TestCase):
    def test_custom_fastapi_runtime_satisfies_contract_and_shuts_down(self) -> None:
        with tempfile.TemporaryDirectory() as model_path:
            port = available_port()
            environment = os.environ.copy()
            environment.update(
                {
                    "HOST": "127.0.0.1",
                    "PORT": str(port),
                    "MODEL_PATH": model_path,
                    "MODEL_REPO": "custom-fastapi-test",
                    "PYTHON_BIN": sys.executable,
                }
            )
            process = subprocess.Popen(
                ["/bin/sh", str(CUSTOM_RUNTIME / "entrypoint.sh")],
                cwd=CUSTOM_RUNTIME,
                env=environment,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
            )
            try:
                wait_until_ready(process, port)
                result = subprocess.run(
                    [
                        sys.executable,
                        str(CONFORMANCE_RUNNER),
                        "--base-url",
                        f"http://127.0.0.1:{port}",
                        "--model",
                        "custom-fastapi-test",
                        "--readiness-path",
                        "/ready",
                        "--timeout",
                        "2",
                    ],
                    text=True,
                    capture_output=True,
                    check=False,
                    timeout=15,
                )
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn("runtime conformance passed", result.stdout)

                process.terminate()
                _, stderr = process.communicate(timeout=10)
                return_code = process.returncode
                self.assertIn(
                    return_code,
                    (0, -signal.SIGTERM),
                    stderr,
                )
                self.assertIn("Application shutdown complete", stderr)
            finally:
                if process.poll() is None:
                    process.kill()
                if process.stdout and not process.stdout.closed:
                    process.communicate(timeout=5)

    def test_conformance_rejects_buffered_stream(self) -> None:
        server = http.server.ThreadingHTTPServer(
            ("127.0.0.1", 0),
            BufferedChatHandler,
        )
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            contract = CONFORMANCE.RuntimeContract(
                base_url=f"http://127.0.0.1:{server.server_port}",
                model="buffered-model",
                timeout=2,
            )
            with self.assertRaisesRegex(
                CONFORMANCE.ConformanceError,
                "buffered",
            ):
                CONFORMANCE.validate_chat_completion(contract)
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=2)


class BufferedChatHandler(http.server.BaseHTTPRequestHandler):
    def do_POST(self) -> None:
        length = int(self.headers.get("Content-Length", "0"))
        payload = json.loads(self.rfile.read(length))
        if self.path != "/v1/chat/completions":
            self.send_error(404)
            return
        if not payload.get("stream"):
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps({"choices": [{"index": 0}]}).encode())
            return

        body = (
            b'data: {"choices":[{"delta":{"content":"buffered"}}]}\n\n'
            b'data: {"choices":[{"finish_reason":"stop"}]}\n\n'
            b"data: [DONE]\n\n"
        )
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, _format: str, *args: object) -> None:
        del args


def available_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as listener:
        listener.bind(("127.0.0.1", 0))
        return int(listener.getsockname()[1])


def wait_until_ready(process: subprocess.Popen[str], port: int) -> None:
    url = f"http://127.0.0.1:{port}/ready"
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        if process.poll() is not None:
            _, stderr = process.communicate(timeout=1)
            raise AssertionError(
                f"custom runtime exited with {process.returncode}: {stderr}"
            )
        try:
            with urllib.request.urlopen(url, timeout=0.5) as response:
                if response.status == 200:
                    return
        except (urllib.error.URLError, TimeoutError):
            time.sleep(0.05)
    raise AssertionError("custom runtime did not become ready within 10 seconds")


if __name__ == "__main__":
    unittest.main()
