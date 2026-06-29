"""Argument parser helpers for consistent CLI error handling."""

from __future__ import annotations

import argparse

from .errors import UsageError


class CLIArgumentParser(argparse.ArgumentParser):
    """Argument parser that raises structured usage errors instead of exiting."""

    def error(self, message: str) -> None:
        raise UsageError(
            message=message,
            usage=self.format_usage(),
            prog=self.prog,
        )
