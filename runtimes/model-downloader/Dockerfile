FROM python:3.11-slim@sha256:e031123e3d85762b141ad1cbc56452ba69c6e722ebf2f042cc0dc86c47c0d8b3

# Pin huggingface_hub for reproducible downloads.
ARG HF_HUB_VERSION=0.23.4
RUN pip install --no-cache-dir "huggingface_hub==${HF_HUB_VERSION}"

RUN groupadd -r inferops -g 65532 && \
    useradd -r -g inferops -u 65532 -m -d /home/inferops inferops

COPY runtimes/model-downloader/download.py /opt/inferops/download.py
RUN chmod 755 /opt/inferops/download.py

USER 65532:65532
WORKDIR /home/inferops

ENTRYPOINT ["python", "/opt/inferops/download.py"]
