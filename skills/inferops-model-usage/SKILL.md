---
name: inferops-model-usage
description: Show commands to list locally deployed InferOps models, inspect their endpoints, forward the local gateway, and call a model without InferCloud. Use for command-only model access instructions; do not execute commands or change the cluster.
---

# Show local model usage commands

Return copyable commands only, with short shell comments where needed. Do not
execute discovery, port-forwarding, inference, or any other commands when this
skill is invoked. Use model names, context, namespace, ports and capabilities
already established in the conversation. When unknown, show clearly marked
values for the user to replace and commands to discover them; do not invent
deployed models or claim an endpoint was tested.

This workflow calls the self-hosted InferOps gateway. It does not require an
InferCloud account, cloud connection, cloud model listing or cloud API key.
Authentication, when enabled locally, uses the gateway's own token. Do not
deploy, activate, repair, upgrade, expose publicly or connect anything to cloud.
If the user asks for actual execution, that is outside this command-only skill.

## List deployments and endpoints

InferOps options follow the subcommand. Show readable listings by default, adding
--output json when requested. A ModelDeployment can exist without serving:
Cached/Inactive is not Active/readiness. Use its deployment name for MODEL, not
the Hugging Face repository name. Namespace defaults to inferops-system only
when no namespace has been established; the selected deployment may be elsewhere.

~~~sh
# Replace context/namespace with the existing deployment target when known.
kubectl config get-contexts -o name
export CTX="$(kubectl config current-context)"
export NS="inferops-system"
inferops models --context "$CTX" --namespace "$NS"
inferops endpoints --context "$CTX" --namespace "$NS"
export MODEL="replace-with-deployment-name"
inferops status "$MODEL" --context "$CTX" --namespace "$NS"
~~~

If the namespace is unknown, optionally show kubectl get modeldeployments -A
as a discovery command when the user has cluster-wide listing access. Preserve
an established kubeconfig path consistently with --kubeconfig; never show its
contents or change the global current context. Include only commands relevant
to the user's question, not every optional variant.

## Reach the local gateway

Use an established reachable gateway URL when one is known. Otherwise show a
localhost forward in one terminal, then the request commands in another. The
default Service/remote port is inferops-gateway:80; substitute known custom
values with --service/--remote-port. If uncertain, include the Service inspection
command rather than claim those defaults were verified. Offer another local
port on conflict; do not kill an unrelated process.

~~~sh
# Terminal 1; keep running. Ctrl-C stops the forward.
kubectl --context "$CTX" -n "$NS" get service inferops-gateway
inferops gateway forward --context "$CTX" --namespace "$NS" --local-port 8080
~~~

## Call a deployment

The local route is /models/DEPLOYMENT_NAME/v1/chat/completions. Set MODEL and
GATEWAY_URL in the request terminal as well; exports do not carry across separate
terminals. Substitute known non-secret model names and origins in the final
commands so the user need not replace them again. Localhost is the machine
running the forward, and the endpoint lasts only while forwarding runs.

For a known unauthenticated local gateway, show this curl request:

~~~sh
# Terminal 2; use the name printed by inferops models.
export MODEL="replace-with-deployment-name"
export GATEWAY_URL="http://127.0.0.1:8080"
curl --fail-with-body --max-time 60 \
  "$GATEWAY_URL/models/$MODEL/v1/chat/completions" \
  -H 'Content-Type: application/json' \
  -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello\"}],\"max_tokens\":64}"
~~~

For an authenticated gateway or unknown authentication state, prefer this Python
shell command. Read an already securely supplied INFEROPS_GATEWAY_TOKEN without
putting a bearer token in process arguments; never print or invent a real token.
Do not use an InferCloud consumer/provider key as the local gateway token.
For text, leave IMAGE_PATH unset. When vision is requested or established, also
show export IMAGE_PATH="/path/to/image.png" before this command and explain in a
shell comment that it requires a deployed vision-capable model. Do not assume a
text model accepts images. Use a known synthetic test PNG path when available.

~~~sh
# Terminal 2; gateway token, if required, must already be supplied securely.
export MODEL="replace-with-deployment-name"
export GATEWAY_URL="http://127.0.0.1:8080"
python3 - <<'PY'
import base64
import json
import os
from pathlib import Path
import urllib.parse
import urllib.request


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


model = os.environ["MODEL"]
origin = os.environ["GATEWAY_URL"].rstrip("/")
image_path = os.environ.get("IMAGE_PATH")
content = "Hello"
if image_path:
    image = base64.b64encode(Path(image_path).read_bytes()).decode("ascii")
    content = [
        {"type": "text", "text": "Describe this image."},
        {"type": "image_url", "image_url": {"url": "data:image/png;base64," + image}},
    ]
headers = {"Content-Type": "application/json"}
token = os.environ.get("INFEROPS_GATEWAY_TOKEN")
if token:
    headers["Authorization"] = "Bearer " + token
request = urllib.request.Request(
    origin + "/models/" + urllib.parse.quote(model, safe="") + "/v1/chat/completions",
    data=json.dumps({"model": model, "messages": [{"role": "user", "content": content}], "max_tokens": 64}).encode(),
    headers=headers,
)
with urllib.request.build_opener(NoRedirect).open(request, timeout=60) as response:
    print(json.load(response)["choices"][0]["message"]["content"])
PY
~~~

Return the appropriate invocation example, not both auth variants by default.
When helpful, add short comments: 401 checks local gateway authentication,
404 checks deployment name/route, 503 checks readiness, and connection refused
checks the forward/port. Show inspect/status commands rather than activation or
repair commands. Do not report discovered models, successful inference, or an
established forward when only displaying commands.
