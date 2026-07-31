# Decorator Deploy

This example declares a `ModelDeployment` through the Python SDK. The
decorated class is metadata only; the selected runtime image owns inference
and exposes its OpenAI-compatible API.

## CPU llama.cpp

Use `app_llama.py` for local CPU testing on OrbStack:

```bash
inferops install \
  --context orbstack \
  --profile homelab \
  --cache-path /var/lib/inferops/models \
  --cache-capacity 2Gi
inferops deploy examples/decorator-deploy/app_llama.py --context orbstack
inferops activate cpu-smollm-decorator --context orbstack --timeout 10m
inferops status cpu-smollm-decorator --context orbstack --watch --timeout 10m
```

Then forward the gateway and call the OpenAI-compatible route:

```bash
inferops gateway forward --context orbstack

curl -X POST http://127.0.0.1:8080/models/cpu-smollm-decorator/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"cpu-smollm-decorator","messages":[{"role":"user","content":"Say hello from CPU llama.cpp."}]}'
```

The CPU app uses the small GGUF model from the YAML smoke test and sets
`gpu=None`, so it does not request Kubernetes GPU resources.

## Shared Cache

Use `app_shared_cache.py` to declare two CPU deployments that reference the
same model repository, revision, cache root, and Secret boundary:

```bash
inferops deploy examples/decorator-deploy/app_shared_cache.py --context orbstack
inferops cache list --context orbstack
```

Both `cpu-smollm-a` and `cpu-smollm-b` should reference one `mode=shared`
cache. Changing either deployment's `model_revision` or
`hugging_face_token_secret_name` creates a separate cache.

## GPU vLLM

Use `app.py` for GPU testing with vLLM:

```bash
inferops install --profile homelab --compute-profile nvidia-gpu \
  --cache-path /var/lib/inferops/models \
  --cache-capacity 80Gi
inferops deploy examples/decorator-deploy/app.py --context <cluster-context>
inferops activate gpu-vllm-qwen --context <cluster-context> --timeout 20m
inferops status gpu-vllm-qwen --context <cluster-context> --watch --timeout 20m
```

## Clean Up

Release compute and delete the decorator deployment when you are done:

```bash
inferops deactivate cpu-smollm-decorator --context orbstack
inferops delete cpu-smollm-decorator --context orbstack
```

For the GPU example, use the same commands with your cluster context:

```bash
inferops deactivate gpu-vllm-qwen --context <cluster-context>
inferops delete gpu-vllm-qwen --context <cluster-context>
```

`inferops delete` preserves model caches. To remove cache records too, list
them and delete the matching cache name:

```bash
inferops cache list --context orbstack
inferops cache delete <cache-name> --context orbstack
```
