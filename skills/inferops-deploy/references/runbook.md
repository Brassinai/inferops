# Select, deploy, and activate a model

## Follow the requested outcome

An explicit $inferops-deploy invocation authorizes selecting, configuring,
downloading, deploying, activating, and testing a model. "Find the best model for
this system" is selection within that workflow, not a reason to stop at a report.
Respect explicit assessment-only/no-deploy instructions. Preserve the intended
outcome across short follow-ups and resume a saved assessment after rechecking
its resource measurements and pinned artifacts; do not ask for a preferred model
again if the user delegated that choice. A normal bounded startup/inference test
within measured resource limits is part of deployment. Installing a new platform,
restarting a shared VM, enlarging its allocation, or stopping unrelated workloads
needs the corresponding scope; do not assume those are model-deployment steps.

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
a different model silently. Without a requested model, compare a small shortlist
that meets the requested engine and capabilities, verify current metadata,
license/access, architecture and precision, then select the best feasible
candidate. A vision requirement excludes text-only fallbacks. If the first
candidate cannot fit, assess a smaller supported model or quantization that still
meets the request before declaring the task blocked. Do not claim a globally
best model from metadata; call it the chosen candidate until real tests succeed.
Continue into deployment once a feasible candidate is selected.

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

## Plan selected GGUF artifacts for vision

For llama.cpp vision, resolve the exact language-model GGUF and matching mmproj
from one pinned snapshot. Verify that the actual installed llama-server build
supports that model/projector architecture; an ARM64 image manifest does not
establish multimodal support. Inspect the trusted engine's version/help or source
compatibility evidence without downloading weights or executing remote model code.
Do not infer projector compatibility from its filename alone.

Use explicit selection for repositories containing multiple quantizations and
projectors. The planner counts the WHOLE repository for disk/download staging,
but only the chosen language model and projector for runtime weight memory:

~~~sh
python3 "$SKILL_DIR/scripts/model_plan.py" --repo "$HF_REPO" --revision "$HF_REVISION" --runtime llama-cpp \
  --model-file "$MODEL_FILE" --mmproj-file "$MMPROJ_FILE" \
  --free-disk-gib "$DISK_GIB" --free-ram-gib "$RAM_GIB" --kv-gib "$KV_GIB" \
  --vision-workspace-gib "$VISION_WORKSPACE_GIB" --runtime-overhead-gib "$RUNTIME_OVERHEAD_GIB"
~~~

Choose those filenames and budgets from evidence. The default runtime overhead
is a conservative 2Gi estimate, not a measured minimum. A different overhead
estimate needs engine/model evidence; do not reduce it merely to force a pass.
Vision workspace accounts for encoder/processor work in addition to weights and
KV. An unknown workspace stays unknown. Subtract system/workload reserve once
before passing free RAM; do not add that reserve again as runtime overhead.
Report policy reserve, selected weights, runtime overhead, KV and vision workspace
separately so a conservative estimate is not mistaken for proven impossibility.

If exact runtime memory remains uncertain but a defensible candidate fits within
a measured hard budget, a resource-limited startup trial is included in the
deployment request. Keep a low supported context/image size and concurrency,
observe OOM/readiness, and stop the new workload if it fails. Never use a trial
to bypass insufficient physical/schedulable memory, evict another workload, or
increase host/VM limits outside scope. If no candidate fits, give the measured
shortfall and concrete resource change needed rather than ask for a model choice.

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

Vision runtime configuration is ordinary work for this deployment; create it
rather than returning "needs MODEL_FILE/--mmproj" as a user task. Copy the
supported spec fields from the installed llama-cpp ModelRuntime into a NEW
namespaced, model-specific ModelRuntime, retaining its pinned image, engine,
protocol, command, health/readiness/metrics paths and required environment.
Remove copied status/server metadata and give the new resource a distinct name.
Set spec.env.MODEL_FILE to the selected language GGUF and append spec.args
["--mmproj", "/models/model/<selected-projector.gguf>"] after checking the actual
container mount path. Use only flags supported by that pinned engine. Preserve
any required existing arguments and never edit the shared llama-cpp runtime.
The planner's requires_runtime_configuration is an action to complete, not an
unsupported-model blocker when the installed schema supports it.

Point the generated ModelDeployment's spec.runtime.ref at that dedicated runtime.
The SDK's engine field is that runtime reference (the ModelRuntime's spec.engine
remains llama-cpp); if an older SDK cannot express it, use a reviewed generated
manifest and the installed CRD. Validate both manifests with server-side dry-run,
then apply the runtime before deploying the model. Do not pass MODEL_FILE or
mmproj as invented App.model keywords, run llama-server -hf around the cache,
or change the whole-snapshot download contract to fit a selected-file estimate.
After caching, verify both files exist at the paths the container will use.

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

For a vision request, ALSO run image-bearing checks using two small public or
synthetic PNGs with different known dominant colors. Generate them locally with
a standard image tool; use no private photos. Supply their known answers only
to the checker, not in the inference prompt. For each PNG:

~~~sh
python3 "$SKILL_DIR/scripts/smoke_inference.py" --gateway-url http://127.0.0.1:8080 --model "$MODEL" \
  --image "$VISION_TEST_PNG" --expect-text "$EXPECTED_COLOR" --requests 1 --timeout 60
~~~

The helper embeds the PNG as an OpenAI image_url content part, checks the expected
answer without printing image/prompt/response content, and reports metrics only.
A protocol success without a correct visual answer is not vision verification.
Require different correct answers for the contrasting test images; a text-only
reply, generic greeting, or a model returning the same answer to both does not
satisfy the request. These bounded checks validate the sampled image behavior,
not broad accuracy, video support, cloud eligibility or capability attestation.

The helper caps requests, generated tokens, response size, image bytes/pixels, and per-request time;
it outputs latency and usage-derived sequential throughput, never model text.
Missing usage means throughput is unknown. It does not measure streaming TTFT or
certify sustained capacity. If startup or the sample fails, inspect model status
and bounded runtime evidence, adjust only supported settings, and test again.
An OOM may require reducing context or choosing a smaller supported model; do not
increase utilization until all observed free VRAM is consumed. Report actual
revision/runtime/resources, observed phase, endpoint/auth instructions, benchmark
sample/results, vision test results when requested, and any remaining uncertainty.
Completion requires Active/readiness and working inference with the requested
capabilities. Otherwise report the exact blocker, attempted scoped remedies and
what remains. Preserve the generated app/runtime manifests and resolved artifact
selection so a follow-up can resume without repeating discovery.

## Return working model-call examples

Deployment completion includes showing the user how to call the model. Put the
examples directly in the final answer, using the actual deployment name, verified
gateway origin/port, and tested route. Do not leave generic my-model values or
unresolved targeting variables, and do not substitute the HF repository id for
the deployment name. The model route is
/models/DEPLOYMENT_NAME/v1/chat/completions. A helper/benchmark command or a link
to a generated file alone does not satisfy this handoff.

Return observed Active/readiness and successful inference, the model name, full
API URL, authentication requirement, and a copyable text request. For an
unauthenticated LOCAL gateway, adapt this curl example:

~~~sh
curl --fail-with-body --max-time 60 \
  http://127.0.0.1:8080/models/my-model/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"my-model","messages":[{"role":"user","content":"Hello"}],"max_tokens":64}'
~~~

For authenticated calls, use a Python example that reads an already securely
supplied INFEROPS_GATEWAY_TOKEN instead of expanding a token in curl arguments.
Never paste a real token into the answer. This stdlib example also works without
auth when the environment variable is absent on an unauthenticated local gateway:

~~~python
import json
import os
import urllib.request

url = "http://127.0.0.1:8080/models/my-model/v1/chat/completions"
model = "my-model"  # substitute the actual deployment name
messages = [{"role": "user", "content": "Hello"}]
headers = {"Content-Type": "application/json"}
token = os.environ.get("INFEROPS_GATEWAY_TOKEN")
if token:
    headers["Authorization"] = "Bearer " + token
request = urllib.request.Request(
    url,
    data=json.dumps({"model": model, "messages": messages, "max_tokens": 64}).encode(),
    headers=headers,
)
with urllib.request.urlopen(request, timeout=60) as response:
    print(json.load(response)["choices"][0]["message"]["content"])
~~~

For a deployed vision model, ALSO include a complete image-call example in the
final answer. Adapt the Python request above with the following messages and
imports, and present the complete runnable code rather than making the user
assemble fragments. Use an existing public/synthetic verification PNG path for a
ready-to-run example, noting that the user can replace it with their own local
PNG. Reading an image path should be the only user edit needed to choose a new
image; the actual model name and URL must already be filled in.

~~~python
import base64
from pathlib import Path

image = base64.b64encode(Path("/absolute/path/to/verified-test.png").read_bytes()).decode("ascii")
messages = [{"role": "user", "content": [
    {"type": "text", "text": "Describe this image."},
    {"type": "image_url", "image_url": {"url": "data:image/png;base64," + image}},
]}]
~~~

Examples are for the user to run; keep their generated text and private images
out of diagnostic logs. State which route and sampled capabilities were actually
tested. Use a demonstrated timeout/token budget when the model needs one instead
of treating the illustrative 60 seconds/64 tokens as a performance guarantee.
State that a localhost port-forward works while its managed session is running,
give its exact stop method, and provide the restart command with the actual
context, namespace, service/remote port when nondefault, and local port. Do not
claim persistent hosting or expose a new public endpoint as part of the handoff.
If deployment or verification is blocked, label any example as unverified and
explain what must be resolved before it will work.

Sources: [Hugging Face metadata API](https://huggingface.co/docs/hub/api),
[llama.cpp multimodal configuration](https://github.com/ggml-org/llama.cpp/blob/master/docs/multimodal.md).
