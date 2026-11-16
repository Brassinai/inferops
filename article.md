Yes, after the operator, the best next area is the **serving/control-plane path**: how traffic reaches the runtime Pods the operator created.

I’d study it in this order:

**1. Gateway**
Start here next. The operator creates model runtime Deployments and Services, but the **gateway is what receives user inference traffic** and routes it to the right model.

Study:

```text
gateway/cmd/gateway/main.go
gateway/internal/discovery/discovery.go
gateway/internal/routing/router.go
gateway/internal/proxy/proxy.go
gateway/internal/auth/auth.go
gateway/internal/metrics/metrics.go
```

Mental picture:

```text
User request
  -> InferOps Gateway
  -> route lookup for model
  -> Kubernetes-discovered backend Service/Endpoint
  -> runtime Pod
  -> response back to user
```

This will connect beautifully with what you already learned from `ModelDeployment`: the operator creates the runtime Service; the gateway discovers and proxies to it.

**2. Proxy and Routing**
Inside gateway, focus on:

```text
routing/
  What models/routes exist?

discovery/
  How does gateway learn about ModelDeployments, Services, EndpointSlices?

proxy/
  How does request forwarding actually happen?
```

This is where the system changes from “Kubernetes operator creates things” to “real HTTP traffic flows through the platform.”

**3. Network Topology**
After gateway internals, study the Kubernetes networking shape:

```text
runtime Pod
  <- Service created by ModelDeployment controller
  <- gateway proxy
  <- gateway Service
  <- optional Ingress / Gateway API / Tailscale
  <- user/client
```

Then read the Helm templates:

```text
deploy/helm/inferops-gateway/templates/service.yaml
deploy/helm/inferops-gateway/templates/ingress.yaml
deploy/helm/inferops-gateway/templates/httproute.yaml
deploy/helm/inferops-gateway/templates/tailscale-ingress.yaml
deploy/helm/inferops-gateway/templates/networkpolicy.yaml
```

This teaches how traffic enters the cluster and how it is restricted.

**4. RBAC**
Study RBAC after you understand who talks to what.

Important files:

```text
deploy/helm/inferops-operator/templates/rbac.yaml
deploy/helm/inferops-gateway/templates/rbac.yaml
deploy/helm/inferops-dashboard/templates/rbac.yaml
```

Mental model:

```text
Operator RBAC
  Can watch/update ModelDeployment, ModelCache, Deployments, Jobs, Pods, status.

Gateway RBAC
  Can discover models and endpoints so it can route traffic.

Dashboard RBAC
  Can read cluster/operator objects to show UI state.
```

RBAC becomes easier once you already know the component’s job.

**5. `/deploy/helm`**
This is the packaging/deployment layer for the whole platform.

It contains charts for:

```text
deploy/helm/inferops-operator
  Installs CRDs, operator manager, RBAC, webhooks, metrics, packaged ModelRuntime definitions.

deploy/helm/inferops-gateway
  Installs gateway Deployment, Service, Ingress/Gateway API/Tailscale options, auth, tenancy, NetworkPolicy.

deploy/helm/inferops-dashboard
  Installs dashboard UI and its read permissions.

deploy/helm/inferops-runtime
  Defines standalone/runtime chart behavior, including CPU values.
```

Think of `/deploy/helm` as:

```text
source code says what the components do
Helm says how those components are installed into a real cluster
```

**6. Dashboard**
Do this after gateway and Helm. The dashboard is usually easier once you understand the objects it displays.

Study:

```text
dashboard/cmd/dashboard/main.go
dashboard/internal/server/server.go
dashboard/internal/server/types.go
deploy/helm/inferops-dashboard/templates/*
```

Mental picture:

```text
Dashboard
  -> reads Kubernetes/operator state
  -> shows ModelDeployments, caches, runtimes, health, maybe routes
  -> helps humans see what the operator and gateway are doing
```

My recommended next article/section path would be:

```text
1. Gateway: from model route to runtime Service
2. Proxy and discovery: how traffic finds the right runtime
3. Network topology: Service, Ingress, Gateway API, Tailscale, NetworkPolicy
4. RBAC: permissions for operator, gateway, dashboard
5. Helm deployment: how InferOps is installed as a platform
6. Dashboard: how humans observe and operate it
```

So I’d move next to **Gateway**. It is the most natural continuation from the operator because it answers: “Now that the operator created the runtime, how does a real inference request reach it?”