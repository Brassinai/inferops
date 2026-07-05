# Namespace Tenancy

InferOps uses Kubernetes namespaces as its tenancy boundary. The operator may
reconcile resources cluster-wide, but every `ModelDeployment`, `ModelCache`,
runtime workload, Service, gateway, credential reference, and replacement
candidate remains in one namespace. A replacement policy cannot displace a
workload in another namespace, and cache paths include the namespace identity.

This is namespace isolation, not a hostile multi-tenant sandbox. Cluster
administrators remain responsible for the CNI, node isolation, Kubernetes API
audit configuration, and any policy engine used to constrain fields such as
runtime images, host paths, node selectors, or tolerations.

## Recommended layout

- Install the CRDs and operator once in a platform namespace such as
  `inferops-system`.
- Create one namespace and one gateway release per team.
- Create approved `ModelRuntime` objects in each team namespace. Runtime
  references are namespace-local.
- Bind a Kubernetes identity-provider group to the generated team Role.
- Size ResourceQuota values from actual GPU, CPU, memory, and cache budgets.
- Keep model credentials in same-namespace Secrets. The team Role intentionally
  cannot read or mutate Secrets.

Do not install multiple cluster-wide operators to simulate tenancy. Their
watches and managed-resource ownership would overlap. The shared operator plus
namespace-local gateways and RBAC is the supported layout.

## Team gateway values

The gateway chart can create namespace-scoped access, quota, and default
container limits. These controls are disabled until an administrator supplies
team-specific values:

```yaml
tenancy:
  access:
    enabled: true
    subjects:
      - kind: Group
        apiGroup: rbac.authorization.k8s.io
        name: inference-team-a
    allowModelRuntimeWrite: false
    allowCacheDelete: false
  quota:
    enabled: true
    hard:
      pods: "20"
      requests.cpu: "16"
      requests.memory: 64Gi
      limits.cpu: "32"
      limits.memory: 128Gi
      requests.nvidia.com/gpu: "2"
      limits.nvidia.com/gpu: "2"
      count/modeldeployments.inference.inferops.dev: "10"
      count/modelcaches.inference.inferops.dev: "20"
  limitRange:
    enabled: true
    limits:
      - type: Container
        defaultRequest:
          cpu: 100m
          memory: 128Mi
        default:
          cpu: "8"
          memory: 32Gi
```

Install the namespace gateway with a release name unique to the team:

```bash
helm upgrade --install team-a-gateway deploy/helm/inferops-gateway \
  --namespace team-a \
  --create-namespace \
  --values team-a-values.yaml \
  --atomic --wait
```

The generated Role supports the operational CLI commands for resources in that
namespace. It allows deployment lifecycle changes, status and endpoint reads,
runtime log reads, and optional cache deletion. It does not grant Node access,
Secret access, status writes, or cross-namespace access. Keep
`allowModelRuntimeWrite=false` when runtime definitions are platform-owned.
Keep `allowCacheDelete=false` for team identities: Kubernetes API deletion
bypasses the CLI's in-use cache safety checks. A platform administrator should
run reviewed cache deletion operations instead.

The `gpu list` command and cluster-wide doctor checks intentionally remain
platform-administrator operations because Node and cross-namespace Pod reads
would pierce the team boundary.

## Network isolation

The operator and gateway charts create NetworkPolicies by default:

- the gateway accepts traffic only on its HTTP port;
- the gateway may reach same-namespace InferOps runtime pods, cluster DNS, and
  the Kubernetes API;
- runtime pods accept traffic from their namespace gateway and have no egress;
- the operator exposes only metrics, health, and webhook ports and may reach
  cluster DNS and the Kubernetes API.

The defaults allow HTTPS only to common Kubernetes API Service IPs. Before
installation, replace `networkPolicy.gateway.apiServerCIDRs` and
`networkPolicy.apiServerCIDRs` with the exact `/32` or `/128` address exposed
to Pods as `KUBERNETES_SERVICE_HOST`. The operator and gateway will lose API
connectivity when that address is absent; do not widen the rule to a public or
private network range.

An empty gateway `ingressFrom` allows any source to reach the gateway HTTP
port. Restrict it to the ingress controller or tailnet namespace in a
production cluster. Likewise, add `runtime.additionalIngressFrom` when a
Prometheus deployment needs direct runtime metrics. Runtime egress should be
added only when a reviewed runtime genuinely requires it; model download is
performed by separate cache Jobs.

NetworkPolicy enforcement requires a compatible CNI. Verify enforcement with a
denied cross-namespace request as well as an allowed gateway request before
considering the boundary operational. Set `networkPolicy.enabled=false` only
when equivalent policy is managed outside the chart.

## Audit trail

The CLI talks directly to the Kubernetes API with the caller's credentials.
Use individual User or identity-provider Group subjects instead of a shared
team kubeconfig so Kubernetes audit events retain the actor. Lifecycle changes
use the `inferops-cli` field manager, and the operator emits namespaced Events
for major lifecycle transitions and failures.

On self-managed API servers, merge a metadata-only rule like this before the
policy's catch-all rule:

```yaml
- level: Metadata
  resources:
    - group: inference.inferops.dev
      resources:
        - modeldeployments
        - modelcaches
        - modelruntimes
  omitStages:
    - RequestReceived
```

`Metadata` records the authenticated user, groups, verb, namespace, resource,
name, user agent, response status, and timing without recording request or
response bodies that may contain sensitive references. Managed Kubernetes
providers expose equivalent audit controls through their control-plane logging
settings.

Review both Kubernetes audit events and InferOps Events during incident
response. Audit events answer who requested a change; InferOps Events and
status conditions answer what the controller observed and why reconciliation
progressed or failed.
