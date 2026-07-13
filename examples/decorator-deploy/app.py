import inferops

app = inferops.App("customer-support-llm")


@app.model(
    name="gpu-vllm-qwen",
    engine="vllm",
    model="Qwen/Qwen2.5-7B-Instruct",
    gpu=1,
    min_replicas=0,
    max_replicas=1,
    max_model_len=4096,
    cache_size="50Gi",
)
class QwenChat:
    pass
