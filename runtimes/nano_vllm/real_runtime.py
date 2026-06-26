"""Real nano-vLLM runtime adapter backed by LLMEngine."""

import threading
import time
import warnings
import os
import subprocess
from typing import Generator, Optional


class RealRuntime:
    """Wraps the nano-vLLM LLMEngine with readiness, health, drain semantics,
    and request tracking for Kubernetes deployment.
    """

    def __init__(
        self,
        model_path: str,
        tensor_parallel_size: int = 1,
        enforce_eager: bool = False,
        dtype: str = "float16",
        max_num_seqs: int = 256,
        max_model_len: int = 8192,
    ):
        """Initialize the real runtime with an LLMEngine.

        Args:
            model_path: Path to the model (HF model ID or local path)
            tensor_parallel_size: Number of GPUs for tensor parallelism
            enforce_eager: Disable CUDA graph capture
            dtype: Model data type (float16, float32, etc.)
            max_num_seqs: Maximum concurrent sequences
            max_model_len: Maximum model sequence length
        """
        self.model_path = model_path
        self.tensor_parallel_size = tensor_parallel_size
        self.enforce_eager = enforce_eager
        self.dtype = dtype
        self.max_num_seqs = max_num_seqs
        self.max_model_len = max_model_len

        self._loaded = False
        self._draining = False
        self._inflight = 0
        self._lock = threading.Lock()
        self._engine: Optional[object] = None  # LLMEngine instance

    def load(self):
        """Load the model into memory via LLMEngine."""
        with self._lock:
            if self._loaded:
                return
            try:
                _validate_accelerator_compatibility()
                _prepare_triton_libcuda_path()
                # Import here to allow tests to skip heavy dependencies
                from nanovllm.engine.llm_engine import LLMEngine

                self._engine = LLMEngine(
                    self.model_path,
                    tensor_parallel_size=self.tensor_parallel_size,
                    enforce_eager=self.enforce_eager,
                    dtype=self.dtype,
                    max_num_seqs=self.max_num_seqs,
                    max_model_len=self.max_model_len,
                )
                _warn_if_smoke_adapter(self._engine)
                self._loaded = True
            except Exception as e:
                _cleanup_distributed()
                raise RuntimeError(f"Failed to initialize LLMEngine: {e}") from e

    def health(self) -> bool:
        """Health check always returns true (should be health of CUDA, disk, etc.)."""
        return True

    def readiness(self) -> bool:
        """Readiness check: model loaded and not draining."""
        with self._lock:
            return self._loaded and not self._draining

    @property
    def inflight(self) -> int:
        """Number of in-flight requests."""
        with self._lock:
            return self._inflight

    def start_drain(self):
        """Begin graceful shutdown: reject new requests but finish in-flight ones."""
        with self._lock:
            self._draining = True

    def stop_drain(self):
        """Resume accepting requests."""
        with self._lock:
            self._draining = False

    def _begin_request(self):
        """Track request start; raise if draining."""
        with self._lock:
            if self._draining:
                raise RuntimeError("Runtime is draining, refusing new requests")
            self._inflight += 1

    def _end_request(self):
        """Track request completion."""
        with self._lock:
            self._inflight = max(0, self._inflight - 1)

    def wait_for_drain(self, timeout: float) -> bool:
        """Wait until active requests finish or the timeout expires."""
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if self.inflight == 0:
                return True
            time.sleep(min(0.05, max(0, deadline - time.monotonic())))
        return self.inflight == 0

    def generate_stream(
        self, prompt: str, max_tokens: int = 32, temperature: float = 0.7
    ) -> Generator[str, None, None]:
        """Generate tokens via LLMEngine and stream as text chunks.

        Args:
            prompt: Input text
            max_tokens: Maximum tokens to generate
            temperature: Sampling temperature

        Yields:
            Text chunks (not full tokens, but incremental text)
        """
        self._begin_request()
        try:
            # Import here to avoid import overhead during startup
            from nanovllm.sampling_params import SamplingParams

            if not self._engine:
                raise RuntimeError("Engine not loaded")

            # Generate via the engine's streaming API
            sampling_params = SamplingParams(max_tokens=max_tokens, temperature=temperature)
            outputs = self._engine.stream_generate(prompt, sampling_params)

            # Stream output tokens as strings. The authoritative nano-vLLM
            # implementation currently yields strings; the text attribute is
            # retained for compatibility with output-wrapper implementations.
            for output in outputs:
                # output is a RequestOutput with text field
                if hasattr(output, "text"):
                    yield output.text
                else:
                    # Fallback if output structure is different
                    yield str(output)
        finally:
            self._end_request()

    def close(self):
        """Release engine workers and accelerator resources."""
        engine = self._engine
        self._engine = None
        self._loaded = False
        if engine and hasattr(engine, "exit"):
            try:
                engine.exit()
            except Exception:
                pass


def _validate_accelerator_compatibility() -> None:
    """Fail clearly when the installed PyTorch lacks kernels for the GPU."""
    import torch

    if not torch.cuda.is_available():
        return
    major, minor = torch.cuda.get_device_capability()
    required_arch = f"sm_{major}{minor}"
    supported_arches = set(torch.cuda.get_arch_list())
    if supported_arches and required_arch not in supported_arches:
        device_name = torch.cuda.get_device_name()
        supported = ", ".join(sorted(supported_arches))
        raise RuntimeError(
            f"PyTorch does not support {device_name} ({required_arch}); "
            f"this build contains: {supported}. Use a PyTorch CUDA build "
            "that includes the detected compute capability."
        )


def _cleanup_distributed() -> None:
    """Release a process group left behind by partial engine initialization."""
    try:
        import torch.distributed as distributed

        if distributed.is_available() and distributed.is_initialized():
            distributed.destroy_process_group()
    except Exception:
        pass


def _warn_if_smoke_adapter(engine: object) -> None:
    """Make untrained upstream smoke adapters visible to operators."""
    model_runner = getattr(engine, "model_runner", None)
    model = getattr(model_runner, "model", None)
    if model is not None and type(model).__name__ == "HFAdapter":
        warnings.warn(
            "nano-vLLM selected HFAdapter, an untrained transport smoke adapter; "
            "generated text does not represent pretrained model inference. Use a "
            "runtime-supported architecture such as Qwen3ForCausalLM for real inference.",
            RuntimeWarning,
            stacklevel=2,
        )


def _prepare_triton_libcuda_path() -> None:
    """Provide Triton an unversioned libcuda link on WSL-style driver mounts."""
    if os.environ.get("TRITON_LIBCUDA_PATH"):
        return
    try:
        linker_cache = subprocess.check_output(["/sbin/ldconfig", "-p"], text=True)
    except (OSError, subprocess.SubprocessError):
        return

    for line in linker_cache.splitlines():
        if "libcuda.so.1" not in line or "=>" not in line:
            continue
        source = line.rsplit("=>", 1)[1].strip()
        source_dir = os.path.dirname(source)
        if os.path.exists(os.path.join(source_dir, "libcuda.so")):
            return
        if not os.path.exists(source):
            continue

        link_dir = os.path.join(os.environ.get("HOME", "/tmp"), ".cache", "inferops", "libcuda")
        os.makedirs(link_dir, exist_ok=True)
        for name in ("libcuda.so", "libcuda.so.1"):
            link = os.path.join(link_dir, name)
            if not os.path.exists(link):
                os.symlink(source, link)
        os.environ["TRITON_LIBCUDA_PATH"] = link_dir
        return
