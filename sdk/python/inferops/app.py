"""Application declaration primitives."""


class App:
    """Represents a deployable inference application."""

    def __init__(self, name: str):
        if not name:
            raise ValueError("app name is required")
        self.name = name
        self.models = []

    def register(self, model_config: dict) -> dict:
        """Register a model declaration with the app."""
        self.models.append(model_config)
        return model_config

    def model(
        self,
        *,
        name: str,
        engine: str,
        model: str,
        gpu: str | int | None = None,
        cpu: str | None = None,
        memory: str | None = None,
        min_replicas: int | None = None,
        max_replicas: int | None = None,
        max_model_len: int | None = None,
        **extra,
    ):
        """Declare a class as a model deployment."""
        if not name:
            raise ValueError("model name is required")
        if not engine:
            raise ValueError("engine is required")
        if not model:
            raise ValueError("model is required")

        def wrapper(cls):
            config = {
                "name": name,
                "engine": engine,
                "model": model,
                "gpu": gpu,
                "cpu": cpu,
                "memory": memory,
                "min_replicas": min_replicas,
                "max_replicas": max_replicas,
                "max_model_len": max_model_len,
                "class": cls,
                "extra": extra,
            }
            cls.__inferops_model__ = config
            self.models.append(config)
            return cls

        return wrapper
