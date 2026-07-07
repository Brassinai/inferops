#!/bin/sh
set -eu

if [ -z "${MODEL_PATH:-}" ]; then
    echo "error: MODEL_PATH must point to a prepared model-cache directory" >&2
    exit 1
fi
if [ ! -d "$MODEL_PATH" ] || [ ! -r "$MODEL_PATH" ] || [ ! -x "$MODEL_PATH" ]; then
    echo "error: MODEL_PATH is not an accessible directory: $MODEL_PATH" >&2
    exit 1
fi

exec "${PYTHON_BIN:-python}" -m uvicorn app:app \
    --host "${HOST:-0.0.0.0}" \
    --port "${PORT:-8000}" \
    --no-access-log \
    "$@"
