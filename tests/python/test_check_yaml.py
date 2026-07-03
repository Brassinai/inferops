from __future__ import annotations

import copy
import importlib.util
from pathlib import Path
import unittest


SCRIPT_PATH = Path(__file__).parents[2] / "scripts" / "check_yaml.py"
SPEC = importlib.util.spec_from_file_location("check_yaml", SCRIPT_PATH)
check_yaml = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(check_yaml)


class OpenAPISchemaCompatibilityTest(unittest.TestCase):
    def setUp(self) -> None:
        self.old = {
            "type": "object",
            "required": ["name"],
            "properties": {
                "name": {"type": "string"},
                "phase": {"type": "string", "enum": ["Pending", "Ready"]},
            },
        }

    def test_allows_additive_optional_property(self) -> None:
        new = copy.deepcopy(self.old)
        new["properties"]["description"] = {"type": "string"}

        check_yaml.compare_openapi_schema(self.old, new, "Example")

    def test_rejects_removed_property(self) -> None:
        new = copy.deepcopy(self.old)
        del new["properties"]["name"]

        with self.assertRaisesRegex(ValueError, "property was removed"):
            check_yaml.compare_openapi_schema(self.old, new, "Example")

    def test_rejects_new_required_field(self) -> None:
        new = copy.deepcopy(self.old)
        new["properties"]["description"] = {"type": "string"}
        new["required"].append("description")

        with self.assertRaisesRegex(ValueError, "added required fields"):
            check_yaml.compare_openapi_schema(self.old, new, "Example")

    def test_rejects_removed_enum_value(self) -> None:
        new = copy.deepcopy(self.old)
        new["properties"]["phase"]["enum"] = ["Ready"]

        with self.assertRaisesRegex(ValueError, "removed enum values"):
            check_yaml.compare_openapi_schema(self.old, new, "Example")


if __name__ == "__main__":
    unittest.main()
