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
    def __init__(self):
        from nanovllm import LLM
        self.llm = LLM("/models/qwen", tensor_parallel_size=1)

    @inferops.web_endpoint(method="POST", path="/chat")
    def chat(self, request):
        return self.llm.generate([request["prompt"]])
