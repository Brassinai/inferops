"""Deployment helpers."""

from __future__ import annotations

from collections.abc import Sequence
import yaml

from .spec import DEFAULT_GPU_MEMORY_UTILIZATION, ModelConfig


def build_manifest(app) -> dict:
    """Build one ModelDeployment manifest from an app with one model."""
    manifests = build_manifests(app)
    if len(manifests) != 1:
        raise ValueError("build_manifest requires an app with exactly one model")
    return manifests[0]


def build_manifests(app) -> list[dict]:
    """Build one ModelDeployment manifest per model in an app."""
    manifests = [_build_model_manifest(model) for model in _iter_model_configs(app.models)]
    return sorted(manifests, key=lambda manifest: manifest["metadata"]["name"])


def render_yaml(app) -> str:
    """Render deterministic YAML for one or more manifests."""
    manifests = build_manifests(app)
    return yaml.safe_dump_all(
        manifests,
        sort_keys=False,
        explicit_start=len(manifests) > 1,
        default_flow_style=False,
    )


def _build_model_manifest(model: dict) -> dict:
    """Build a ModelDeployment manifest from one normalized model config."""
    runtime = {
        "ref": model.engine,
        "maxModelLen": model.max_model_len,
    }
    if model.runtime_image is not None:
        runtime["image"] = model.runtime_image
    if model.dtype is not None:
        runtime["dtype"] = model.dtype

    resources = {
        "cpu": model.cpu,
        "memory": model.memory,
    }
    if model.gpu_count is not None:
        resources["gpu"] = {
            "count": model.gpu_count,
            "vendor": model.gpu_vendor,
        }
        if model.gpu_type is not None:
            resources["gpu"]["type"] = model.gpu_type
        runtime["tensorParallelSize"] = model.gpu_count
        runtime["gpuMemoryUtilization"] = DEFAULT_GPU_MEMORY_UTILIZATION

    manifest = {
        "apiVersion": "inference.inferops.dev/v1alpha1",
        "kind": "ModelDeployment",
        "metadata": {"name": model.name},
        "spec": {
            "model": {
                "name": model.name,
                "source": model.model_source,
                "repo": model.model,
                "revision": model.model_revision,
            },
            "runtime": runtime,
            "resources": resources,
            "activation": {
                "desiredState": model.activation,
                "whenFull": model.when_full,
                "priority": model.priority,
                "drainTimeout": model.drain_timeout,
            },
            "scaling": {
                "minReplicas": model.min_replicas,
                "maxReplicas": model.max_replicas,
            },
            "routing": {
                "enabled": model.route_enabled,
                "path": model.route_path,
                "openAICompatible": model.openai_compatible,
            },
            "cache": {
                "enabled": model.cache_enabled,
                "type": model.cache_type,
                "size": model.cache_size,
                "path": model.cache_path,
            },
        },
    }
    if model.hugging_face_token_secret_name is not None:
        manifest["spec"]["secrets"] = {
            "huggingFaceTokenSecretName": model.hugging_face_token_secret_name,
        }
    return manifest


def _iter_model_configs(models: Sequence[ModelConfig]) -> list[ModelConfig]:
    invalid_models = [type(model).__name__ for model in models if not isinstance(model, ModelConfig)]
    if invalid_models:
        raise TypeError("app.models must contain normalized ModelConfig instances")
    return list(models)
