from __future__ import annotations

import importlib.util
from pathlib import Path
import unittest


SCRIPT_PATH = (
    Path(__file__).parents[2] / "scripts" / "sanitize_kubernetes_export.py"
)
SPEC = importlib.util.spec_from_file_location(
    "sanitize_kubernetes_export",
    SCRIPT_PATH,
)
sanitize_kubernetes_export = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(sanitize_kubernetes_export)


class KubernetesExportSanitizerTest(unittest.TestCase):
    def test_sanitizes_list_items_without_mutating_input(self) -> None:
        document = {
            "apiVersion": "v1",
            "kind": "List",
            "metadata": {"resourceVersion": "10"},
            "items": [
                {
                    "apiVersion": "inference.inferops.dev/v1alpha1",
                    "kind": "ModelDeployment",
                    "metadata": {
                        "name": "qwen",
                        "namespace": "default",
                        "uid": "old-uid",
                        "resourceVersion": "7",
                        "ownerReferences": [{"uid": "owner-uid"}],
                        "annotations": {
                            "kubectl.kubernetes.io/last-applied-configuration": "{}",
                            "inferops.dev/note": "preserve",
                        },
                    },
                    "spec": {"model": {"repo": "Qwen/Qwen2.5"}},
                    "status": {"phase": "Active", "assignedNode": "old-node"},
                }
            ],
        }

        sanitized = sanitize_kubernetes_export.sanitize_document(document)

        self.assertNotIn("metadata", sanitized)
        item = sanitized["items"][0]
        self.assertNotIn("status", item)
        self.assertNotIn("uid", item["metadata"])
        self.assertNotIn("resourceVersion", item["metadata"])
        self.assertNotIn("ownerReferences", item["metadata"])
        self.assertEqual(
            item["metadata"]["annotations"],
            {"inferops.dev/note": "preserve"},
        )
        self.assertEqual(item["spec"], document["items"][0]["spec"])
        self.assertIn("status", document["items"][0])

    def test_rejects_malformed_list(self) -> None:
        with self.assertRaisesRegex(ValueError, "items must be an array"):
            sanitize_kubernetes_export.sanitize_document(
                {"apiVersion": "v1", "kind": "List", "items": {}}
            )


if __name__ == "__main__":
    unittest.main()
