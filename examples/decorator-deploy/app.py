import inferops

app = inferops.App("customer-support-llm")


@app.model(
    name="gpu-vllm-qwen",
    engine="vllm",
    model="Qwen/Qwen2.5-0.5B-Instruct",
    gpu=1,
    cpu="4",
    memory="16Gi",
    min_replicas=0,
    max_replicas=1,
    max_model_len=1024,
    cache_size="10Gi",
)
class QwenChat:
    pass
