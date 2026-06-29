# syntax=docker/dockerfile:1.7

ARG VLLM_IMAGE=vllm/vllm-openai:v0.23.0@sha256:6d8429e38e3747723ca07ee1b17972e09bb9c51c4032b266f24fb1cc3b22ed8f
FROM ${VLLM_IMAGE}

ENV HOST=0.0.0.0 \
    PORT=8000 \
    MODEL_PATH=/models/model \
    HF_HUB_OFFLINE=1 \
    TRANSFORMERS_OFFLINE=1 \
    PYTHONUNBUFFERED=1

COPY --chmod=755 runtimes/vllm/entrypoint.sh /opt/inferops/entrypoint.sh

EXPOSE 8000
ENTRYPOINT ["/opt/inferops/entrypoint.sh"]
