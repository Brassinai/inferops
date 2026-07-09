"""Application declaration primitives."""

from __future__ import annotations

from collections.abc import Mapping
from typing import Any

from .runtime import attach_runtime_methods
from .spec import (
    DEFAULT_ACTIVATION_STATE,
    DEFAULT_CACHE_ENABLED,
    DEFAULT_CACHE_PATH,
    DEFAULT_CACHE_SIZE,
    DEFAULT_CACHE_TYPE,
    DEFAULT_DRAIN_TIMEOUT,
    DEFAULT_GPU_COUNT,
    DEFAULT_GPU_VENDOR,
    DEFAULT_MAX_MODEL_LEN,
    DEFAULT_MAX_REPLICAS,
    DEFAULT_MEMORY,
    DEFAULT_MIN_REPLICAS,
    DEFAULT_MODEL_REVISION,
    DEFAULT_MODEL_SOURCE,
    DEFAULT_OPENAI_COMPATIBLE,
    DEFAULT_PRIORITY,
    DEFAULT_ROUTE_ENABLED,
    DEFAULT_RUNTIME,
    DEFAULT_IDLE_TIMEOUT,
    DEFAULT_TARGET_PENDING_REQUESTS,
    DEFAULT_WHEN_FULL,
    DEFAULT_CPU,
    ModelConfig,
    collect_endpoint_metadata,
    normalize_model_config,
)


class App:
    """Represents a deployable inference application."""

    def __init__(self, name: str):
        if not name:
            raise ValueError("app name is required")
        self.name = name
        self.models: list[ModelConfig] = []

    def register(self, model_config: Mapping[str, Any]) -> ModelConfig:
        """Register a model declaration with the app."""
        normalized = normalize_model_config(model_config)
        self.models.append(normalized)
        return normalized

    def model(
        self,
        *,
        name: str,
        engine: str = DEFAULT_RUNTIME,
        model: str,
        gpu: str | int | None = DEFAULT_GPU_COUNT,
        gpu_vendor: str = DEFAULT_GPU_VENDOR,
        gpu_type: str | None = None,
        cpu: str | None = DEFAULT_CPU,
        memory: str | None = DEFAULT_MEMORY,
        activation: str = DEFAULT_ACTIVATION_STATE,
        when_full: str = DEFAULT_WHEN_FULL,
        priority: int = DEFAULT_PRIORITY,
        drain_timeout: str = DEFAULT_DRAIN_TIMEOUT,
        min_replicas: int = DEFAULT_MIN_REPLICAS,
        max_replicas: int = DEFAULT_MAX_REPLICAS,
        target_pending_requests: int | None = DEFAULT_TARGET_PENDING_REQUESTS,
        idle_timeout: str | None = DEFAULT_IDLE_TIMEOUT,
        max_model_len: int = DEFAULT_MAX_MODEL_LEN,
        route_path: str | None = None,
        route_enabled: bool = DEFAULT_ROUTE_ENABLED,
        openai_compatible: bool = DEFAULT_OPENAI_COMPATIBLE,
        cache_enabled: bool = DEFAULT_CACHE_ENABLED,
        cache_type: str = DEFAULT_CACHE_TYPE,
        cache_size: str = DEFAULT_CACHE_SIZE,
        cache_path: str = DEFAULT_CACHE_PATH,
        model_source: str = DEFAULT_MODEL_SOURCE,
        model_revision: str = DEFAULT_MODEL_REVISION,
        runtime_image: str | None = None,
        dtype: str | None = None,
        hugging_face_token_secret_name: str | None = None,
        **extra,
    ) -> Any:
        """Declare a class as a model deployment."""
        if extra:
            unexpected = ", ".join(sorted(extra))
            raise ValueError(f"unsupported model options: {unexpected}")

        def wrapper(cls: type[Any]) -> type[Any]:
            attach_runtime_methods(cls)
            config = normalize_model_config(
                {
                    "name": name,
                    "engine": engine,
                    "model": model,
                    "gpu": gpu,
                    "gpu_vendor": gpu_vendor,
                    "gpu_type": gpu_type,
                    "cpu": cpu,
                    "memory": memory,
                    "activation": activation,
                    "when_full": when_full,
                    "priority": priority,
                    "drain_timeout": drain_timeout,
                    "min_replicas": min_replicas,
                    "max_replicas": max_replicas,
                    "target_pending_requests": target_pending_requests,
                    "idle_timeout": idle_timeout,
                    "max_model_len": max_model_len,
                    "route_path": route_path,
                    "route_enabled": route_enabled,
                    "openai_compatible": openai_compatible,
                    "cache_enabled": cache_enabled,
                    "cache_type": cache_type,
                    "cache_size": cache_size,
                    "cache_path": cache_path,
                    "model_source": model_source,
                    "model_revision": model_revision,
                    "runtime_image": runtime_image,
                    "dtype": dtype,
                    "hugging_face_token_secret_name": hugging_face_token_secret_name,
                },
                model_class=cls,
                endpoints=collect_endpoint_metadata(cls),
            )
            cls.__inferops_model__ = config
            self.models.append(config)
            return cls

        return wrapper
