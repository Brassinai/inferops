# Quickstart

The runtime images and standalone runtime Helm chart can be exercised today.
The operator manager and reconcilers do not yet implement the complete
`ModelDeployment` to runtime workload flow, so applying a CRD alone will not
create an inference pod.

For a local Linux-container smoke test, use one of:

- [llama.cpp on CPU](../runtimes/llama_cpp/README.md)
- [vLLM on CPU or GPU](../runtimes/vllm/README.md)
- [SGLang on a supported GPU](../runtimes/sglang/README.md)
- [nano-vLLM with an externally built engine image](../runtimes/nano_vllm/README.md)

To exercise the Kubernetes workload layer without relying on unfinished
reconciliation, use the
[standalone runtime chart](../deploy/helm/inferops-runtime/README.md) or follow
the [manual Kubernetes deployment guide](devops-engineering-guide.md).

The intended end-to-end installation remains:

```bash
helm install inferops ./deploy/helm/inferops-operator \
  --namespace inferops-system \
  --create-namespace
```

Once reconciliation is implemented, users will deploy with the SDK/CLI or
apply a `ModelDeployment` directly.
