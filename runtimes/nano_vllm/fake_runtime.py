import threading
import time
from typing import Generator


class FakeRuntime:
    """A minimal fake runtime that mimics readiness, health, metrics,
    streaming generation, and bounded drain semantics for testing.
    """

    def __init__(self):
        self._loaded = False
        self._draining = False
        self._inflight = 0
        self._lock = threading.Lock()

    def load(self):
        """Simulate loading the model into memory."""
        with self._lock:
            self._loaded = True

    def health(self) -> bool:
        return True

    def readiness(self) -> bool:
        with self._lock:
            return self._loaded and not self._draining

    @property
    def inflight(self) -> int:
        with self._lock:
            return self._inflight

    def start_drain(self):
        with self._lock:
            self._draining = True

    def stop_drain(self):
        with self._lock:
            self._draining = False

    def _begin_request(self):
        with self._lock:
            if self._draining:
                raise RuntimeError("Runtime is draining, refusing new requests")
            self._inflight += 1

    def _end_request(self):
        with self._lock:
            self._inflight = max(0, self._inflight - 1)

    def wait_for_drain(self, timeout: float) -> bool:
        """Wait until active requests finish or the timeout expires."""
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if self.inflight == 0:
                return True
            time.sleep(min(0.01, max(0, deadline - time.monotonic())))
        return self.inflight == 0

    def close(self):
        """Release runtime resources."""

    def generate_stream(
        self, prompt: str, max_tokens: int = 32, temperature: float = 0.7
    ) -> Generator[str, None, None]:
        """Yield a deterministic stream of token-like strings based on the
        prompt. This is synchronous generator used for testing streaming
        semantics.

        Yields small pieces (strings) to simulate incremental token output.
        """
        del temperature
        self._begin_request()
        try:
            # yield each character encoded as a pseudo-token
            produced = 0
            for ch in prompt:
                if produced >= max_tokens:
                    break
                yield ch
                produced += 1
            # if we still need to produce completion tokens, emit numeric tokens
            while produced < max_tokens:
                yield f"<{produced}>"
                produced += 1
        finally:
            self._end_request()
