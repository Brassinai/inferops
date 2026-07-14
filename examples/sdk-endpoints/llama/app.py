from __future__ import annotations

from collections.abc import AsyncIterator
from typing import Any

import inferops


app = inferops.App("llama-sdk-endpoints")
DEFAULT_MAX_TOKENS = 96


@app.model(
    name="assistant-llama",
    engine="llama-cpp",
    model="jc-builds/SmolLM2-135M-Instruct-Q4_K_M-GGUF",
    gpu=None,
    cpu="4",
    memory="2Gi",
    min_replicas=0,
    max_replicas=1,
    max_model_len=512,
    cache_size="1Gi",
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
