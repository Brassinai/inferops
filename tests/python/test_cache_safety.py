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

    def test_shared_artifact_cache_references_same_identity_deployments(self) -> None:
        cache = shared_cache()
        deployments = [
            deployment("cpu-smollm-a", ""),
            deployment("cpu-smollm-b", ""),
            deployment("cpu-smollm-c", "", revision="other"),
            deployment("cpu-smollm-private", "", secret="hf-private"),
        ]

        refs = cache_references(
            cache,
            deployments,
            "/var/lib/inferops/models/default/artifacts/abc123",
        )

        self.assertEqual(refs, ["cpu-smollm-a", "cpu-smollm-b"])

    def test_shared_cache_reference_respects_explicit_cache_root(self) -> None:
        cache = shared_cache()
        deployments = [
            deployment("cpu-smollm-a", "", cache_root="/other-cache"),
            deployment("cpu-smollm-b", ""),
        ]

        refs = cache_references(
            cache,
            deployments,
            "/var/lib/inferops/models/default/artifacts/abc123",
        )

        self.assertEqual(refs, ["cpu-smollm-b"])


def shared_cache() -> dict:
    return {
        "metadata": {
            "name": "model-abc123-cache",
            "namespace": "default",
            "labels": {
                "inferops.dev/cache-mode": "shared",
                "inferops.dev/cache-identity": "abc123",
            },
            "annotations": {
                "inferops.dev/cache-source": "huggingface",
                "inferops.dev/cache-root": "/var/lib/inferops/models",
            },
        },
        "spec": {
            "modelRepo": "jc-builds/SmolLM2-135M-Instruct-Q4_K_M-GGUF",
            "revision": "main",
            "storage": {
                "path": "/var/lib/inferops/models/default/artifacts/abc123"
            },
        },
        "status": {},
    }


def deployment(
    name: str,
    cache_path: str,
    *,
    revision: str = "main",
    secret: str = "",
    cache_root: str = "",
) -> dict:
    secrets = {}
    if secret:
        secrets["huggingFaceTokenSecretName"] = secret
    cache_spec = {"enabled": True}
    if cache_root:
        cache_spec["path"] = cache_root
    return {
        "metadata": {"name": name, "namespace": "default"},
        "spec": {
            "cache": cache_spec,
            "model": {
                "repo": "jc-builds/SmolLM2-135M-Instruct-Q4_K_M-GGUF",
                "revision": revision,
            },
            "secrets": secrets,
        },
        "status": {"cache": {"path": cache_path}},
    }


if __name__ == "__main__":
    unittest.main()
