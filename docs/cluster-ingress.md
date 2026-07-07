# Cluster and ingress support

InferOps keeps the gateway workload independent of the cluster's north-south
traffic implementation. The gateway chart can expose the same HTTP Service
through a standard Kubernetes `Ingress`, a Gateway API `HTTPRoute`, or a
`LoadBalancer` Service. The default remains `ClusterIP`. Tailscale remains the
recommended private-access path for the homelab profile.

The chart does not install an ingress controller, Gateway API CRDs, a Gateway,
DNS, certificates, or a cloud load-balancer controller. Cluster operators own
those shared infrastructure components.

## Support matrix

| Target | InferOps resource | Required cluster component |
| --- | --- | --- |
| NGINX | `networking.k8s.io/v1` Ingress | An NGINX `IngressClass` |
| Traefik | `networking.k8s.io/v1` Ingress | A Traefik `IngressClass` |
| Istio | `gateway.networking.k8s.io/v1` HTTPRoute | Gateway API CRDs, Istio, and an accepting Gateway |
| Other Gateway API implementations | `gateway.networking.k8s.io/v1` HTTPRoute | Gateway API CRDs and an accepting Gateway |
| Managed-cloud or on-premises load balancer | `v1` LoadBalancer Service | A Service load-balancer implementation |
| Homelab private access | Tailscale Ingress | Tailscale Kubernetes Operator |

Kubernetes documents the stable
[Ingress API](https://kubernetes.io/docs/concepts/services-networking/ingress/),
[Gateway API](https://kubernetes.io/docs/concepts/services-networking/gateway/),
and
[LoadBalancer Service](https://kubernetes.io/docs/concepts/services-networking/service/)
contracts. Controller-specific prerequisites remain in the
[NGINX](https://docs.nginx.com/nginx-ingress-controller/),
[Traefik](https://doc.traefik.io/traefik/reference/install-configuration/providers/kubernetes/kubernetes-ingress/),
and
[Istio Gateway API](https://istio.io/latest/docs/tasks/traffic-management/ingress/gateway-api/)
documentation.

## Standard Ingress

The CLI requires an explicit class so an install cannot silently bind to an
unexpected default controller. Create the gateway bearer-token Secret before
enabling external exposure:

```bash
kubectl create secret generic inferops-gateway-token \
  --namespace inferops-system \
  --from-literal=token='<random bearer token>'
```

```bash
# NGINX
inferops install \
  --namespace inferops-system \
  --exposure ingress \
  --ingress-class nginx \
  --ingress-hostname models.example.com \
  --gateway-auth-secret inferops-gateway-token

# Traefik
inferops install \
  --namespace inferops-system \
  --exposure ingress \
  --ingress-class traefik \
  --ingress-hostname models.example.com \
  --gateway-auth-secret inferops-gateway-token
```

The hostname is optional. For TLS, controller annotations, or certificate
references, use a reviewed values file:

```yaml
ingress:
  enabled: true
  className: nginx
  hostname: models.example.com
  annotations:
    cert-manager.io/cluster-issuer: public
  tls:
    - hosts:
        - models.example.com
      secretName: inferops-gateway-tls
auth:
  enabled: true
  secretName: inferops-gateway-token
```

```bash
helm upgrade --install inferops-gateway deploy/helm/inferops-gateway \
  --namespace inferops-system \
  --values gateway-exposure.yaml \
  --atomic --wait
```

InferOps requires the external path to remain `/`. Rewriting or stripping a
prefix would change the stable `/models/<name>/v1/...` API and is rejected by
the chart.

## Gateway API and Istio

InferOps creates an `HTTPRoute` and attaches it to an existing Gateway. It does
not create or take ownership of the shared `Gateway`:

```bash
inferops install \
  --namespace inferops-system \
  --exposure gateway-api \
  --gateway-name public \
  --gateway-hostname models.example.com \
  --gateway-auth-secret inferops-gateway-token
```

For a Gateway or listener in another namespace:

```bash
inferops install \
  --namespace inferops-system \
  --exposure gateway-api \
  --gateway-name public \
  --gateway-namespace istio-ingress \
  --gateway-section-name http \
  --gateway-hostname models.example.com \
  --gateway-auth-secret inferops-gateway-token
```

The parent Gateway must allow routes from the InferOps namespace. Istio's
Gateway API integration uses the same `HTTPRoute`; a Gateway with
`gatewayClassName: istio` is managed outside this chart.

Check both sides of the attachment after installation:

```bash
kubectl get gateway -A
kubectl describe httproute inferops-gateway -n inferops-system
```

The route is usable only when its parents report `Accepted=True` and its
backend references report `ResolvedRefs=True`.

## LoadBalancer Services

Use the cluster's default Service load-balancer implementation:

```bash
inferops install \
  --namespace inferops-system \
  --exposure load-balancer \
  --gateway-auth-secret inferops-gateway-token
```

Or select a non-default implementation:

```bash
inferops install \
  --namespace inferops-system \
  --exposure load-balancer \
  --load-balancer-class example.com/internal \
  --gateway-auth-secret inferops-gateway-token
```

Cloud-specific annotations, allowed source ranges, and traffic policy stay in
Helm values so InferOps does not guess provider semantics:

```yaml
service:
  type: LoadBalancer
  annotations:
    example.com/provider-setting: "reviewed-value"
  loadBalancerSourceRanges:
    - 203.0.113.0/24
  externalTrafficPolicy: Local
auth:
  enabled: true
  secretName: inferops-gateway-token
```

`loadBalancerClass` is immutable on Kubernetes Services. Recreate the Service
during a planned change if the selected implementation must change.

The CLI refuses Ingress, Gateway API, and LoadBalancer exposure without
built-in gateway authentication. If an upstream gateway enforces equivalent
authentication, acknowledge that trust boundary explicitly with
`--allow-unauthenticated-exposure`. This flag does not configure or verify the
upstream policy.

## Multi-node gateway placement

The runtime scheduler and gateway use ordinary Kubernetes Services, so they do
not depend on a single node. For gateway availability across nodes, start with
the checked-in profile:

```bash
helm upgrade --install inferops-gateway deploy/helm/inferops-gateway \
  --namespace inferops-system \
  --values deploy/helm/inferops-gateway/values-multinode.yaml \
  --atomic --wait
```

It runs two replicas and requires hostname topology spread. A two-node cluster
therefore needs both nodes to be schedulable. Adjust `replicaCount`,
`topologySpreadConstraints`, affinity, and tolerations for the cluster's
failure domains. Keep the PodDisruptionBudget enabled.

## Acceptance checks

Run these checks on every managed-cluster or controller combination before
calling it supported in an environment:

```bash
kubectl get nodes
kubectl rollout status deployment/inferops-gateway -n inferops-system
kubectl get pods -n inferops-system \
  -l app.kubernetes.io/name=inferops-gateway -o wide
kubectl get service,ingress,httproute -n inferops-system
inferops doctor --namespace inferops-system --check gateway
curl --fail --show-error https://models.example.com/readyz
```

Also send an authenticated streaming request to an active model, cancel an
in-flight request, and verify that activating and draining models return the
documented `503` lifecycle response without reaching the runtime. Capture the
controller resource status and events when a check fails; a rendered resource
alone does not prove that the cluster controller accepted it.

CI renders and structurally validates NGINX, Traefik, generic Gateway API,
Istio attachment, LoadBalancer, Tailscale, and multi-node chart
configurations. Built-in Kubernetes resources also pass kubeconform. Those
offline checks catch template regressions but do not replace a live provider
acceptance run.
