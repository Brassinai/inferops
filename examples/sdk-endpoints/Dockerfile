FROM python:3.11-slim

ARG APP_SOURCE=examples/sdk-endpoints/llama/app.py
ARG CONTAINER_APP_PATH=/app/app.py

WORKDIR /app

COPY sdk/python /tmp/inferops-sdk
COPY cli /tmp/inferops-cli
RUN pip install --no-cache-dir /tmp/inferops-sdk /tmp/inferops-cli

RUN mkdir -p "$(dirname "$CONTAINER_APP_PATH")"
COPY ${APP_SOURCE} ${CONTAINER_APP_PATH}

RUN useradd --create-home --uid 10001 inferops
USER 10001:10001

EXPOSE 8080

CMD ["inferops", "serve", "/app/app.py", "--host", "0.0.0.0", "--port", "8080"]
