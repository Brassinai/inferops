#!/bin/sh
# Translate the InferOps runtime contract to SGLang's server CLI.
set -eu

if [ -z "${MODEL_PATH:-}" ]; then
    echo "error: MODEL_PATH must point to a prepared model-cache directory" >&2
    exit 1
fi
if [ ! -d "$MODEL_PATH" ] || [ ! -r "$MODEL_PATH" ] || [ ! -x "$MODEL_PATH" ]; then
    echo "error: MODEL_PATH is not an accessible directory: $MODEL_PATH" >&2
    exit 1
fi

set -- -m sglang.launch_server \
    --model-path "$MODEL_PATH" \
    --host "${HOST:-0.0.0.0}" \
    --port "${PORT:-8000}" \
    --enable-metrics \
    "$@"

if [ -n "${MODEL_REPO:-}" ]; then
    set -- "$@" --served-model-name "$MODEL_REPO"
fi
if [ -n "${TENSOR_PARALLEL_SIZE:-}" ]; then
    set -- "$@" --tp-size "$TENSOR_PARALLEL_SIZE"
fi
if [ -n "${MODEL_DTYPE:-}" ]; then
    set -- "$@" --dtype "$MODEL_DTYPE"
fi
if [ -n "${MAX_MODEL_LEN:-}" ]; then
    set -- "$@" --context-length "$MAX_MODEL_LEN"
fi
if [ -n "${GPU_MEMORY_UTILIZATION:-}" ]; then
    set -- "$@" --mem-fraction-static "$GPU_MEMORY_UTILIZATION"
fi
if [ -n "${MAX_NUM_SEQS:-}" ]; then
    set -- "$@" --max-running-requests "$MAX_NUM_SEQS"
fi

case "${ENFORCE_EAGER:-false}" in
    true|1)
        set -- "$@" --disable-prefill-cuda-graph --disable-decode-cuda-graph
        ;;
    false|0|"") ;;
    *)
        echo "error: ENFORCE_EAGER must be true or false" >&2
        exit 1
        ;;
esac

exec python3 "$@"
