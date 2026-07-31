import inferops

app = inferops.App("shared-cache-demo")


@app.model(
    name="cpu-smollm-a",
    engine="llama-cpp",
    model="jc-builds/SmolLM2-135M-Instruct-Q4_K_M-GGUF",
    model_revision="main",
    gpu=None,
    cpu="2",
    memory="4Gi",
    min_replicas=0,
    max_replicas=1,
    max_model_len=1024,
    cache_size="2Gi",
    cache_path="/var/lib/inferops/models",
)
class SmolLMA:
    pass


@app.model(
    name="cpu-smollm-b",
    engine="llama-cpp",
    model="jc-builds/SmolLM2-135M-Instruct-Q4_K_M-GGUF",
    model_revision="main",
    gpu=None,
    cpu="2",
    memory="4Gi",
    min_replicas=0,
    max_replicas=1,
    max_model_len=1024,
    cache_size="2Gi",
    cache_path="/var/lib/inferops/models",
)
class SmolLMB:
    pass
