FROM python:3.11-slim@sha256:fc39d2e11b2d542e4f8baf3711a872b3f856b37c2210d36e7d8f8ae037f54b6a

# Pin huggingface_hub for reproducible downloads.
ARG HF_HUB_VERSION=0.23.4
RUN pip install --no-cache-dir "huggingface_hub==${HF_HUB_VERSION}"

RUN groupadd -r inferops -g 65532 && \
    useradd -r -g inferops -u 65532 -m -d /home/inferops inferops

COPY download.py /opt/inferops/download.py
RUN chmod 755 /opt/inferops/download.py

USER 65532:65532
WORKDIR /home/inferops

ENTRYPOINT ["python", "/opt/inferops/download.py"]
