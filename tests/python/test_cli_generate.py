"""Tests for the `inferops generate` CLI command."""

from __future__ import annotations

import argparse
from contextlib import redirect_stderr, redirect_stdout
import io
from pathlib import Path
import tempfile
import textwrap
import unittest

import yaml

from inferops_cli import generate


ROOT = Path(__file__).resolve().parents[2]


def load_yaml_fixture(relative_path: str) -> dict:
    """Load one YAML fixture from the repository."""
    with (ROOT / relative_path).open(encoding="utf-8") as stream:
        return yaml.safe_load(stream)


class CLIGenerateTest(unittest.TestCase):
    def test_generate_emits_deterministic_contract_yaml(self) -> None:
        source = textwrap.dedent(
            """
            import inferops

            app = inferops.App("support")

            @app.model(name="qwen-inactive", model="Qwen/Qwen2.5-7B-Instruct")
            class QwenInactive:
                pass
            """
        )
        expected = load_yaml_fixture("deploy/manifests/examples/contracts/modeldeployment-inactive.yaml")

        with tempfile.TemporaryDirectory() as directory:
            app_path = Path(directory) / "app.py"
            app_path.write_text(source, encoding="utf-8")
            args = argparse.Namespace(app=str(app_path))

            first_stdout = io.StringIO()
            first_stderr = io.StringIO()
            with redirect_stdout(first_stdout), redirect_stderr(first_stderr):
                first_code = generate.run(args)

            second_stdout = io.StringIO()
            second_stderr = io.StringIO()
            with redirect_stdout(second_stdout), redirect_stderr(second_stderr):
                second_code = generate.run(args)

        self.assertEqual(first_code, 0)
        self.assertEqual(second_code, 0)
        self.assertEqual(first_stderr.getvalue(), "")
        self.assertEqual(second_stderr.getvalue(), "")
        self.assertEqual(first_stdout.getvalue(), second_stdout.getvalue())
        self.assertEqual(
            yaml.safe_load(first_stdout.getvalue()),
            {key: expected[key] for key in ("apiVersion", "kind", "metadata", "spec")},
        )

    def test_generate_reports_missing_app_instance(self) -> None:
        source = "VALUE = 1\n"

        with tempfile.TemporaryDirectory() as directory:
            app_path = Path(directory) / "app.py"
            app_path.write_text(source, encoding="utf-8")
            args = argparse.Namespace(app=str(app_path))
            stdout = io.StringIO()
            stderr = io.StringIO()
            with redirect_stdout(stdout), redirect_stderr(stderr):
                exit_code = generate.run(args)

        self.assertEqual(exit_code, 1)
        self.assertEqual(stdout.getvalue(), "")
        self.assertIn("no inferops.App instance found", stderr.getvalue())

    def test_generate_rejects_directory_paths(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            args = argparse.Namespace(app=directory)
            stdout = io.StringIO()
            stderr = io.StringIO()
            with redirect_stdout(stdout), redirect_stderr(stderr):
                exit_code = generate.run(args)

        self.assertEqual(exit_code, 1)
        self.assertEqual(stdout.getvalue(), "")
        self.assertIn("application path must be a file", stderr.getvalue())


if __name__ == "__main__":
    unittest.main()
