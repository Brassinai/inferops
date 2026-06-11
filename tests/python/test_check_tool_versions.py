import importlib.util
from pathlib import Path
import unittest
from unittest import mock


SCRIPT_PATH = Path(__file__).parents[2] / "scripts" / "check_tool_versions.py"
SPEC = importlib.util.spec_from_file_location("check_tool_versions", SCRIPT_PATH)
check_tool_versions = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(check_tool_versions)


class KubeconformVersionTest(unittest.TestCase):
    @mock.patch.object(check_tool_versions.shutil, "which")
    @mock.patch.object(check_tool_versions, "output")
    def test_resolves_command_before_reading_go_build_metadata(self, output, which):
        output.side_effect = [
            "development",
            (
                "/tmp/bin/kubeconform: go1.22.0\n"
                "\tpath\tgithub.com/yannh/kubeconform/cmd/kubeconform\n"
                "\tmod\tgithub.com/yannh/kubeconform\tv0.6.7"
            ),
        ]
        which.return_value = "/tmp/bin/kubeconform"

        version = check_tool_versions.kubeconform_version("go", "kubeconform")

        self.assertEqual(version, "\tmod\tgithub.com/yannh/kubeconform\tv0.6.7")
        which.assert_called_once_with("kubeconform")
        output.assert_has_calls(
            [
                mock.call("kubeconform", "-v"),
                mock.call("go", "version", "-m", "/tmp/bin/kubeconform"),
            ]
        )

    @mock.patch.object(check_tool_versions.shutil, "which")
    @mock.patch.object(check_tool_versions, "output", return_value="v0.6.7")
    def test_uses_reported_version_without_reading_build_metadata(self, output, which):
        version = check_tool_versions.kubeconform_version("go", "kubeconform")

        self.assertEqual(version, "v0.6.7")
        which.assert_not_called()
        output.assert_called_once_with("kubeconform", "-v")


if __name__ == "__main__":
    unittest.main()
