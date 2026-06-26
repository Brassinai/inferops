# syntax=docker/dockerfile:1.7

ARG FAKE_BASE_IMAGE=python:3.11-slim
ARG REAL_BASE_IMAGE=pytorch/pytorch:2.9.1-cuda13.0-cudnn9-devel
ARG UVICORN_VERSION=0.49.0
ARG NANOVLLM_REPO=https://github.com/Brassinai/nano-vllm.git
ARG NANOVLLM_COMMIT=a7ba517a4fc2921a6d0ba44763df087628b50363

FROM ${FAKE_BASE_IMAGE} AS fake
ARG UVICORN_VERSION
WORKDIR /opt/inferops
ENV FAKE_MODE=true \
    HOST=0.0.0.0 \
    PORT=8000 \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1
RUN python -m pip install --no-cache-dir "uvicorn==${UVICORN_VERSION}"
COPY runtimes /opt/inferops/runtimes
USER 65532:65532
EXPOSE 8000
ENTRYPOINT ["python", "-m", "runtimes.nano_vllm.server"]

FROM ${REAL_BASE_IMAGE} AS real
ARG UVICORN_VERSION
ARG NANOVLLM_REPO
ARG NANOVLLM_COMMIT
WORKDIR /opt/inferops
ENV FAKE_MODE=false \
    HOST=0.0.0.0 \
    PORT=8000 \
    HOME=/tmp/inferops \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1
RUN apt-get update && \
    apt-get install --no-install-recommends --yes ca-certificates git && \
    rm -rf /var/lib/apt/lists/* && \
    python -m pip install --no-cache-dir \
        "uvicorn==${UVICORN_VERSION}" \
        "git+${NANOVLLM_REPO}@${NANOVLLM_COMMIT}" && \
    groupadd --gid 65532 inferops && \
    useradd --uid 65532 --gid 65532 --home-dir /tmp/inferops --shell /usr/sbin/nologin inferops && \
    mkdir -p /tmp/inferops && chown 65532:65532 /tmp/inferops
RUN chown --recursive 65532:65532 /tmp/inferops
COPY runtimes /opt/inferops/runtimes
ENV CUDA_HOME=/usr/local/cuda \
    PATH=/usr/local/cuda/bin:${PATH} \
    LD_LIBRARY_PATH=/usr/local/cuda/lib64:${LD_LIBRARY_PATH}
USER 65532:65532
EXPOSE 8000
ENTRYPOINT ["python", "-m", "runtimes.nano_vllm.server"]
