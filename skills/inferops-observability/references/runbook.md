# Set up monitoring and show model metrics

Discover existing Prometheus/Grafana releases, namespace, storage, and capacity
before installing a duplicate stack. Set MONITORING_NS to the discovered or new
monitoring namespace (default monitoring). Keep NS as the InferOps namespace.

~~~sh
helm list -A --kube-context "$CTX"
inferops observability setup --context "$CTX" --namespace "$NS" --monitoring-namespace "$MONITORING_NS" \
  --output json
~~~

If cluster-wide release listing is Forbidden, use helm list --namespace "$NS"
--kube-context "$CTX" for the requested namespace and mark wider ownership unknown.


Setup installs/upgrades kube-prometheus-stack and enables InferOps ServiceMonitors
and dashboards. Choose an approved compatible --chart-version when creating the
stack and preserve an existing release's version and settings. Do not set the
example password admin or pass a real password on the command line. Use the
chart-generated Secret unless an existing secure password mechanism is required.
When Prometheus/Grafana already exist, inspect their selectors/ownership and run
only the enable operation where appropriate:

~~~sh
inferops observability enable --context "$CTX" --namespace "$NS" --output json
kubectl --context "$CTX" -n "$MONITORING_NS" get pods,services
kubectl --context "$CTX" -n "$NS" get servicemonitors
kubectl --context "$CTX" -n "$NS" get configmaps -l grafana_dashboard=1
~~~

Check monitoring rollout readiness and scrape targets; a successful Helm command
alone is not proof of collected metrics. Existing Prometheus selectors must
include the InferOps ServiceMonitor namespace/labels. Dashboard sidecars must
watch the namespace containing the InferOps dashboard ConfigMaps.

## Open Grafana and log in

~~~sh
inferops observability open --context "$CTX" --namespace "$MONITORING_NS" --local-port 3000
~~~

Keep this in a persistent terminal/session. The CLI discovers Grafana, waits for
readiness, chooses an available port when the preferred one is busy, and may
reconnect; use the actual printed URL, not an assumed 3000. Keep localhost
binding and provide the terminal/process stop instructions. For remote execution,
explain that localhost is on that remote host and use an authorized SSH tunnel
or run forwarding on the user's own computer. Check WSL browser reachability.

Username is admin unless the existing setup specifies another identity. Discover
the actual Grafana admin Secret from its deployment's secretKeyRef references;
do not guess solely from a release name and do not print the whole Pod spec.
Provide this command for the USER to run locally after resolving GRAFANA_SECRET
and its password key (the chart default is admin-password):

~~~sh
kubectl --context "$CTX" -n "$MONITORING_NS" get secret "$GRAFANA_SECRET" -o jsonpath='{.data.admin-password}' | base64 --decode
~~~

Use a platform-equivalent decoder if needed. Do not execute that command into
shared tool output or repeat the password in chat/logs; use an available secure
credential delivery channel if the user wants it delivered directly. Never
reset an existing admin password just to gain access.

## Guided metric walkthrough

1. Open the reported URL, log in, select Dashboards, and search for InferOps.
2. Open InferOps / Platform. Choose a recent interval such as Last 15 minutes
   and refresh. Inspect activation queue/duration, cache failures, GPU slots,
   gateway request rate, latency, and upstream errors.
3. Open the runtime dashboard actually exported by that engine (for example
   InferOps / vLLM Runtime or InferOps / llama.cpp Runtime). Inspect available
   throughput, queue, and memory panels; unsupported telemetry stays missing.
4. Generate a few short authorized inference requests through the gateway, then
   refresh. Empty graphs before traffic are not necessarily a scrape failure.
5. In Explore choose the Prometheus data source. Start with the available
   inferops_gateway_requests_total and inferops_gateway_request_duration_seconds
   series, then inspect actual runtime metric names/labels before filtering.
6. For a specific model, use only model labels that the runtime actually exports.
   Platform metric labels intentionally exclude object names, repos, namespaces,
   UIDs, and request IDs. Never invent a platform model-name filter or promise
   per-model visibility where that metric contract does not provide it.

If data is absent, distinguish no traffic, unhealthy targets, label/namespace
selection, and an unsupported runtime metric. Report the installed stack,
verified targets/dashboard state, access URL and lifetime, secure login steps,
and the exact panels or actual series the user can use for their model.
