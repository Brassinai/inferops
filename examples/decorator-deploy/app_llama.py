import inferops

app = inferops.App("cpu-llama-smoke-test")


@app.model(
    name="cpu-smollm-decorator",
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
class SmolLMChat:
    pass
