# Namespace Tenancy

InferOps uses Kubernetes namespaces as the tenancy boundary. Every
`ModelDeployment`, `ModelCache`, Service, gateway, credential, and replacement
candidate lives in one namespace.

## Recommended layout

- Install CRDs and operator once in `inferops-system`.
- Create one namespace and one gateway release per team.
- Create approved `ModelRuntime` objects in each team namespace.
- Bind an identity-provider group to the generated team Role.
- Size `ResourceQuota` from real GPU, CPU, memory, and cache budgets.
- Keep model credentials in same-namespace Secrets.

Do not install multiple cluster-wide operators.

## Team gateway values

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

Install:

```bash
helm upgrade --install team-a-gateway deploy/helm/inferops-gateway \
  --namespace team-a \
  --create-namespace \
  --values team-a-values.yaml \
  --atomic --wait
```

The generated Role supports operational CLI commands in that namespace. It
does not grant Node access, Secret access, status writes, or cross-namespace
access.

## Network isolation

Default NetworkPolicies:

- Gateway accepts traffic only on its HTTP port.
- Gateway reaches same-namespace runtimes, cluster DNS, and Kubernetes API.
- Runtime pods accept traffic from their namespace gateway and have no egress.
- Operator exposes only metrics, health, and webhook ports.

Replace chart defaults with exact Kubernetes API Service IPs and restricted
ingress sources before production. Set `networkPolicy.enabled=false` only when
equivalent policy is managed outside the chart.

## Audit trail

Use individual User or identity-provider Group subjects so Kubernetes audit
events retain the actor. Lifecycle changes use the `inferops-cli` field
manager. The operator emits Events for major lifecycle transitions.

Example metadata-only audit rule:

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
