# Autoscaling

Autoscaling design should account for inference-specific signals, requested
CPU, memory, optional GPU capacity, queue depth, request concurrency, and safe
rollout behavior.

Possible Kubernetes integrations include HPA and KEDA.
