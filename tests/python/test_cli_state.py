"""Tests for dependency-free CLI state behavior."""

from __future__ import annotations

import tempfile
from pathlib import Path
import unittest

from inferops_cli.state import load_state, save_state, state_path


class CLIStateTest(unittest.TestCase):
    def test_missing_state_returns_empty_versioned_state(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            self.assertEqual(load_state(directory), {"version": 1, "deployments": {}})

    def test_state_round_trip(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            expected = {"version": 1, "deployments": {"qwen": {"phase": "Active"}}}

            save_state(expected, directory)

            self.assertEqual(load_state(directory), expected)
            self.assertEqual(state_path(directory), Path(directory) / ".inferops" / "state.json")


if __name__ == "__main__":
    unittest.main()
