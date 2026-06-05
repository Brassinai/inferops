"""Deployment helpers."""


def build_manifest(app) -> dict:
    """Build a placeholder Kubernetes manifest from an app declaration."""
    models = []
    for model in app.models:
        models.append(
            {
                "name": model["name"],
                "engine": model["engine"],
                "model": model["model"],
                "gpu": model.get("gpu"),
                "cpu": model.get("cpu"),
                "memory": model.get("memory"),
                "minReplicas": model.get("min_replicas"),
                "maxReplicas": model.get("max_replicas"),
                "maxModelLen": model.get("max_model_len"),
            }
        )

    return {
        "apiVersion": "inference.inferops.dev/v1alpha1",
        "kind": "ModelDeployment",
        "metadata": {"name": app.name},
        "spec": {"models": models},
    }
