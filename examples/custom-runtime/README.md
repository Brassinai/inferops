# Custom Runtime

Example placeholder for declaring a `ModelRuntime` adapter. InferOps defaults
to nano-vLLM and is designed to support vLLM, SGLang, llama.cpp, and other
conforming OpenAI-compatible runtimes.

A custom image must own its inference server, OpenAI-compatible API, health,
readiness, metrics, signal handling, and model-format support. InferOps passes
the prepared `MODEL_PATH` and translates shared settings; it does not provide a
fallback inference server or download models inside the runtime pod.
