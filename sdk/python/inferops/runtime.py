"""Runtime helper methods for decorated SDK model classes."""

from __future__ import annotations

from collections.abc import AsyncIterator
from typing import Any, Protocol

from .spec import ModelConfig


class RuntimeInvoker(Protocol):
    """Minimal runtime abstraction used by custom SDK endpoints."""

    async def generate(self, model: ModelConfig, request: Any, **kwargs: Any) -> Any:
        """Generate one non-streaming response for a model."""

    def generate_stream(self, model: ModelConfig, request: Any, **kwargs: Any) -> AsyncIterator[Any]:
        """Generate one streaming response for a model."""


def attach_runtime_methods(model_class: type[Any]) -> None:
    """Attach runtime helpers to one decorated model class."""
    _attach_model_method(model_class, "bind_runtime", bind_runtime)
    _attach_model_method(model_class, "_require_runtime", _require_runtime)
    _attach_model_method(model_class, "generate", generate)
    _attach_model_method(model_class, "generate_stream", generate_stream)


def bind_runtime(self: Any, runtime: RuntimeInvoker) -> Any:
    """Bind one runtime invoker to a model instance."""
    _validate_runtime(runtime)
    self.__inferops_runtime__ = runtime
    return self


def _require_runtime(self: Any) -> RuntimeInvoker:
    runtime = getattr(self, "__inferops_runtime__", None)
    if runtime is None:
        raise RuntimeError("model runtime is not bound; call bind_runtime(...) before invoking model helpers")
    return runtime


async def generate(self: Any, request: Any, **kwargs: Any) -> Any:
    """Generate one non-streaming response through the bound runtime."""
    runtime = self._require_runtime()
    model_config = getattr(self, "__inferops_model__", None)
    if model_config is None:
        raise RuntimeError("model metadata is not attached to this instance")
    return await runtime.generate(model_config, request, **kwargs)


def generate_stream(self: Any, request: Any, **kwargs: Any) -> AsyncIterator[Any]:
    """Generate one streaming response through the bound runtime."""
    runtime = self._require_runtime()
    model_config = getattr(self, "__inferops_model__", None)
    if model_config is None:
        raise RuntimeError("model metadata is not attached to this instance")
    return runtime.generate_stream(model_config, request, **kwargs)


def _attach_model_method(model_class: type[Any], name: str, helper: Any) -> None:
    existing = getattr(model_class, name, None)
    if existing is None:
        setattr(model_class, name, helper)
        return
    if existing is helper:
        return
    raise ValueError(
        f"model class {model_class.__name__} uses reserved helper name {name!r}; choose a different method name"
    )


def _validate_runtime(runtime: Any) -> None:
    if not callable(getattr(runtime, "generate", None)):
        raise TypeError("runtime must provide a callable generate(model, request, **kwargs) method")
    if not callable(getattr(runtime, "generate_stream", None)):
        raise TypeError("runtime must provide a callable generate_stream(model, request, **kwargs) method")
