# Production

Production guidance should cover:

- Required Kubernetes components.
- CPU and memory capacity assumptions.
- GPU device plugin assumptions when deployments request GPUs.
- Storage classes for model cache.
- RBAC and namespace isolation.
- Immutable, hardware-compatible runtime images produced by the engine release
  pipeline.
- Image registry credentials for runtime pods and separately scoped model
  registry credentials for ModelCache jobs. Runtime pods must not receive
  model-download credentials.
- Monitoring, logging, metrics, and alerting.
- Upgrade and rollback procedures.
