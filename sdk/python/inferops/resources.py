"""Resource requirement helpers."""


class Resources:
    """Describes CPU, memory, and GPU requirements."""

    def __init__(self, cpu: str | None = None, memory: str | None = None, gpu: str | None = None):
        self.cpu = cpu
        self.memory = memory
        self.gpu = gpu
