"""nano_vllm runtime adapter package (fake mode and real LLMEngine mode supported)."""

from .fake_runtime import FakeRuntime
from .real_runtime import RealRuntime

__all__ = ["FakeRuntime", "RealRuntime", "create_app", "make_runtime_from_env"]


def __getattr__(name):
    """Load server helpers lazily so ``python -m`` has a clean module lifecycle."""
    if name in ("create_app", "make_runtime_from_env"):
        from . import server

        return getattr(server, name)
    raise AttributeError(name)
