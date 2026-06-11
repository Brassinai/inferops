"""Deployment helpers."""


def build_manifest(app) -> dict:
    """Build one ModelDeployment manifest from an app with one model."""
    if len(app.models) != 1:
        raise ValueError("build_manifest requires an app with exactly one model")

    return _build_model_manifest(app.models[0])


def _build_model_manifest(model: dict) -> dict:
    """Build a ModelDeployment manifest from one registered model."""
    name = model["name"]
    gpu = model.get("gpu")
    gpu_count = gpu if isinstance(gpu, int) else 1
    gpu_type = gpu if isinstance(gpu, str) else ""
    return {
        "apiVersion": "inference.inferops.dev/v1alpha1",
        "kind": "ModelDeployment",
        "metadata": {"name": name},
        "spec": {
            "model": {
                "name": name,
                "source": "huggingface",
                "repo": model["model"],
                "revision": "main",
            },
            "runtime": {
                "ref": model["engine"],
                "maxModelLen": model.get("max_model_len") or 4096,
                "tensorParallelSize": 1,
                "gpuMemoryUtilization": 0.85,
            },
            "resources": {
                "cpu": model.get("cpu") or "4",
                "memory": model.get("memory") or "16Gi",
                "gpu": {"count": gpu_count, "vendor": "nvidia", "type": gpu_type},
            },
            "activation": {
                "desiredState": "Inactive",
                "whenFull": "Queue",
                "priority": 50,
                "drainTimeout": "5m",
            },
            "scaling": {
                "minReplicas": model.get("min_replicas") or 0,
                "maxReplicas": model.get("max_replicas") or 1,
            },
            "routing": {
                "enabled": True,
                "path": f"/models/{name}",
                "openAICompatible": True,
            },
            "cache": {
                "enabled": True,
                "type": "nodeLocal",
                "size": "50Gi",
                "path": "/var/lib/inferops/models",
            },
        },
    }


def build_manifests(app) -> list[dict]:
    """Build one ModelDeployment manifest per model in an app."""
    return [_build_model_manifest(model) for model in app.models]
