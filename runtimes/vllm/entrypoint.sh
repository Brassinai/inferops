#!/bin/sh
# Translate the InferOps runtime contract to vLLM's server CLI.
set -eu

if [ -z "${MODEL_PATH:-}" ]; then
    echo "error: MODEL_PATH must point to a prepared model-cache directory" >&2
    exit 1
fi
if [ ! -d "$MODEL_PATH" ] || [ ! -r "$MODEL_PATH" ] || [ ! -x "$MODEL_PATH" ]; then
    echo "error: MODEL_PATH is not an accessible directory: $MODEL_PATH" >&2
    exit 1
fi

set -- serve "$MODEL_PATH" \
    --host "${HOST:-0.0.0.0}" \
    --port "${PORT:-8000}" \
    "$@"

if [ -n "${MODEL_REPO:-}" ]; then
    set -- "$@" --served-model-name "$MODEL_REPO"
fi
if [ -n "${TENSOR_PARALLEL_SIZE:-}" ]; then
    set -- "$@" --tensor-parallel-size "$TENSOR_PARALLEL_SIZE"
fi
if [ -n "${MODEL_DTYPE:-}" ]; then
    set -- "$@" --dtype "$MODEL_DTYPE"
fi
if [ -n "${MAX_MODEL_LEN:-}" ]; then
    set -- "$@" --max-model-len "$MAX_MODEL_LEN"
fi
if [ -n "${GPU_MEMORY_UTILIZATION:-}" ]; then
    set -- "$@" --gpu-memory-utilization "$GPU_MEMORY_UTILIZATION"
fi
if [ -n "${MAX_NUM_SEQS:-}" ]; then
    set -- "$@" --max-num-seqs "$MAX_NUM_SEQS"
fi

case "${ENFORCE_EAGER:-false}" in
    true|1) set -- "$@" --enforce-eager ;;
    false|0|"") ;;
    *)
        echo "error: ENFORCE_EAGER must be true or false" >&2
        exit 1
        ;;
esac

exec vllm "$@"
