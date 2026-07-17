"""Consistent CLI errors and exit codes."""

from __future__ import annotations

from collections.abc import Callable
from enum import IntEnum
import sys


class ExitCode(IntEnum):
    """Stable CLI exit codes."""

    SUCCESS = 0
    ERROR = 1
    USAGE = 2
    NOT_FOUND = 3
    LIFECYCLE_REJECTED = 10
    LIFECYCLE_FAILED = 11
    LIFECYCLE_TIMEOUT = 12
    LIFECYCLE_SUPERSEDED = 13


class CLIError(Exception):
    """A user-facing CLI error with a stable exit code."""

    def __init__(self, message: str, exit_code: ExitCode = ExitCode.ERROR):
        super().__init__(message)
        self.exit_code = exit_code


class NotFoundError(CLIError):
    """Raised when a requested resource does not exist."""

    def __init__(self, message: str):
        super().__init__(message, exit_code=ExitCode.NOT_FOUND)


class UsageError(CLIError):
    """Raised when argument parsing fails."""

    def __init__(self, message: str, usage: str, prog: str):
        super().__init__(message, exit_code=ExitCode.USAGE)
        self.usage = usage
        self.prog = prog


def run_with_cli_errors(action: Callable[[], int]) -> int:
    """Run one handler and render consistent user-facing errors."""
    try:
        return int(action())
    except CLIError as err:
        render_cli_error(err)
        return int(err.exit_code)
    except (FileNotFoundError, ValueError) as err:
        render_cli_error(CLIError(str(err), exit_code=ExitCode.ERROR))
        return int(ExitCode.ERROR)
    except BrokenPipeError:
        return int(ExitCode.SUCCESS)
    except Exception:
        render_cli_error(CLIError("unexpected failure", exit_code=ExitCode.ERROR))
        return int(ExitCode.ERROR)


def render_cli_error(err: CLIError) -> None:
    """Render one CLI error to stderr."""
    if isinstance(err, UsageError):
        usage = err.usage
        print(usage, file=sys.stderr, end="" if usage.endswith("\n") else "\n")
        print(f"{err.prog}: error: {err}", file=sys.stderr)
        return
    _print_error(str(err))


def _print_error(message: str) -> None:
    print(f"error: {message}", file=sys.stderr)
