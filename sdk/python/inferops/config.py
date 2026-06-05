"""SDK configuration."""


class Config:
    """Holds SDK configuration for Kubernetes deployment."""

    def __init__(self, namespace: str = "default"):
        if not namespace:
            raise ValueError("namespace is required")
        self.namespace = namespace
