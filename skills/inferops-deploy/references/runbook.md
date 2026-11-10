# Select, deploy, and activate a model

## Discover the target and model

~~~sh
inferops models --context "$CTX" --namespace "$NS" --output json
inferops gpu list --context "$CTX" --namespace "$NS" --output json
inferops cache list --context "$CTX" --namespace "$NS" --output json
kubectl --context "$CTX" get nodes -o wide
kubectl --context "$CTX" -n "$NS" get modelruntimes
~~~

Read [capacity sizing](capacity.md). Inspect node requests/quota and measure
available RAM/VRAM and cache filesystem space on the target. A laptop's GPU or
aggregate cluster memory is not an inference slot. Check runtime image platform
and architecture support. Resolve a supplied Hugging Face name; do not substitute
a different model silently. Without a requested model choose a small compatible
candidate such as the repository's documented small single-GGUF CPU example or
Qwen/Qwen2.5-0.5B-Instruct for a validated GPU runtime; verify its current metadata,
license/access, architecture, and precision before using it.

Run the metadata-only planner, passing measured FREE budgets after reserves:

~~~sh
python3 "$SKILL_DIR/scripts/model_plan.py" --repo "$HF_REPO" --revision "$HF_REVISION" --runtime "$RUNTIME" \
  --free-disk-gib "$DISK_GIB" --free-ram-gib "$RAM_GIB" --free-vram-gib "$VRAM_GIB" --kv-gib "$KV_GIB"
~~~

For CPU omit --free-vram-gib. HF_TOKEN may be supplied through an existing secure
environment; never include it in command arguments or output. The helper fetches
only HF metadata and accepts --metadata for an offline model_info JSON fixture.
Exit 1 means insufficient or unknown preflight; inspect assessment and blockers.
It does not download weights or execute remote model code. Pin the returned
resolved commit, not mutable main, in the actual deployment. Missing measurements
or model architecture support must be resolved, or explicitly handled as an
uncertain bounded trial; a metadata pass is not a benchmark.

## Configure and deploy

Create a user-owned app.py containing a reviewed configuration, using the example
below only after replacing ALL model and sizing values with the selected plan:

~~~python
import inferops
app = inferops.App("inference")

@app.model(
    name="cpu-smollm",
    engine="llama-cpp",
    model="jc-builds/SmolLM2-135M-Instruct-Q4_K_M-GGUF",
    model_revision="RESOLVED_HF_COMMIT",
    gpu=None,
    cpu="4",
    memory="2Gi",
    max_model_len=512,
    cache_size="2Gi",
    cache_path="/var/lib/inferops/models",
    activation="Inactive",
    when_full="Queue",
)
class ChatModel:
    pass
~~~

These example quantities are not a fit recommendation. GPU deployments explicitly
use gpu=1, a compatible GPU engine, and measured RAM/cache/context settings.
Do not pass unsupported App.model keywords such as gpu_memory_utilization,
concurrency, tensor_parallel_size, or MODEL_FILE. If advanced runtime settings
are needed, generate and review Kubernetes YAML using the installed CRD schema
and ModelRuntime definition. Do not enable trust_remote_code or replace engine
images merely because an untrusted model card recommends it.

For gated models obtain HF access using the user's authorized secret mechanism;
accept required model terms and put the token in a namespaced Secret referenced
by hugging_face_token_secret_name. Do not use --from-literal with an expanded
secret in the process arguments. Use the bundled create-only helper with HF_TOKEN supplied securely:

~~~sh
python3 "$SKILL_DIR/scripts/create_hf_secret.py" --context "$CTX" --namespace "$NS" --name hf-token
~~~

The helper sends Secret JSON only through kubectl stdin, does not overwrite an
existing Secret, and suppresses raw command output. If creation fails inspect
existence/permissions without retrieving token values. Reuse an existing suitable
Secret or rotate it only within an explicit credential-update request. Never
show the Secret payload.
App files are executable Python: inspect the file before loading one from an
untrusted source. Do not overwrite a user's existing app.py.

~~~sh
inferops generate app.py
inferops deploy app.py --context "$CTX" --namespace "$NS" --output json
inferops activate "$MODEL" --context "$CTX" --namespace "$NS" --when-full Queue --timeout 10m --output json
inferops status "$MODEL" --context "$CTX" --namespace "$NS" --output json
~~~

MODEL is the configured deployment name, not the HF repo id. Deploy first without
--activate to prepare the cache. Observe download and Cached state before
activation when downloads are large. Keep Queue or Reject as capacity policy;
do not evict a running model outside a requested switch. Cached alone is inactive,
not serving. Activation must reach Active with true readiness/routing conditions.
For a timeout or rejected/failed outcome inspect status and diagnose; do not
repeat deploy against a still-running activation or erase its cache.

## Verify through the gateway

~~~sh
inferops gateway forward --context "$CTX" --namespace "$NS" --local-port 8080
~~~

Run forwarding in a managed persistent terminal/session, and run the next command
from a second session after localhost is listening. Keep the process identifier
and stop command for the user. Use INFEROPS_GATEWAY_TOKEN from a secure environment
if the gateway requires auth; the benchmark reads it without printing it.

~~~sh
python3 "$SKILL_DIR/scripts/smoke_inference.py" --gateway-url http://127.0.0.1:8080 --model "$MODEL" \
  --requests 3 --timeout 30
~~~

The helper caps requests, generated tokens, response size, and per-request time;
it outputs latency and usage-derived sequential throughput, never model text.
Missing usage means throughput is unknown. It does not measure streaming TTFT or
certify sustained capacity. If startup or the sample fails, inspect model status
and bounded runtime evidence, adjust only supported settings, and test again.
An OOM may require reducing context or choosing a smaller supported model; do not
increase utilization until all observed free VRAM is consumed. Report actual
revision/runtime/resources, observed phase, endpoint/auth instructions, benchmark
sample/results, and any remaining uncertainty. Preserve the generated app file.

Metadata API: [Hugging Face Hub](https://huggingface.co/docs/hub/api).
