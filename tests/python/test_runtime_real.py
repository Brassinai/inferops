"""Unit tests for the nano-vLLM engine adapter."""

from __future__ import annotations

import concurrent.futures.thread  # Initialize executor hooks before sys.modules mocking.
import os
import sys
import tempfile
import types
import unittest
from unittest import mock

from runtimes.nano_vllm.real_runtime import RealRuntime, _prepare_triton_libcuda_path


class Output:
    def __init__(self, text: str):
        self.text = text


class Engine:
    def __init__(self, model_path: str, **kwargs):
        self.model_path = model_path
        self.kwargs = kwargs
        self.exited = False

    def stream_generate(self, prompt, sampling_params):
        self.request = (prompt, sampling_params)
        yield "first"
        yield Output(" second")

    def exit(self):
        self.exited = True


class HFAdapter:
    pass


class SamplingParams:
    def __init__(self, **kwargs):
        self.kwargs = kwargs


class CUDA:
    @staticmethod
    def is_available():
        return False


class RealRuntimeTest(unittest.TestCase):
    def modules(self):
        engine_module = types.ModuleType("nanovllm.engine.llm_engine")
        engine_module.LLMEngine = Engine
        sampling_module = types.ModuleType("nanovllm.sampling_params")
        sampling_module.SamplingParams = SamplingParams
        torch_module = types.ModuleType("torch")
        torch_module.cuda = CUDA()
        return {
            "nanovllm": types.ModuleType("nanovllm"),
            "nanovllm.engine": types.ModuleType("nanovllm.engine"),
            "nanovllm.engine.llm_engine": engine_module,
            "nanovllm.sampling_params": sampling_module,
            "torch": torch_module,
        }

    def test_load_generate_and_close(self) -> None:
        runtime = RealRuntime("/models/test", tensor_parallel_size=2, max_model_len=128)
        with mock.patch.dict(sys.modules, self.modules()):
            runtime.load()
            engine = runtime._engine
            chunks = list(runtime.generate_stream("hello", max_tokens=3, temperature=0.5))

        self.assertTrue(runtime.readiness())
        self.assertEqual(chunks, ["first", " second"])
        self.assertEqual(engine.model_path, "/models/test")
        self.assertEqual(engine.kwargs["tensor_parallel_size"], 2)
        self.assertEqual(engine.request[1].kwargs, {"max_tokens": 3, "temperature": 0.5})
        self.assertEqual(runtime.inflight, 0)

        runtime.close()
        self.assertTrue(engine.exited)
        self.assertFalse(runtime.readiness())

    def test_unsupported_gpu_architecture_fails_before_engine_load(self) -> None:
        class UnsupportedCUDA:
            @staticmethod
            def is_available():
                return True

            @staticmethod
            def get_device_capability():
                return (12, 0)

            @staticmethod
            def get_arch_list():
                return ["sm_80", "sm_90"]

            @staticmethod
            def get_device_name():
                return "NVIDIA GeForce RTX 5090"

        modules = self.modules()
        modules["torch"].cuda = UnsupportedCUDA()
        runtime = RealRuntime("/models/test")

        with mock.patch.dict(sys.modules, modules):
            with self.assertRaisesRegex(RuntimeError, "sm_120"):
                runtime.load()

        self.assertFalse(runtime.readiness())

    def test_untrained_smoke_adapter_emits_warning(self) -> None:
        class SmokeEngine(Engine):
            def __init__(self, model_path: str, **kwargs):
                super().__init__(model_path, **kwargs)
                self.model_runner = types.SimpleNamespace(model=HFAdapter())

        modules = self.modules()
        modules["nanovllm.engine.llm_engine"].LLMEngine = SmokeEngine
        runtime = RealRuntime("/models/test")

        with mock.patch.dict(sys.modules, modules):
            with self.assertWarnsRegex(RuntimeWarning, "untrained transport smoke adapter"):
                runtime.load()

        runtime.close()

    def test_prepares_unversioned_libcuda_link_for_triton(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            driver_dir = os.path.join(directory, "driver")
            home = os.path.join(directory, "home")
            os.makedirs(driver_dir)
            source = os.path.join(driver_dir, "libcuda.so.1")
            with open(source, "wb"):
                pass
            linker_cache = f"libcuda.so.1 (libc6,x86-64) => {source}\n"

            with mock.patch.dict(os.environ, {"HOME": home}, clear=True):
                with mock.patch("subprocess.check_output", return_value=linker_cache):
                    _prepare_triton_libcuda_path()

                link_dir = os.environ["TRITON_LIBCUDA_PATH"]
                self.assertEqual(os.path.realpath(os.path.join(link_dir, "libcuda.so")), source)
                self.assertEqual(os.path.realpath(os.path.join(link_dir, "libcuda.so.1")), source)

    def test_load_failure_does_not_become_ready(self) -> None:
        class BrokenEngine:
            def __init__(self, *_args, **_kwargs):
                raise OSError("broken")

        modules = self.modules()
        modules["nanovllm.engine.llm_engine"].LLMEngine = BrokenEngine
        runtime = RealRuntime("/models/test")

        with mock.patch.dict(sys.modules, modules):
            with self.assertRaisesRegex(RuntimeError, "Failed to initialize"):
                runtime.load()

        self.assertFalse(runtime.readiness())


if __name__ == "__main__":
    unittest.main()
