# Cluster and Ingress Support

The gateway chart exposes the same HTTP Service through Ingress, Gateway API,
or a LoadBalancer. Default is `ClusterIP`. The chart does not install ingress
controllers, Gateway API CRDs, DNS, certificates, or load-balancer controllers.

## Support matrix

| Target | InferOps resource | Required cluster component |
| --- | --- | --- |
| NGINX | `Ingress` | NGINX `IngressClass` |
| Traefik | `Ingress` | Traefik `IngressClass` |
| Istio | `HTTPRoute` | Gateway API CRDs + Istio Gateway |
| Other Gateway API | `HTTPRoute` | Gateway API CRDs + Gateway |
| Cloud/on-prem LB | `LoadBalancer` Service | Load-balancer implementation |
| Homelab private access | Tailscale Ingress | Tailscale Kubernetes Operator |

## Ingress

Create a bearer-token Secret first:

```bash
kubectl create secret generic inferops-gateway-token \
  --namespace inferops-system \
  --from-literal=token='<random token>'
```

```bash
inferops install \
  --namespace inferops-system \
  --exposure ingress \
  --ingress-class nginx \
  --ingress-hostname models.example.com \
  --gateway-auth-secret inferops-gateway-token
```

For TLS or annotations, use a values file:

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

External path must remain `/`. Prefix rewriting breaks the API.

## Gateway API / Istio

```bash
inferops install \
  --namespace inferops-system \
  --exposure gateway-api \
  --gateway-name public \
  --gateway-hostname models.example.com \
  --gateway-auth-secret inferops-gateway-token
```

For a Gateway in another namespace:

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

Check attachment:

```bash
kubectl get gateway -A
kubectl describe httproute inferops-gateway -n inferops-system
```

## LoadBalancer

```bash
inferops install \
  --namespace inferops-system \
  --exposure load-balancer \
  --gateway-auth-secret inferops-gateway-token
```

Cloud-specific annotations stay in Helm values:

```yaml
service:
  type: LoadBalancer
  annotations:
    example.com/provider-setting: "value"
  loadBalancerSourceRanges:
    - 203.0.113.0/24
auth:
  enabled: true
  secretName: inferops-gateway-token
```

## Multi-node gateway

```bash
helm upgrade --install inferops-gateway deploy/helm/inferops-gateway \
  --namespace inferops-system \
  --values deploy/helm/inferops-gateway/values-multinode.yaml \
  --atomic --wait
```

## Acceptance checks

```bash
kubectl get nodes
kubectl rollout status deployment/inferops-gateway -n inferops-system
kubectl get service,ingress,httproute -n inferops-system
inferops doctor --namespace inferops-system --check gateway
curl --fail https://models.example.com/readyz
```
