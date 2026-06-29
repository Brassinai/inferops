"""Helpers for invoking custom SDK web endpoints."""

from __future__ import annotations

from collections.abc import AsyncIterator, Iterator
from dataclasses import dataclass
import inspect
from typing import Any

from .spec import EndpointMetadata


@dataclass(frozen=True, slots=True)
class EndpointInvocation:
    """Normalized endpoint invocation result."""

    name: str
    method: str
    path: str
    streaming: bool
    response: Any | None = None
    stream: AsyncIterator[Any] | None = None


async def invoke_web_endpoint(model: Any, endpoint_name: str, request: Any) -> EndpointInvocation:
    """Invoke one decorated endpoint and normalize response versus stream semantics."""
    endpoint = _require_endpoint_metadata(model, endpoint_name)
    handler = getattr(model, endpoint_name, None)
    if handler is None or not callable(handler):
        raise AttributeError(f"endpoint handler not found: {endpoint_name}")

    result = handler(request)
    if inspect.isawaitable(result):
        result = await result

    if inspect.isasyncgen(result):
        return _build_streaming_invocation(endpoint=endpoint, stream=result)

    if _is_async_iterator(result):
        return _build_streaming_invocation(endpoint=endpoint, stream=result)

    if inspect.isgenerator(result):
        return _build_streaming_invocation(endpoint=endpoint, stream=_sync_iterator_to_async(result))

    if endpoint.streaming:
        raise TypeError(
            f"endpoint {endpoint.name!r} declared streaming but returned a non-streaming response; "
            "yield items or return an async iterator"
        )

    return EndpointInvocation(
        name=endpoint.name,
        method=endpoint.method,
        path=endpoint.path,
        streaming=False,
        response=result,
    )


def _require_endpoint_metadata(model: Any, endpoint_name: str) -> EndpointMetadata:
    model_config = getattr(model, "__inferops_model__", None)
    if model_config is None:
        raise RuntimeError("model metadata is not attached to this instance")
    for endpoint in model_config.endpoints:
        if endpoint.name == endpoint_name:
            return endpoint
    raise AttributeError(f"endpoint metadata not found: {endpoint_name}")


def _is_async_iterator(value: Any) -> bool:
    return hasattr(value, "__aiter__") and not inspect.isawaitable(value)


def _build_streaming_invocation(*, endpoint: EndpointMetadata, stream: AsyncIterator[Any]) -> EndpointInvocation:
    if not endpoint.streaming:
        raise TypeError(
            f"endpoint {endpoint.name!r} produced a streaming response; add streaming=True or implement the handler as a generator"
        )
    return EndpointInvocation(
        name=endpoint.name,
        method=endpoint.method,
        path=endpoint.path,
        streaming=True,
        stream=stream,
    )


async def _sync_iterator_to_async(iterator: Iterator[Any]) -> AsyncIterator[Any]:
    for item in iterator:
        yield item
