# Forward the gateway and call a model

~~~sh
inferops models --context "$CTX" --namespace "$NS" --output json
inferops endpoints --context "$CTX" --namespace "$NS" --output json
inferops status "$MODEL" --context "$CTX" --namespace "$NS" --output json
kubectl --context "$CTX" -n "$NS" get service inferops-gateway
~~~

Select the intended deployment name, not its HF repository id. Require observed
Active/readiness and a routed endpoint; do not activate an inactive model unless
starting it is part of the request. Verify the actual gateway Service and port.
If its name differs, pass --service and the observed --remote-port (default 80).

~~~sh
inferops gateway forward --context "$CTX" --namespace "$NS" --local-port 8080
~~~

Use localhost binding. Run this in a managed persistent terminal/session rather
than a blocking one-shot command or a detached shell that dies at turn end.
Verify that forwarding is listening and keep the terminal/session id. Unlike the
Grafana command, gateway forward does not automatically pick another local port:
on a conflict use a free --local-port such as 8081 without killing an unrelated
process. Do not expose 0.0.0.0 unless requested and the exposure/auth implications
are understood. Localhost refers to the execution machine; use a local forward
or authorized SSH tunnel when the user is on another host.

## Demonstrate inference

Use the forwarded origin, for example http://127.0.0.1:8080. The gateway path is
/models/DEPLOYMENT_NAME/v1/chat/completions, not the runtime's internal Service.
When auth is enabled, take the gateway token from the user's secure environment
as INFEROPS_GATEWAY_TOKEN; never paste a real token into the example. The bundled
checker reads it without putting it in process arguments:

~~~sh
python3 "$SKILL_DIR/scripts/smoke_inference.py" --gateway-url http://127.0.0.1:8080 --model "$MODEL" \
  --requests 1 --timeout 30
~~~

Give the user a concrete curl example with their non-secret deployment name
substituted. For an unauthenticated LOCAL gateway:

~~~sh
curl --fail-with-body --max-time 30 http://127.0.0.1:8080/models/my-model/v1/chat/completions   -H 'Content-Type: application/json'   -d '{"model":"my-model","messages":[{"role":"user","content":"Hello"}],"max_tokens":16}'
~~~

If auth is configured, prefer this Python example over expanding a bearer token
in curl's process arguments. It assumes the gateway token is already supplied
securely in the environment:

~~~python
import json, os, urllib.request
model = "my-model"  # substitute the actual deployment name
request = urllib.request.Request(
    f"http://127.0.0.1:8080/models/{model}/v1/chat/completions",
    data=json.dumps({"model": model, "messages": [{"role": "user", "content": "Hello"}], "max_tokens": 16}).encode(),
    headers={"Content-Type": "application/json", "Authorization": "Bearer " + os.environ["INFEROPS_GATEWAY_TOKEN"]},
)
with urllib.request.urlopen(request, timeout=30) as response:
    print(response.read().decode())
~~~

That example is for the user to run locally; do not copy their generated text
into diagnostic logs. A 401 means check gateway auth, 404 means check deployment
name/route, 503 means inspect model readiness and upstream endpoints. A transport
failure means first check the forward, Service port, and context. Never solve
these by bypassing gateway auth or exposing runtime Pods directly.

Return the gateway origin, full model API URL, successful test result, example,
authentication source, forwarding lifetime, and how to stop it (Ctrl-C in its
terminal, or terminate the exact managed session). Do not claim permanent hosting
from a port-forward.
