#!/bin/sh
# Translate the InferOps runtime contract to llama.cpp's server CLI.
set -eu

if [ -z "${MODEL_PATH:-}" ]; then
    echo "error: MODEL_PATH must point to a prepared model-cache directory" >&2
    exit 1
fi
if [ ! -d "$MODEL_PATH" ] || [ ! -r "$MODEL_PATH" ] || [ ! -x "$MODEL_PATH" ]; then
    echo "error: MODEL_PATH is not an accessible directory: $MODEL_PATH" >&2
    exit 1
fi

if [ -n "${MODEL_FILE:-}" ]; then
    case "$MODEL_FILE" in
        /*|*/*)
            echo "error: MODEL_FILE must be a filename relative to MODEL_PATH" >&2
            exit 1
            ;;
        *) model_file="$MODEL_PATH/$MODEL_FILE" ;;
    esac
else
    model_file=""
    model_count=0
    for candidate in "$MODEL_PATH"/*.gguf; do
        [ -f "$candidate" ] || continue
        model_file="$candidate"
        model_count=$((model_count + 1))
    done
    if [ "$model_count" -ne 1 ]; then
        echo "error: MODEL_PATH must contain exactly one .gguf file when MODEL_FILE is unset" >&2
        exit 1
    fi
fi

if [ ! -f "$model_file" ] || [ ! -r "$model_file" ]; then
    echo "error: model GGUF file is not readable: $model_file" >&2
    exit 1
fi

if [ -n "${LLAMA_SERVER_BIN:-}" ]; then
    server_bin="$LLAMA_SERVER_BIN"
elif [ -x /app/llama-server ]; then
    server_bin=/app/llama-server
elif [ -x /llama.cpp/bin/llama-server ]; then
    server_bin=/llama.cpp/bin/llama-server
else
    echo "error: llama-server executable was not found in the engine image" >&2
    exit 1
fi

set -- \
    --model "$model_file" \
    --host "${HOST:-0.0.0.0}" \
    --port "${PORT:-8000}" \
    --metrics \
    "$@"

if [ -n "${MODEL_REPO:-}" ]; then
    set -- "$@" --alias "$MODEL_REPO"
fi
if [ -n "${MAX_MODEL_LEN:-}" ]; then
    set -- "$@" --ctx-size "$MAX_MODEL_LEN"
fi
if [ -n "${MAX_NUM_SEQS:-}" ]; then
    set -- "$@" --parallel "$MAX_NUM_SEQS"
fi
if [ -n "${CPU_THREADS:-}" ]; then
    set -- "$@" --threads "$CPU_THREADS"
fi
if [ -n "${CPU_THREADS_BATCH:-}" ]; then
    set -- "$@" --threads-batch "$CPU_THREADS_BATCH"
fi

exec "$server_bin" "$@"
