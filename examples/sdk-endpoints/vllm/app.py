from __future__ import annotations

from collections.abc import AsyncIterator
from typing import Any

import inferops


app = inferops.App("vllm-sdk-endpoints")
DEFAULT_MAX_TOKENS = 96


@app.model(
    name="assistant-vllm",
    engine="vllm",
    model="Qwen/Qwen2.5-7B-Instruct",
    gpu=1,
    min_replicas=0,
    max_replicas=1,
    max_model_len=4096,
    cache_size="50Gi",
)
class Assistant:
    @inferops.web_endpoint(method="POST", path="/chat")
    async def chat(self, request: dict[str, Any]) -> Any:
        return await self.generate(
            request["prompt"],
            max_tokens=request.get("max_tokens", DEFAULT_MAX_TOKENS),
        )

    @inferops.web_endpoint(method="POST", path="/chat/stream")
    async def stream_chat(self, request: dict[str, Any]) -> AsyncIterator[Any]:
        async for chunk in self.generate_stream(
            request["prompt"],
            max_tokens=request.get("max_tokens", DEFAULT_MAX_TOKENS),
        ):
            yield chunk
