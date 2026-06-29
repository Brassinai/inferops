# syntax=docker/dockerfile:1.7

# The engine image is supplied by the nano-vLLM release pipeline. InferOps adds
# only the environment-to-CLI adapter.
ARG NANOVLLM_IMAGE
FROM ${NANOVLLM_IMAGE}

ENV HOST=0.0.0.0 \
    PORT=8000 \
    MODEL_PATH=/models/model \
    HF_HUB_OFFLINE=1 \
    TRANSFORMERS_OFFLINE=1 \
    PYTHONUNBUFFERED=1

COPY --chmod=755 runtimes/nano_vllm/entrypoint.sh /opt/inferops/entrypoint.sh

EXPOSE 8000
ENTRYPOINT ["/opt/inferops/entrypoint.sh"]
