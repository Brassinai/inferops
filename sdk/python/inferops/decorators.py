"""Decorator helpers for declaring model deployments."""


def web_endpoint(*, method: str = "POST", path: str):
    """Declare a method as a web endpoint on a model class."""
    if not method:
        raise ValueError("method is required")
    if not path:
        raise ValueError("path is required")

    def wrapper(func):
        func.__inferops_endpoint__ = {"method": method, "path": path}
        return func

    return wrapper
