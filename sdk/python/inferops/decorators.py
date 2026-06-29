"""Decorator helpers for declaring model deployments."""

from __future__ import annotations

from .spec import validate_endpoint_metadata


def web_endpoint(*, method: str = "POST", path: str, streaming: bool | None = None):
    """Declare a method as a web endpoint on a model class."""
    endpoint = validate_endpoint_metadata(method=method, path=path, streaming=streaming)

    def wrapper(func):
        func.__inferops_endpoint__ = endpoint
        return func

    return wrapper
