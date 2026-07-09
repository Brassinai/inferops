#!/usr/bin/env python3
"""Parse all checked-in Python source without importing project dependencies."""

from __future__ import annotations

import ast
from pathlib import Path


ROOTS = (
    Path("sdk/python"),
    Path("cli"),
    Path("examples"),
    Path("tests/python"),
    Path("scripts"),
)


def main() -> None:
    paths = sorted(path for root in ROOTS for path in root.rglob("*.py"))
    if not paths:
        raise SystemExit("error: no Python files found")

    for path in paths:
        ast.parse(path.read_text(encoding="utf-8"), filename=str(path))

    print(f"Parsed {len(paths)} Python files.")


if __name__ == "__main__":
    main()
