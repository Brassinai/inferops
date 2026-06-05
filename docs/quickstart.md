# Quickstart

This document will cover local setup and first deployment once the operator and charts are implemented.

Planned installation shape:

```bash
helm install inferops ./deploy/helm/inferops-operator \
  --namespace inferops-system \
  --create-namespace
```

Planned deployment options:

```bash
inferops deploy app.py
kubectl apply -f modeldeployment.yaml
```
