from __future__ import annotations

import json
import os

import inferops


model = os.getenv("INFEROPS_MODEL", "assistant-vllm")
client = inferops.Client(
    base_url=os.getenv("INFEROPS_BASE_URL", f"http://127.0.0.1:8080/models/{model}"),
    api_key=os.getenv("INFEROPS_API_KEY") or None,
)

response = client.chat.completions.create(
    model=model,
    messages=[{"role": "user", "content": "Explain Kubernetes Services simply."}],
)
print(json.dumps(response, sort_keys=True))

stream = client.chat.completions.create(
    model=model,
    messages=[{"role": "user", "content": "Write a short poem."}],
    stream=True,
)
for event in stream:
    print(json.dumps(event, sort_keys=True))
