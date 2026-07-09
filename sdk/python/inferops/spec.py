"""Normalization and validation helpers for SDK model declarations."""

from __future__ import annotations

from collections.abc import Iterable, Mapping
from dataclasses import dataclass
import inspect
import re
from typing import Any


DEFAULT_CPU = "8"
DEFAULT_MEMORY = "32Gi"
DEFAULT_MODEL_SOURCE = "huggingface"
DEFAULT_MODEL_REVISION = "main"
DEFAULT_ACTIVATION_STATE = "Inactive"
DEFAULT_WHEN_FULL = "Queue"
DEFAULT_PRIORITY = 50
DEFAULT_DRAIN_TIMEOUT = "5m"
DEFAULT_MIN_REPLICAS = 0
DEFAULT_MAX_REPLICAS = 1
DEFAULT_TARGET_PENDING_REQUESTS = None
DEFAULT_IDLE_TIMEOUT = None
DEFAULT_MAX_MODEL_LEN = 4096
DEFAULT_ROUTE_ENABLED = True
DEFAULT_OPENAI_COMPATIBLE = True
DEFAULT_CACHE_ENABLED = True
DEFAULT_CACHE_TYPE = "nodeLocal"
DEFAULT_CACHE_SIZE = "100Gi"
DEFAULT_CACHE_PATH = "/var/lib/inferops/models"
DEFAULT_GPU_VENDOR = "nvidia"
DEFAULT_GPU_COUNT = 1
DEFAULT_RUNTIME = "nano-vllm"
DEFAULT_GPU_MEMORY_UTILIZATION = 0.85

_VALID_NAME_RE = re.compile(r"^[a-z0-9]([-a-z0-9]*[a-z0-9])?$")
_VALID_DURATION_RE = re.compile(r"^[0-9]+(?:ms|s|m|h)$")
_ALLOWED_ENDPOINT_METHODS = {"DELETE", "GET", "PATCH", "POST", "PUT"}
_ALLOWED_MODEL_OPTIONS = frozenset(
    {
        "name",
        "engine",
        "model",
        "gpu",
        "gpu_vendor",
        "gpu_type",
        "cpu",
        "memory",
        "activation",
        "when_full",
        "priority",
        "drain_timeout",
        "min_replicas",
        "max_replicas",
        "target_pending_requests",
        "idle_timeout",
        "max_model_len",
        "route_path",
        "route_enabled",
        "openai_compatible",
        "cache_enabled",
        "cache_type",
        "cache_size",
        "cache_path",
        "model_source",
        "model_revision",
        "runtime_image",
        "dtype",
        "hugging_face_token_secret_name",
    }
)

_ACTIVATION_STATE_ALIASES = {
    "inactive": "Inactive",
    "active": "Active",
    "Inactive": "Inactive",
    "Active": "Active",
}

_WHEN_FULL_ALIASES = {
    "queue": "Queue",
    "Queue": "Queue",
    "reject": "Reject",
    "Reject": "Reject",
    "replace-oldest": "ReplaceOldest",
    "replace_oldest": "ReplaceOldest",
    "ReplaceOldest": "ReplaceOldest",
    "replace-lowest-priority": "ReplaceLowestPriority",
    "replace_lowest_priority": "ReplaceLowestPriority",
    "ReplaceLowestPriority": "ReplaceLowestPriority",
}


@dataclass(frozen=True, slots=True)
class EndpointSpec:
    """Normalized metadata declared by ``@web_endpoint``."""

    method: str
    path: str
    streaming: bool | None = None


@dataclass(frozen=True, slots=True)
class EndpointMetadata:
    """Normalized endpoint metadata attached to one model method."""

    name: str
    method: str
    path: str
    streaming: bool


@dataclass(frozen=True, slots=True)
class ModelConfig:
    """Normalized immutable model declaration used by the SDK."""

    name: str
    engine: str
    model: str
    model_source: str
    model_revision: str
    cpu: str
    memory: str
    gpu_count: int | None
    gpu_vendor: str | None
    gpu_type: str | None
    activation: str
    when_full: str
    priority: int
    drain_timeout: str
    min_replicas: int
    max_replicas: int
    target_pending_requests: int | None
    idle_timeout: str | None
    max_model_len: int
    route_path: str
    route_enabled: bool
    openai_compatible: bool
    cache_enabled: bool
    cache_type: str
    cache_size: str
    cache_path: str
    runtime_image: str | None
    dtype: str | None
    hugging_face_token_secret_name: str | None
    endpoints: tuple[EndpointMetadata, ...] = ()
    model_class: type[Any] | None = None


def validate_endpoint_metadata(*, method: str, path: str, streaming: bool | None = None) -> EndpointSpec:
    """Validate one endpoint metadata declaration."""
    if not isinstance(method, str) or not method.strip():
        raise ValueError("endpoint method is required")
    normalized_method = method.strip().upper()
    if normalized_method not in _ALLOWED_ENDPOINT_METHODS:
        allowed = ", ".join(sorted(_ALLOWED_ENDPOINT_METHODS))
        raise ValueError(f"endpoint method must be one of {allowed}")
    if not isinstance(path, str) or not path.strip():
        raise ValueError("endpoint path is required")
    normalized_path = path.strip()
    if not normalized_path.startswith("/"):
        raise ValueError("endpoint path must start with '/'")
    if streaming is not None and not isinstance(streaming, bool):
        raise ValueError("endpoint streaming must be a boolean when provided")
    return EndpointSpec(method=normalized_method, path=normalized_path, streaming=streaming)


def collect_endpoint_metadata(model_class: type[Any]) -> tuple[EndpointMetadata, ...]:
    """Collect deterministic endpoint metadata from a model class."""
    endpoints: list[EndpointMetadata] = []
    seen: set[tuple[str, str]] = set()
    for attr_name in sorted(dir(model_class)):
        attr = getattr(model_class, attr_name)
        endpoint = getattr(attr, "__inferops_endpoint__", None)
        if endpoint is None:
            continue
        inferred_streaming = inspect.isasyncgenfunction(attr) or inspect.isgeneratorfunction(attr)
        if endpoint.streaming is False and inferred_streaming:
            raise ValueError(
                f"endpoint {endpoint.method} {endpoint.path} on model {model_class.__name__} cannot declare streaming=False while yielding"
            )
        dedupe_key = (endpoint.method, endpoint.path)
        if dedupe_key in seen:
            raise ValueError(
                f"duplicate endpoint declaration for {endpoint.method} {endpoint.path} on model {model_class.__name__}"
            )
        seen.add(dedupe_key)
        endpoints.append(
            EndpointMetadata(
                name=attr_name,
                method=endpoint.method,
                path=endpoint.path,
                streaming=inferred_streaming if endpoint.streaming is None else endpoint.streaming,
            )
        )
    return tuple(endpoints)


def normalize_model_config(
    raw: Mapping[str, Any],
    *,
    model_class: type[Any] | None = None,
    endpoints: Iterable[EndpointMetadata] = (),
) -> ModelConfig:
    """Validate and normalize one user model declaration."""
    unexpected_keys = sorted(set(raw) - _ALLOWED_MODEL_OPTIONS)
    if unexpected_keys:
        raise ValueError(f"unsupported model options: {', '.join(unexpected_keys)}")

    name = _require_model_name(raw.get("name"))
    max_replicas = _normalize_non_negative_int(raw.get("max_replicas", DEFAULT_MAX_REPLICAS), field_name="max_replicas")
    min_replicas = _normalize_non_negative_int(raw.get("min_replicas", DEFAULT_MIN_REPLICAS), field_name="min_replicas")
    if max_replicas < min_replicas:
        raise ValueError("max_replicas must be greater than or equal to min_replicas")
    target_pending_requests = _normalize_optional_positive_int(
        raw.get("target_pending_requests", DEFAULT_TARGET_PENDING_REQUESTS),
        field_name="target_pending_requests",
    )

    gpu_count, gpu_vendor, gpu_type = _normalize_gpu(
        gpu=raw.get("gpu", DEFAULT_GPU_COUNT),
        gpu_vendor=raw.get("gpu_vendor", DEFAULT_GPU_VENDOR),
        gpu_type=raw.get("gpu_type"),
    )

    return ModelConfig(
        name=name,
        engine=_require_non_empty_string(raw.get("engine", DEFAULT_RUNTIME), field_name="engine"),
        model=_require_non_empty_string(raw.get("model"), field_name="model"),
        model_source=_require_non_empty_string(raw.get("model_source", DEFAULT_MODEL_SOURCE), field_name="model_source"),
        model_revision=_require_non_empty_string(
            raw.get("model_revision", DEFAULT_MODEL_REVISION), field_name="model_revision"
        ),
        cpu=_normalize_resource_quantity(_value_or_default(raw.get("cpu"), DEFAULT_CPU), field_name="cpu"),
        memory=_normalize_resource_quantity(_value_or_default(raw.get("memory"), DEFAULT_MEMORY), field_name="memory"),
        gpu_count=gpu_count,
        gpu_vendor=gpu_vendor,
        gpu_type=gpu_type,
        activation=_normalize_activation_state(raw.get("activation", DEFAULT_ACTIVATION_STATE)),
        when_full=_normalize_when_full(raw.get("when_full", DEFAULT_WHEN_FULL)),
        priority=_normalize_non_negative_int(raw.get("priority", DEFAULT_PRIORITY), field_name="priority"),
        drain_timeout=_normalize_duration(raw.get("drain_timeout", DEFAULT_DRAIN_TIMEOUT), field_name="drain_timeout"),
        min_replicas=min_replicas,
        max_replicas=max_replicas,
        target_pending_requests=target_pending_requests,
        idle_timeout=_normalize_optional_duration(raw.get("idle_timeout", DEFAULT_IDLE_TIMEOUT), field_name="idle_timeout"),
        max_model_len=_normalize_positive_int(raw.get("max_model_len", DEFAULT_MAX_MODEL_LEN), field_name="max_model_len"),
        route_path=_normalize_route_path(_value_or_default(raw.get("route_path"), f"/models/{name}")),
        route_enabled=_normalize_bool(raw.get("route_enabled", DEFAULT_ROUTE_ENABLED), field_name="route_enabled"),
        openai_compatible=_normalize_bool(
            raw.get("openai_compatible", DEFAULT_OPENAI_COMPATIBLE), field_name="openai_compatible"
        ),
        cache_enabled=_normalize_bool(raw.get("cache_enabled", DEFAULT_CACHE_ENABLED), field_name="cache_enabled"),
        cache_type=_require_non_empty_string(raw.get("cache_type", DEFAULT_CACHE_TYPE), field_name="cache_type"),
        cache_size=_normalize_resource_quantity(raw.get("cache_size", DEFAULT_CACHE_SIZE), field_name="cache_size"),
        cache_path=_normalize_absolute_path(raw.get("cache_path", DEFAULT_CACHE_PATH), field_name="cache_path"),
        runtime_image=_normalize_optional_string(raw.get("runtime_image"), field_name="runtime_image"),
        dtype=_normalize_optional_string(raw.get("dtype"), field_name="dtype"),
        hugging_face_token_secret_name=_normalize_optional_string(
            raw.get("hugging_face_token_secret_name"),
            field_name="hugging_face_token_secret_name",
        ),
        endpoints=tuple(endpoints),
        model_class=model_class,
    )


def _require_model_name(value: Any) -> str:
    name = _require_non_empty_string(value, field_name="name")
    if len(name) > 63 or _VALID_NAME_RE.fullmatch(name) is None:
        raise ValueError(
            "name must be a valid Kubernetes DNS-1123 label with only lowercase letters, numbers, and hyphens"
        )
    return name


def _value_or_default(value: Any, default: Any) -> Any:
    if value is None:
        return default
    return value


def _require_non_empty_string(value: Any, *, field_name: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{field_name} is required")
    return value.strip()


def _normalize_optional_string(value: Any, *, field_name: str) -> str | None:
    if value is None:
        return None
    return _require_non_empty_string(value, field_name=field_name)


def _normalize_positive_int(value: Any, *, field_name: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or value < 1:
        raise ValueError(f"{field_name} must be an integer greater than or equal to 1")
    return value


def _normalize_optional_positive_int(value: Any, *, field_name: str) -> int | None:
    if value is None:
        return None
    return _normalize_positive_int(value, field_name=field_name)


def _normalize_non_negative_int(value: Any, *, field_name: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or value < 0:
        raise ValueError(f"{field_name} must be an integer greater than or equal to 0")
    return value


def _normalize_resource_quantity(value: Any, *, field_name: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{field_name} must be a non-empty Kubernetes resource quantity")
    return value.strip()


def _normalize_duration(value: Any, *, field_name: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{field_name} must be a non-empty duration string")
    normalized = value.strip()
    if _VALID_DURATION_RE.fullmatch(normalized) is None:
        raise ValueError(f"{field_name} must look like a duration such as 5m or 30s")
    return normalized


def _normalize_optional_duration(value: Any, *, field_name: str) -> str | None:
    if value is None:
        return None
    return _normalize_duration(value, field_name=field_name)


def _normalize_route_path(value: Any) -> str:
    if not isinstance(value, str) or not value.strip():
        raise ValueError("route_path must be a non-empty path")
    normalized = value.strip()
    if not normalized.startswith("/"):
        raise ValueError("route_path must start with '/'")
    return normalized


def _normalize_absolute_path(value: Any, *, field_name: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{field_name} must be a non-empty absolute path")
    normalized = value.strip()
    if not normalized.startswith("/"):
        raise ValueError(f"{field_name} must start with '/'")
    return normalized


def _normalize_bool(value: Any, *, field_name: str) -> bool:
    if not isinstance(value, bool):
        raise ValueError(f"{field_name} must be a boolean")
    return value


def _normalize_activation_state(value: Any) -> str:
    if value not in _ACTIVATION_STATE_ALIASES:
        raise ValueError("activation must be 'inactive' or 'active'")
    return _ACTIVATION_STATE_ALIASES[value]


def _normalize_when_full(value: Any) -> str:
    if value not in _WHEN_FULL_ALIASES:
        raise ValueError(
            "when_full must be one of queue, reject, replace-oldest, or replace-lowest-priority"
        )
    return _WHEN_FULL_ALIASES[value]


def _normalize_gpu(*, gpu: Any, gpu_vendor: Any, gpu_type: Any) -> tuple[int | None, str | None, str | None]:
    if gpu is None:
        if gpu_type is not None:
            raise ValueError("gpu_type requires gpu to be set")
        if gpu_vendor not in (None, DEFAULT_GPU_VENDOR):
            raise ValueError("gpu_vendor requires gpu to be set")
        return None, None, None

    vendor = _require_non_empty_string(gpu_vendor, field_name="gpu_vendor")
    normalized_gpu_type = _normalize_optional_string(gpu_type, field_name="gpu_type")

    if isinstance(gpu, bool):
        raise ValueError("gpu must be None, a positive integer count, or a non-empty GPU type string")
    if isinstance(gpu, int):
        if gpu < 1:
            raise ValueError("gpu count must be at least 1")
        return gpu, vendor, normalized_gpu_type
    if isinstance(gpu, str):
        requested_type = gpu.strip()
        if not requested_type:
            raise ValueError("gpu type must not be empty")
        if normalized_gpu_type is not None:
            raise ValueError("gpu_type cannot be combined with a string gpu value; pass gpu as a count instead")
        return 1, vendor, requested_type
    raise ValueError("gpu must be None, a positive integer count, or a non-empty GPU type string")
