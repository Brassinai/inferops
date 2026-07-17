"""Tests for cache-reference classification."""

from __future__ import annotations

import unittest

from inferops_cli.cache_safety import cache_references


class CacheReferenceTest(unittest.TestCase):
    def test_same_model_repo_does_not_reference_generated_cache(self) -> None:
        cache = {
            "metadata": {
                "name": "cpu-smollm-a-f719162840b8-cache",
                "labels": {"inferops.dev/modeldeployment": "cpu-smollm-a"},
            },
            "spec": {
                "modelRepo": "jc-builds/SmolLM2-135M-Instruct-Q4_K_M-GGUF",
                "revision": "main",
                "storage": {
                    "path": "/var/lib/inferops/models/default/cpu-smollm-a-f719162840b8-cache"
                },
            },
            "status": {
                "path": "/var/lib/inferops/models/default/cpu-smollm-a-f719162840b8-cache"
            },
        }
        deployments = [
            deployment(
                "cpu-smollm-a",
                "/var/lib/inferops/models/default/cpu-smollm-a-f719162840b8-cache",
            ),
            deployment(
                "cpu-smollm-b",
                "/var/lib/inferops/models/default/cpu-smollm-b-2150a608ab82-cache",
            ),
            deployment(
                "cpu-smollm-decorator",
                "/var/lib/inferops/models/default/cpu-smollm-decorator-ea08fa59cc11-cache",
            ),
        ]

        refs = cache_references(
            cache,
            deployments,
            "/var/lib/inferops/models/default/cpu-smollm-a-f719162840b8-cache",
        )

        self.assertEqual(refs, ["cpu-smollm-a"])


def deployment(name: str, cache_path: str) -> dict:
    return {
        "metadata": {"name": name},
        "spec": {
            "cache": {"enabled": True},
            "model": {
                "repo": "jc-builds/SmolLM2-135M-Instruct-Q4_K_M-GGUF",
                "revision": "main",
            },
        },
        "status": {"cache": {"path": cache_path}},
    }


if __name__ == "__main__":
    unittest.main()
