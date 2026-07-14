"""Runtime binding that forwards SDK endpoint calls to an InferOps gateway."""

from __future__ import annotations

from collections.abc import AsyncIterator, Mapping
from typing import Any

from .client import Client
from .spec import ModelConfig


class GatewayRuntime:
    """Runtime invoker backed by a live InferOps gateway route."""

    def __init__(
        self,
        *,
        gateway_url: str = "http://127.0.0.1:8080",
        api_key: str | None = None,
        timeout: float = 30.0,
    ) -> None:
        normalized = gateway_url.strip().rstrip("/")
        if not normalized:
            raise ValueError("gateway_url is required")
        self._gateway_url = normalized
        self._api_key = api_key
        self._timeout = timeout
        self._clients: dict[str, Client] = {}

    async def generate(self, model: ModelConfig, request: Any, **params: Any) -> Any:
        """Send a non-streaming chat completion request to the model route."""
        return self._client_for(model).chat.completions.create(
            model=model.name,
            messages=_messages_from_request(request),
            **params,
        )

    def generate_stream(self, model: ModelConfig, request: Any, **params: Any) -> AsyncIterator[Any]:
        """Send a streaming chat completion request to the model route."""
        stream = self._client_for(model).chat.completions.create(
            model=model.name,
            messages=_messages_from_request(request),
            stream=True,
            **params,
        )

        async def iterator() -> AsyncIterator[Any]:
            for event in stream:
                yield event

        return iterator()

    def _client_for(self, model: ModelConfig) -> Client:
        client = self._clients.get(model.name)
        if client is None:
            client = Client(
                base_url=f"{self._gateway_url}/models/{model.name}",
                api_key=self._api_key,
                timeout=self._timeout,
            )
            self._clients[model.name] = client
        return client


def _messages_from_request(request: Any) -> list[dict[str, Any]]:
    if isinstance(request, str):
        return [{"role": "user", "content": request}]
    if isinstance(request, list):
        return request
    if isinstance(request, Mapping):
        messages = request.get("messages")
        if isinstance(messages, list):
            return messages
        prompt = request.get("prompt")
        if isinstance(prompt, str):
            return [{"role": "user", "content": prompt}]
    raise TypeError("request must be a prompt string, a messages list, or a mapping with prompt/messages")
