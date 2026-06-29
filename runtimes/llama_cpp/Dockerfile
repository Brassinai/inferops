# syntax=docker/dockerfile:1.7

ARG LLAMA_CPP_IMAGE=ghcr.io/ggml-org/llama.cpp:server@sha256:565c0f60c9357967aa2af3d3b90d9905bc04a3b5ff469bc322aa801086bc3f5e
FROM ${LLAMA_CPP_IMAGE}

ENV HOST=0.0.0.0 \
    PORT=8000 \
    MODEL_PATH=/models/model

COPY --chmod=755 runtimes/llama_cpp/entrypoint.sh /opt/inferops/entrypoint.sh

EXPOSE 8000
ENTRYPOINT ["/opt/inferops/entrypoint.sh"]
