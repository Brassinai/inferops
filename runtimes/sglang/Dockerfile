# syntax=docker/dockerfile:1.7

ARG SGLANG_IMAGE=lmsysorg/sglang:v0.5.14@sha256:5027e95bf6ec536856b1b52a91d1f35ff5c564ab83e8a94758a169ff09bb8df3
FROM ${SGLANG_IMAGE}

ENV HOST=0.0.0.0 \
    PORT=8000 \
    MODEL_PATH=/models/model \
    HF_HUB_OFFLINE=1 \
    TRANSFORMERS_OFFLINE=1 \
    PYTHONUNBUFFERED=1

COPY --chmod=755 runtimes/sglang/entrypoint.sh /opt/inferops/entrypoint.sh

EXPOSE 8000
ENTRYPOINT ["/opt/inferops/entrypoint.sh"]
