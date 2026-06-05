# Architecture

inferops is a self-hosted Kubernetes-native orchestration platform for nano-vLLM.

The intended control-plane flow is:

```txt
Python SDK / CLI / YAML
        -> Kubernetes API
        -> ModelDeployment CRD
        -> nano-vLLM Operator
        -> Kubernetes resources
        -> nano-vLLM runtime pods
        -> OpenAI-compatible API endpoint
```

Phase 1 will define the controller boundaries, CRD semantics, gateway responsibilities, rollout behavior, model cache strategy, and autoscaling integration in more detail.
