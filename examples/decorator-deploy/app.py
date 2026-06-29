import inferops

app = inferops.App("customer-support-llm")


@app.model(
    name="qwen-chat",
    engine="nano-vllm",
    model="Qwen/Qwen2.5-7B-Instruct",
    gpu="L4",
    min_replicas=0,
    max_replicas=4,
    max_model_len=4096,
)
class QwenChat:
    pass
