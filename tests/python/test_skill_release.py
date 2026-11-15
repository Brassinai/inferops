from __future__ import annotations

from contextlib import redirect_stdout
from email.message import Message
import importlib.util
import hashlib
import io
import json
from pathlib import Path
import sys
import unittest
from unittest.mock import patch
import urllib.error

ROOT = Path(__file__).resolve().parents[2]
PATH = ROOT / "skills/inferops-install/scripts/resolve_release.py"
spec = importlib.util.spec_from_file_location("skill_release", PATH)
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)
SHA = "a" * 40
DIGEST = "sha256:" + "b" * 64


class FakeMetadata:
    def __init__(self, latest=None, tags=None, platform="amd64", missing=None, publications=None):
        self.latest = latest or {"tag_name": "v0.2.0", "draft": False, "prerelease": False}
        self.tags = tags or []
        self.platform = platform
        self.missing = missing
        self.publications = publications or {}
        self.calls = []

    def __call__(self, url, headers=None, **kwargs):
        self.calls.append((url, headers, kwargs))
        if url.endswith("/releases/latest"):
            if isinstance(self.latest, Exception):
                raise self.latest
            return self.latest, DIGEST
        if "/tags?" in url:
            return self.tags, DIGEST
        if "/releases/tags/" in url:
            tag = url.rsplit("/", 1)[1]
            if tag in self.publications:
                return self.publications[tag], DIGEST
            raise not_found()
        if "/commits/" in url:
            return {"sha": SHA}, DIGEST
        if "/contents/cli/inferops_cli/charts/" in url:
            if "/inferops-gateway/" in url:
                content = 'image:\n  repository: ghcr.io/brassinai/inferops-gateway\n  tag: "v0.1.0"\n'
            else:
                content = ('image:\n  repository: ghcr.io/brassinai/inferops-operator\n  tag: "v0.1.0"\n'
                           'cache:\n  downloaderImage: ghcr.io/brassinai/model-downloader:v0.1.0\nmodelRuntimes:\n')
                for runtime in ("llama-cpp", "nano-vllm", "vllm", "sglang"):
                    content += f'  {runtime}:\n    image:\n      repository: ghcr.io/brassinai/{runtime}\n      tag: "v0.1.0"\n'
            return {"encoding": "base64", "content": release.base64.b64encode(content.encode()).decode()}, DIGEST
        if url.startswith("https://ghcr.io/token?"):
            return {"token": "fixture-pull-token"}, DIGEST
        if self.missing and self.missing in url:
            raise urllib.error.HTTPError(url, 404, "fixture-private-error", {}, None)
        if "/manifests/sha256:" in url:
            return {"config": {"digest": DIGEST}}, DIGEST
        if "/manifests/" in url:
            return {"manifests": [
                {"digest": DIGEST, "platform": {"os": "linux", "architecture": self.platform}},
                {"digest": DIGEST, "platform": {"os": "unknown", "architecture": "unknown"}},
            ]}, DIGEST
        raise AssertionError("unexpected request")


def not_found():
    return urllib.error.HTTPError(release.API, 404, "not found", {}, None)


class ReleaseSelectionTests(unittest.TestCase):
    def test_latest_published_release_drives_images_and_source_commit(self):
        reader = FakeMetadata()
        with patch.dict(release.os.environ, {}, clear=True):
            report = release.resolve("linux/amd64", "llama-cpp", read=reader, channel="stable")
        self.assertEqual(report["image_tag"], "v0.2.0")
        self.assertEqual(report["selection"], "github_latest_release")
        self.assertIn("@" + SHA + "#subdirectory=cli", report["cli_spec"])
        self.assertIn("@" + SHA + "#subdirectory=sdk/python", report["sdk_spec"])
        self.assertEqual(len(report["verified_images"]), 4)
        self.assertTrue(all(v["image"].endswith(":v0.2.0") for v in report["verified_images"]))
        self.assertNotIn("fixture-pull-token", json.dumps(report))

    def test_explicit_version_does_not_query_latest(self):
        reader = FakeMetadata()
        with patch.dict(release.os.environ, {}, clear=True):
            report = release.resolve("linux/amd64", "vllm", tag="v0.1.5", read=reader, channel="stable")
        self.assertEqual(report["image_tag"], "v0.1.5")
        self.assertFalse(any("/releases/latest" in call[0] for call in reader.calls))
        self.assertIn("refs%2Ftags%2Fv0.1.5", reader.calls[0][0])

    def test_named_image_tag_can_use_explicit_source_ref(self):
        reader = FakeMetadata()
        with patch.dict(release.os.environ, {}, clear=True):
            report = release.resolve("linux/amd64", "nano-vllm", tag="cloud", source_ref=SHA, read=reader, channel="stable")
        self.assertEqual(report["image_tag"], "cloud")
        self.assertEqual(report["source_commit"], SHA)
        self.assertEqual(report["source_ref"], SHA)

    def test_tag_fallback_uses_numeric_versions_not_lexical_sort(self):
        reader = FakeMetadata(latest=not_found(), tags=[
            {"name": "v0.9.0"}, {"name": "v0.10.0"}, {"name": "v9.0.0-rc.1"},
            {"name": "latest"}, {"name": "cloud"},
        ])
        self.assertEqual(release.latest_tag(reader), ("v0.10.0", "github_highest_stable_tag"))

    def test_draft_and_prerelease_never_selected_automatically(self):
        for field in ("draft", "prerelease"):
            reader = FakeMetadata(latest={"tag_name": "v0.3.0", field: True})
            with self.subTest(field=field), self.assertRaises(release.ResolutionError):
                release.latest_tag(reader)

    def test_numeric_tag_marked_prerelease_is_excluded_from_fallback(self):
        reader = FakeMetadata(latest=not_found(), tags=[
            {"name": "v0.3.0"}, {"name": "v0.2.0"},
        ], publications={"v0.3.0": {"prerelease": True}})
        self.assertEqual(release.latest_tag(reader), ("v0.2.0", "github_highest_stable_tag"))

    def test_no_releases_or_tags_has_no_chart_default_fallback(self):
        reader = FakeMetadata(latest=not_found())
        with self.assertRaisesRegex(release.ResolutionError, "no stable"):
            release.resolve("linux/amd64", "llama-cpp", read=reader, channel="stable")
        self.assertFalse(any("ghcr.io" in call[0] for call in reader.calls))

    def test_github_permission_failure_does_not_fall_back(self):
        reader = FakeMetadata(latest=urllib.error.HTTPError(release.API, 403, "limited", {}, None))
        with self.assertRaises(urllib.error.HTTPError):
            release.latest_tag(reader)
        self.assertEqual(len(reader.calls), 1)

    def test_incomplete_tag_inventory_requires_explicit_selection(self):
        reader = FakeMetadata(latest=not_found(), tags=[{"name": "v0.1.0"}] * 100)
        with self.assertRaisesRegex(release.ResolutionError, "discovery limit"):
            release.latest_tag(reader)

    def test_missing_image_blocks_selected_release_without_downgrading(self):
        reader = FakeMetadata(missing="/model-downloader/")
        with patch.dict(release.os.environ, {}, clear=True):
            with self.assertRaisesRegex(release.ResolutionError, "model-downloader.*404"):
                release.resolve("linux/amd64", "llama-cpp", read=reader, channel="stable")
        self.assertFalse(any("/tags?" in call[0] for call in reader.calls))

    def test_architecture_mismatch_blocks_install(self):
        reader = FakeMetadata(platform="amd64")
        with patch.dict(release.os.environ, {}, clear=True):
            with self.assertRaisesRegex(release.ResolutionError, "no image for linux/arm64"):
                release.resolve("linux/arm64", "llama-cpp", read=reader, channel="stable")

    def test_only_selected_runtime_is_checked(self):
        reader = FakeMetadata()
        with patch.dict(release.os.environ, {}, clear=True):
            report = release.resolve("linux/amd64", "vllm", read=reader, channel="stable")
        images = [v["image"] for v in report["verified_images"]]
        self.assertTrue(any("/vllm:" in image for image in images))
        self.assertFalse(any("/llama-cpp:" in image for image in images))

    def test_source_and_registry_credentials_remain_separate(self):
        reader = FakeMetadata()
        with patch.dict(release.os.environ, {
            "GITHUB_TOKEN": "fixture-source-secret",
            "GHCR_USER": "fixture-user", "GHCR_TOKEN": "fixture-registry-secret",
        }, clear=True):
            report = release.resolve("linux/amd64", "llama-cpp", read=reader, channel="stable")
        for url, headers, _ in reader.calls:
            authorization = (headers or {}).get("Authorization", "")
            if url.startswith(release.API):
                self.assertEqual(authorization, "Bearer fixture-source-secret")
            elif "/token?" in url:
                self.assertTrue(authorization.startswith("Basic "))
            else:
                self.assertEqual(authorization, "Bearer fixture-pull-token")
        self.assertNotIn("fixture-", json.dumps(report))

    def test_invalid_source_platform_and_tag_do_not_make_requests(self):
        for kwargs in (
            {"platform": "darwin/arm64", "runtime": "llama-cpp"},
            {"platform": "linux/amd64", "runtime": "unknown"},
            {"platform": "linux/amd64", "runtime": "llama-cpp", "tag": "--bad"},
            {"platform": "linux/amd64", "runtime": "llama-cpp", "source_ref": "main"},
        ):
            reader = FakeMetadata()
            with self.subTest(kwargs=kwargs), self.assertRaises(release.ResolutionError):
                release.resolve(**kwargs, read=reader, channel="stable")
            self.assertEqual(reader.calls, [])


class OperatorMetadata(FakeMetadata):
    def __init__(self, images=None, **kwargs):
        super().__init__(**kwargs)
        self.images = images if images is not None else {
            "v0.9.0": {"created": "2026-01-01T00:00:00Z"},
            "aaaaaaa": {"created": "2026-02-01T00:00:00Z"},
        }
        self.configs = {}

    def __call__(self, url, headers=None, **kwargs):
        if "/tags/list?" in url:
            self.calls.append((url, headers, kwargs))
            return {"tags": list(self.images)}, DIGEST
        if self.missing and self.missing in url:
            raise not_found()
        if "/manifests/" in url:
            self.calls.append((url, headers, kwargs))
            tag = url.rsplit("/", 1)[1]
            settings = self.images.get(tag, {})
            digest = "sha256:" + hashlib.sha256(settings.get("alias", tag).encode()).hexdigest()
            self.configs[digest] = {"os": "linux", "architecture": self.platform,
                "created": settings.get("created"),
                "config": {"Labels": {"org.opencontainers.image.revision": settings.get("revision", SHA)}}}
            return {"config": {"digest": digest}}, digest
        if "/blobs/" in url:
            self.calls.append((url, headers, kwargs))
            return self.configs[url.rsplit("/", 1)[1]], DIGEST
        return super().__call__(url, headers, **kwargs)


class OperatorSelectionTests(unittest.TestCase):
    def test_default_uses_newest_operator_build_without_a_github_release(self):
        reader = OperatorMetadata(latest=not_found())
        report = release.resolve("linux/amd64", "llama-cpp", read=reader)
        self.assertEqual(report["image_tag"], "aaaaaaa")
        self.assertEqual(report["selection"], "registry_latest_operator")
        self.assertEqual(report["source_commit"], SHA)
        self.assertEqual(len(report["verified_images"]), 5)
        self.assertEqual(report["chart_default_images"]["inferops-gateway"], "ghcr.io/brassinai/inferops-gateway:v0.1.0")
        self.assertTrue(report["operator_upgrade_required"])
        self.assertFalse(any('/inferops-gateway/manifests/aaaaaaa' in url for url, _, _ in reader.calls))
        self.assertFalse(any("/releases/" in url or "/tags?" in url for url, _, _ in reader.calls))

    def test_commit_tag_resolves_source_when_oci_revision_is_absent(self):
        reader = OperatorMetadata(images={"aaaaaaa": {"created": "2026-01-01T00:00:00Z", "revision": None}})
        self.assertEqual(release.resolve("linux/amd64", "llama-cpp", read=reader)["source_commit"], SHA)

    def test_explicit_operator_tag_also_preserves_chart_companion_versions(self):
        reader = OperatorMetadata()
        report = release.resolve("linux/amd64", "llama-cpp", tag="aaaaaaa", read=reader)
        self.assertFalse(any('/tags/list?' in url for url, _, _ in reader.calls))
        self.assertTrue(any('/inferops-gateway/manifests/v0.1.0' in url for url, _, _ in reader.calls))
        self.assertTrue(report["operator_upgrade_required"])

    def test_chart_image_values_require_unambiguous_literal_fields(self):
        self.assertEqual(release.chart_scalar('image:\n  tag: "v0.1.0" # pinned\n', ("image", "tag")), "v0.1.0")
        for text in ['image:\n  tag: *alias\n', 'image:\n  tag: v1\n  tag: v2\n', 'unrelated:\n  tag: v1\n']:
            with self.subTest(text=text), self.assertRaises(release.ResolutionError):
                release.chart_scalar(text, ("image", "tag"))

    def test_unavailable_chart_defaults_do_not_guess_dependency_versions(self):
        class MissingChart(OperatorMetadata):
            def __call__(self, url, headers=None, **kwargs):
                if '/contents/' in url:
                    return {"encoding": "none"}, DIGEST
                return super().__call__(url, headers, **kwargs)
        with self.assertRaisesRegex(release.ResolutionError, "defaults are unavailable"):
            release.resolve("linux/amd64", "llama-cpp", read=MissingChart())

    def test_floating_label_does_not_guess_a_source_branch(self):
        reader = OperatorMetadata(images={"cloud": {"created": "2026-01-01T00:00:00Z", "revision": None}})
        with self.assertRaisesRegex(release.ResolutionError, "source revision"):
            release.resolve("linux/amd64", "llama-cpp", read=reader)

    def test_commit_alias_preferred_over_latest_for_same_image(self):
        reader = OperatorMetadata(images={
            "latest": {"created": "2026-01-01T00:00:00Z", "alias": "aaaaaaa"},
            "v0.1.0": {"created": "2026-01-01T00:00:00Z", "alias": "aaaaaaa"},
            "aaaaaaa": {"created": "2026-01-01T00:00:00Z"},
        })
        self.assertEqual(release.resolve("linux/amd64", "llama-cpp", read=reader)["image_tag"], "aaaaaaa")

    def test_ambiguous_build_times_and_missing_dates_do_not_guess(self):
        cases = [{}, {"created": "invalid"}, {"created": "2026-01-01T00:00:00"}]
        for settings in cases:
            with self.subTest(settings=settings), self.assertRaises(release.ResolutionError):
                release.latest_operator(OperatorMetadata(images={"cloud": settings}))
        reader = OperatorMetadata(images={name: {"created": "2026-01-01T00:00:00Z"} for name in ("cloud", "latest")})
        with self.assertRaisesRegex(release.ResolutionError, "tied"):
            release.latest_operator(reader)

    def test_newest_image_architecture_and_missing_companions_block_without_downgrade(self):
        with self.assertRaisesRegex(release.ResolutionError, "does not target"):
            release.resolve("linux/arm64", "llama-cpp", read=OperatorMetadata())
        with self.assertRaisesRegex(release.ResolutionError, "model-downloader.*404"):
            release.resolve("linux/amd64", "llama-cpp", read=OperatorMetadata(missing="/model-downloader/"))

    def test_source_revision_must_match_resolved_commit(self):
        class WrongCommit(OperatorMetadata):
            def __call__(self, url, headers=None, **kwargs):
                if "/commits/" in url:
                    return {"sha": "c" * 40}, DIGEST
                return super().__call__(url, headers, **kwargs)
        with self.assertRaisesRegex(release.ResolutionError, "does not match"):
            release.resolve("linux/amd64", "llama-cpp", read=WrongCommit())

    def test_tag_move_during_discovery_is_rejected(self):
        class MovingTag(OperatorMetadata):
            count = 0
            def __call__(self, url, headers=None, **kwargs):
                result, digest = super().__call__(url, headers, **kwargs)
                if "/inferops-operator/manifests/aaaaaaa" in url:
                    self.count += 1
                    if self.count > 1:
                        digest = DIGEST
                return result, digest
        with self.assertRaisesRegex(release.ResolutionError, "tag changed"):
            release.resolve("linux/amd64", "llama-cpp", read=MovingTag())

    def test_empty_and_unbounded_inventories_are_not_latest(self):
        with self.assertRaisesRegex(release.ResolutionError, "no operator"):
            release.latest_operator(OperatorMetadata(images={}))
        batches = [[f"tag{i:03}" for i in range(start, start + 100)] for start in (0, 100, 200)]
        def reader(url, headers=None, **kwargs):
            return ({"token": "synthetic"} if "/token?" in url else {"tags": batches.pop(0)}), DIGEST
        with self.assertRaisesRegex(release.ResolutionError, "discovery limit"):
            release.operator_tags(reader)


class RegistryMetadataTests(unittest.TestCase):
    def test_single_platform_manifest_requires_matching_config(self):
        for architecture in ("arm64", "amd64"):
            def reader(url, headers=None, **kwargs):
                if "/token?" in url:
                    return {"token": "fixture"}, DIGEST
                if "/manifests/" in url:
                    return {"config": {"digest": DIGEST}}, DIGEST
                return {"os": "linux", "architecture": architecture}, DIGEST
            with self.subTest(architecture=architecture), patch.dict(release.os.environ, {}, clear=True):
                if architecture == "arm64":
                    report = release.image_manifest("llama-cpp", "v0.2.0", "linux/arm64", reader)
                    self.assertEqual(report["platform"], "linux/arm64")
                else:
                    with self.assertRaises(release.ResolutionError):
                        release.image_manifest("llama-cpp", "v0.2.0", "linux/arm64", reader)

    def test_redirected_blob_drops_authorization(self):
        headers = Message()
        headers["Location"] = "https://pkg-containers.githubusercontent.com/config?signature=fixture"
        redirect = urllib.error.HTTPError("https://ghcr.io/blob", 307, "redirect", headers, None)
        response = io.BytesIO(b'{"os":"linux","architecture":"amd64"}')
        response.headers = {}
        with patch.object(release.urllib.request, "build_opener") as build:
            build.return_value.open.side_effect = [redirect, response]
            release.read_json("https://ghcr.io/blob", {"Authorization": "Bearer fixture"}, blob_redirect=True)
            request = build.return_value.open.call_args_list[1].args[0]
            self.assertIsNone(request.get_header("Authorization"))

    def test_untrusted_redirect_does_not_receive_a_request(self):
        headers = Message()
        headers["Location"] = "https://untrusted.example/metadata"
        redirect = urllib.error.HTTPError("https://ghcr.io/blob", 307, "redirect", headers, None)
        with patch.object(release.urllib.request, "build_opener") as build:
            build.return_value.open.side_effect = redirect
            with self.assertRaises(release.ResolutionError):
                release.read_json("https://ghcr.io/blob", {"Authorization": "Bearer fixture"}, blob_redirect=True)
            self.assertEqual(build.return_value.open.call_count, 1)

    def test_digest_mismatch_blocks_metadata_verification(self):
        response = io.BytesIO(b'{"architecture":"amd64"}')
        response.headers = {}
        with patch.object(release.urllib.request, "build_opener") as build:
            build.return_value.open.return_value = response
            with self.assertRaisesRegex(release.ResolutionError, "digest mismatch"):
                release.read_json("https://ghcr.io/blob", expected_digest=DIGEST)

    def test_cli_error_does_not_include_remote_error_body(self):
        output = io.StringIO()
        failure = urllib.error.HTTPError("https://ghcr.io", 401, "fixture-secret", {}, io.BytesIO(b"fixture-secret"))
        with patch.object(sys, "argv", ["resolve", "--platform", "linux/amd64", "--runtime", "llama-cpp"]):
            with patch.object(release, "resolve", side_effect=failure), redirect_stdout(output):
                self.assertEqual(release.main(), 1)
        self.assertNotIn("fixture-secret", output.getvalue())


if __name__ == "__main__":
    unittest.main()
