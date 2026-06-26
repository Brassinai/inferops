"""Tests for the dependency-free fake runtime."""

from __future__ import annotations

import unittest

from runtimes.nano_vllm.fake_runtime import FakeRuntime


class FakeRuntimeTest(unittest.TestCase):
    def test_health_and_readiness_follow_load_and_drain(self) -> None:
        runtime = FakeRuntime()

        self.assertTrue(runtime.health())
        self.assertFalse(runtime.readiness())
        runtime.load()
        self.assertTrue(runtime.readiness())
        runtime.start_drain()
        self.assertFalse(runtime.readiness())

    def test_streaming_yields_chunks_and_tracks_inflight(self) -> None:
        runtime = FakeRuntime()
        runtime.load()
        stream = runtime.generate_stream("abc", max_tokens=5)

        self.assertEqual(runtime.inflight, 0)
        self.assertEqual(next(stream), "a")
        self.assertEqual(runtime.inflight, 1)
        stream.close()

        self.assertEqual(runtime.inflight, 0)
        self.assertTrue(runtime.wait_for_drain(0.01))

    def test_drain_wait_is_bounded_while_request_is_active(self) -> None:
        runtime = FakeRuntime()
        runtime.load()
        stream = runtime.generate_stream("abc", max_tokens=3)
        next(stream)
        runtime.start_drain()

        self.assertFalse(runtime.wait_for_drain(0.001))
        stream.close()
        self.assertTrue(runtime.wait_for_drain(0.01))

    def test_drain_rejects_new_requests(self) -> None:
        runtime = FakeRuntime()
        runtime.load()
        runtime.start_drain()

        with self.assertRaisesRegex(RuntimeError, "draining"):
            next(runtime.generate_stream("x", max_tokens=1))


if __name__ == "__main__":
    unittest.main()
