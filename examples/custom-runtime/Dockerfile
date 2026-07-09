# syntax=docker/dockerfile:1.7

FROM python:3.11-slim@sha256:fc39d2e11b2d542e4f8baf3711a872b3f856b37c2210d36e7d8f8ae037f54b6a

RUN groupadd -r inferops -g 65532 && \
    useradd -r -g inferops -u 65532 -m -d /home/inferops inferops

COPY examples/custom-runtime/requirements.txt /opt/inferops/requirements.txt
RUN pip install --no-cache-dir --requirement /opt/inferops/requirements.txt

COPY examples/custom-runtime/app.py /opt/inferops/app.py
COPY --chmod=755 examples/custom-runtime/entrypoint.sh /opt/inferops/entrypoint.sh

ENV HOST=0.0.0.0 \
    PORT=8000 \
    MODEL_PATH=/models/model \
    MODEL_REPO=custom-fastapi \
    PYTHONUNBUFFERED=1

USER 65532:65532
WORKDIR /opt/inferops
EXPOSE 8000
ENTRYPOINT ["/opt/inferops/entrypoint.sh"]
