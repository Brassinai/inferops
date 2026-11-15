#!/usr/bin/env python3
"""Resolve the newest published operator image and verify its installation set."""

from __future__ import annotations

import argparse
import base64
from datetime import datetime
import hashlib
import json
import os
import re
import sys
import urllib.error
import urllib.parse
import urllib.request

API = "https://api.github.com/repos/brassinai/inferops"
GIT = "https://github.com/brassinai/inferops.git"
IMAGE_TAG = re.compile(r"^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$")
STABLE_TAG = re.compile(r"^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$")
DIGEST = re.compile(r"^sha256:[0-9a-f]{64}$")
ACCEPT = ", ".join((
    "application/vnd.oci.image.index.v1+json",
    "application/vnd.docker.distribution.manifest.list.v2+json",
    "application/vnd.oci.image.manifest.v1+json",
    "application/vnd.docker.distribution.manifest.v2+json",
))


class ResolutionError(ValueError):
    pass


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def read_json(url, headers=None, *, expected_digest=None, blob_redirect=False):
    """Read bounded metadata; never forward credentials through a redirect."""
    headers = dict(headers or {})
    opener = urllib.request.build_opener(NoRedirect)
    for _ in range(4):
        try:
            with opener.open(urllib.request.Request(url, headers=headers), timeout=15) as response:
                body = response.read(8 * 1024**2 + 1)
                claimed_digest = response.headers.get("Docker-Content-Digest")
            break
        except urllib.error.HTTPError as exc:
            if not blob_redirect or exc.code not in (301, 302, 303, 307, 308):
                raise
            location = urllib.parse.urljoin(url, exc.headers.get("Location", ""))
            target = urllib.parse.urlsplit(location)
            if (target.scheme != "https" or target.username or target.password
                    or target.hostname not in ("ghcr.io", "pkg-containers.githubusercontent.com")):
                raise ResolutionError("untrusted image metadata redirect") from None
            url, headers = location, {}
    else:
        raise ResolutionError("too many image metadata redirects")
    if len(body) > 8 * 1024**2:
        raise ResolutionError("metadata exceeds size limit")
    digest = "sha256:" + hashlib.sha256(body).hexdigest()
    if expected_digest and digest != expected_digest:
        raise ResolutionError("image metadata digest mismatch")
    if claimed_digest and claimed_digest != digest:
        raise ResolutionError("registry manifest digest mismatch")
    return json.loads(body), digest


def github(path, read):
    headers = {"Accept": "application/vnd.github+json", "User-Agent": "inferops-install-skill"}
    token = os.environ.get("GH_TOKEN") or os.environ.get("GITHUB_TOKEN")
    if token:
        headers["Authorization"] = "Bearer " + token
    return read(API + "/" + path, headers)[0]


def latest_tag(read):
    try:
        release = github("releases/latest", read)
    except urllib.error.HTTPError as exc:
        if exc.code != 404:
            raise
    else:
        tag = release.get("tag_name")
        if release.get("draft") or release.get("prerelease") or not isinstance(tag, str) or not STABLE_TAG.fullmatch(tag):
            raise ResolutionError("latest release is not a stable version; select an explicit tag")
        return tag, "github_latest_release"
    # GitHub does not include plain tags in the release endpoint.
    candidates = []
    for page in range(1, 11):
        batch = github(f"tags?per_page=100&page={page}", read)
        if not isinstance(batch, list):
            raise ResolutionError("invalid GitHub tag inventory")
        for item in batch:
            tag = item.get("name")
            match = STABLE_TAG.fullmatch(tag) if isinstance(tag, str) else None
            if match:
                candidates.append((tuple(map(int, match.groups())), tag))
        if len(batch) < 100:
            break
    else:
        raise ResolutionError("tag inventory exceeds discovery limit; select an explicit tag")
    if not candidates:
        raise ResolutionError("no stable InferOps release or version tag is published; provide an explicit published image tag and source ref")
    for _, tag in sorted(set(candidates), reverse=True)[:20]:
        try:
            publication = github("releases/tags/" + urllib.parse.quote(tag, safe=""), read)
        except urllib.error.HTTPError as exc:
            if exc.code != 404:
                raise
        else:
            if publication.get("draft") or publication.get("prerelease"):
                continue
        return tag, "github_highest_stable_tag"
    raise ResolutionError("no eligible stable tag within the publication-check limit; select an explicit tag")


def platform_matches(actual, requested):
    parts = requested.split("/")
    return (actual.get("os") == parts[0] and actual.get("architecture") == parts[1]
            and (len(parts) == 2 or actual.get("variant") == parts[2]))


def registry_headers(repository, read):
    query = urllib.parse.urlencode({"service": "ghcr.io", "scope": f"repository:brassinai/{repository}:pull"})
    auth_headers = {}
    user, password = os.environ.get("GHCR_USER"), os.environ.get("GHCR_TOKEN")
    if bool(user) != bool(password):
        raise ResolutionError("private GHCR access requires both GHCR_USER and GHCR_TOKEN")
    if user and password:
        auth_headers["Authorization"] = "Basic " + base64.b64encode(f"{user}:{password}".encode()).decode()
    credential = read("https://ghcr.io/token?" + query, auth_headers)[0]
    bearer = credential.get("token") or credential.get("access_token")
    if not isinstance(bearer, str) or not bearer:
        raise ResolutionError("GHCR returned no pull credential")
    return {"Authorization": "Bearer " + bearer, "Accept": ACCEPT}


def operator_tags(read):
    """Read a complete, bounded tag inventory; registry order is not recency."""
    headers = registry_headers("inferops-operator", read)
    tags = []
    last = ""
    for _ in range(3):
        query = urllib.parse.urlencode({"n": 100, "last": last})
        inventory = read("https://ghcr.io/v2/brassinai/inferops-operator/tags/list?" + query, headers)[0]
        if not isinstance(inventory, dict) or "tags" not in inventory:
            raise ResolutionError("invalid operator tag inventory")
        batch = inventory["tags"]
        if batch is None:
            batch = []
        if (not isinstance(batch, list) or any(not isinstance(tag, str) or not IMAGE_TAG.fullmatch(tag) for tag in batch)
                or len(set(batch)) != len(batch) or any(tag <= last for tag in batch)):
            raise ResolutionError("invalid operator tag inventory")
        tags.extend(sorted(batch))
        if len(tags) > 200:
            raise ResolutionError("operator tag inventory exceeds discovery limit; select an explicit tag")
        if len(batch) < 100:
            return tags
        last = tags[-1]
    raise ResolutionError("operator tag inventory exceeds discovery limit; select an explicit tag")


def image_manifest(repository, tag, platform, read, *, metadata=False):
    try:
        return _image_manifest(repository, tag, platform, read, metadata=metadata)
    except urllib.error.HTTPError as exc:
        raise ResolutionError(f"{repository}:{tag} metadata HTTP {exc.code}; verify this image is published and accessible") from None


def _image_manifest(repository, tag, platform, read, *, metadata=False):
    headers = registry_headers(repository, read)
    root = f"https://ghcr.io/v2/brassinai/{repository}"
    manifest, digest = read(root + "/manifests/" + urllib.parse.quote(tag, safe=""), headers)
    if not isinstance(manifest, dict):
        raise ResolutionError("invalid image manifest")
    if "manifests" in manifest:
        matches = [entry for entry in manifest["manifests"]
                   if (platform_matches(entry.get("platform", {}), platform) if platform else
                       entry.get("platform", {}).get("os") == "linux")]
        if not matches:
            raise ResolutionError(f"{repository}:{tag} has no image for {platform}")
        selected = matches[0].get("digest", "")
        if not DIGEST.fullmatch(selected):
            raise ResolutionError("invalid platform manifest digest")
        child, _ = read(root + "/manifests/" + selected, headers, expected_digest=selected)
        if not isinstance(child, dict) or not isinstance(child.get("config"), dict):
            raise ResolutionError("platform image manifest is incomplete")
        image = child
    else:
        selected = digest
        image = manifest
    config = None
    if metadata or "manifests" not in manifest:
        config_digest = image.get("config", {}).get("digest", "")
        if not DIGEST.fullmatch(config_digest):
            raise ResolutionError("single-platform image has no valid config digest")
        config, _ = read(root + "/blobs/" + config_digest, headers,
                         expected_digest=config_digest, blob_redirect=True)
        if platform and not platform_matches(config, platform):
            raise ResolutionError(f"{repository}:{tag} does not target {platform}")
    result = {
        "image": f"ghcr.io/brassinai/{repository}:{tag}",
        "manifest_digest": digest,
        "platform_digest": selected,
        "platform": platform,
    }
    if metadata:
        annotations = {**manifest.get("annotations", {}), **image.get("annotations", {})}
        labels = config.get("config", {}).get("Labels") or {}
        result.update(
            created=config.get("created") or annotations.get("org.opencontainers.image.created"),
            source_revision=labels.get("org.opencontainers.image.revision") or annotations.get("org.opencontainers.image.revision"),
        )
    return result


def latest_operator(read):
    candidates = []
    for tag in operator_tags(read):
        image = image_manifest("inferops-operator", tag, None, read, metadata=True)
        try:
            created = datetime.fromisoformat(image["created"].replace("Z", "+00:00"))
            if created.utcoffset() is None:
                raise ValueError("missing timezone")
        except (AttributeError, TypeError, ValueError):
            raise ResolutionError(f"operator:{tag} has no valid image creation time; select an explicit tag") from None
        # Prefer a commit/version alias over a floating label for the same image.
        concrete = 2 if re.fullmatch(r"[0-9a-fA-F]{7,40}", tag) else int(bool(STABLE_TAG.fullmatch(tag)))
        candidates.append((created, concrete, tag, image))
    if not candidates:
        raise ResolutionError("no operator image tags are published")
    candidates.sort(key=lambda item: item[:3], reverse=True)
    newest = candidates[0]
    if any(item[0] == newest[0] and item[3]["manifest_digest"] != newest[3]["manifest_digest"] for item in candidates[1:]):
        raise ResolutionError("operator images have tied creation times; select an explicit tag")
    return newest[2], newest[3]


def chart_text(name, commit, read):
    """Fetch packaged defaults from the same upstream source as the operator."""
    result = github(f"contents/cli/inferops_cli/charts/{name}/values.yaml?ref={commit}", read)
    if result.get("encoding") != "base64" or not isinstance(result.get("content"), str):
        raise ResolutionError(f"{name} packaged chart defaults are unavailable")
    try:
        return base64.b64decode(result["content"], validate=False).decode("utf-8")
    except (ValueError, UnicodeError):
        raise ResolutionError(f"{name} packaged chart defaults are invalid") from None


def chart_scalar(text, path):
    """Read a simple mapping scalar; do not interpret YAML tags or aliases."""
    parents = []
    found = []
    for line in text.splitlines():
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        indent = len(line) - len(line.lstrip(" "))
        while parents and parents[-1][0] >= indent:
            parents.pop()
        match = re.fullmatch(r" *([A-Za-z0-9_.-]+):(?:\s+(.*))?", line.rstrip())
        if not match:
            continue
        key, value = match.groups()
        current = tuple(item[1] for item in parents) + (key,)
        if value is None or value.startswith("#"):
            parents.append((indent, key))
        elif current == path:
            # These image fields contain literal quoted/plain scalars in the
            # packaged charts. More complex YAML requires explicit resolution.
            match = re.fullmatch(r'''(?:"([A-Za-z0-9_./:@+-]+)"|'([A-Za-z0-9_./:@+-]+)'|([A-Za-z0-9_./:@+-]+))(?:\s+#.*)?''', value)
            if not match:
                raise ResolutionError("unsupported packaged chart image value: " + ".".join(path))
            found.append(next(part for part in match.groups() if part is not None))
    if len(found) != 1:
        raise ResolutionError("missing or ambiguous packaged chart image value: " + ".".join(path))
    return found[0]


def companion_images(commit, runtime, read):
    operator = chart_text("inferops-operator", commit, read)
    gateway = chart_text("inferops-gateway", commit, read)
    images = [("inferops-operator", chart_scalar(operator, ("image", "tag")))]
    for text, prefix, repository in [(operator, ("image",), "inferops-operator"),
                                      (gateway, ("image",), "inferops-gateway"),
                                      (operator, ("modelRuntimes", runtime, "image"), runtime)]:
        if chart_scalar(text, prefix + ("repository",)) != f"ghcr.io/brassinai/{repository}":
            raise ResolutionError(f"{repository} packaged chart uses a different repository; resolve its image explicitly")
        if repository != "inferops-operator":
            images.append((repository, chart_scalar(text, prefix + ("tag",))))
    downloader = chart_scalar(operator, ("cache", "downloaderImage"))
    prefix = "ghcr.io/brassinai/model-downloader:"
    if not downloader.startswith(prefix):
        raise ResolutionError("packaged chart downloader repository requires explicit resolution")
    images.append(("model-downloader", downloader[len(prefix):]))
    if any(not IMAGE_TAG.fullmatch(tag) for _, tag in images):
        raise ResolutionError("invalid packaged chart image tag")
    return images


def resolve(platform, runtime, tag=None, source_ref=None, read=read_json, *, channel="operator"):
    if not re.fullmatch(r"linux/[a-z0-9_]+(?:/v[0-9]+)?", platform):
        raise ResolutionError("use the target node's OCI platform, for example linux/amd64")
    if runtime not in ("llama-cpp", "nano-vllm", "vllm", "sglang"):
        raise ResolutionError("unsupported runtime selection")
    if source_ref and tag is None:
        raise ResolutionError("source-ref requires an explicit image tag")
    if channel not in ("operator", "stable"):
        raise ResolutionError("unsupported installation channel")
    source = "explicit_tag"
    operator = None
    if tag is None:
        if channel == "stable":
            tag, source = latest_tag(read)
        else:
            tag, operator = latest_operator(read)
            source = "registry_latest_operator"
    if not isinstance(tag, str) or not IMAGE_TAG.fullmatch(tag):
        raise ResolutionError("resolved tag is not a valid image tag")
    revision = operator.get("source_revision") if operator else None
    if revision and not re.fullmatch(r"[0-9a-fA-F]{7,40}", revision):
        raise ResolutionError("operator image has an invalid source revision")
    if source_ref:
        ref = source_ref
    elif revision:
        ref = revision
    elif re.fullmatch(r"[0-9a-fA-F]{7,40}", tag):
        ref = tag
    else:
        ref = "refs/tags/" + tag
        if operator and not STABLE_TAG.fullmatch(tag):
            raise ResolutionError("newest operator image has no source revision; select its tag with a verified source-ref")
    if not re.fullmatch(r"[A-Za-z0-9_./-]+", ref) or ref.startswith("-") or ".." in ref:
        raise ResolutionError("invalid source ref")
    try:
        commit = github("commits/" + urllib.parse.quote(ref, safe=""), read)
    except urllib.error.HTTPError as exc:
        raise ResolutionError(f"operator:{tag} source ref {ref} metadata HTTP {exc.code}; verify the matching upstream source") from None
    sha = commit.get("sha")
    if not isinstance(sha, str) or not re.fullmatch(r"[0-9a-fA-F]{40}", sha):
        raise ResolutionError("could not resolve the selected source ref to a commit")
    if revision and not sha.lower().startswith(revision.lower()):
        raise ResolutionError("operator source revision does not match the resolved commit")
    if not source_ref and not revision and re.fullmatch(r"[0-9a-fA-F]{7,40}", tag) and not sha.lower().startswith(tag.lower()):
        raise ResolutionError("operator commit tag does not match the resolved commit")
    if channel == "operator":
        companions = companion_images(sha, runtime, read)
        wanted = list(dict.fromkeys([("inferops-operator", tag), *companions]))
    else:
        companions = None
        wanted = [(repository, tag) for repository in ("inferops-operator", "inferops-gateway", "model-downloader", runtime)]
    images = [image_manifest(repository, image_tag, platform, read) for repository, image_tag in wanted]
    if operator and images[0]["manifest_digest"] != operator["manifest_digest"]:
        raise ResolutionError("operator tag changed during discovery; resolve again before installation")
    return {
        "image_tag": tag, "selection": source, "source_ref": ref, "source_commit": sha,
        "operator_created": operator.get("created") if operator else None,
        "chart_default_images": {repository: f"ghcr.io/brassinai/{repository}:{image_tag}"
                                 for repository, image_tag in companions} if companions else None,
        "operator_upgrade_required": bool(companions and dict(companions)["inferops-operator"] != tag),
        "sdk_spec": f"inferops @ git+{GIT}@{sha}#subdirectory=sdk/python",
        "cli_spec": f"inferops-cli @ git+{GIT}@{sha}#subdirectory=cli",
        "verified_images": images,
        "note": "metadata-only verification for the selected node/runtime; unused registered runtimes are not verified; manifests do not prove a Pod can pull or run",
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--platform", required=True)
    parser.add_argument("--runtime", required=True, choices=("llama-cpp", "nano-vllm", "vllm", "sglang"))
    parser.add_argument("--channel", choices=("operator", "stable"), default="operator",
                        help="newest published operator image (default), or published stable release")
    parser.add_argument("--tag", help="explicit image tag; overrides channel discovery")
    parser.add_argument("--source-ref", help="verified source commit/ref for an explicitly selected image tag")
    args = parser.parse_args()
    try:
        report = resolve(args.platform, args.runtime, args.tag, args.source_ref, channel=args.channel)
    except urllib.error.HTTPError as exc:
        print(json.dumps({"error": f"release/image metadata HTTP {exc.code}; check publication, access, and selected ref"}))
        return 1
    except ResolutionError as exc:
        print(json.dumps({"error": str(exc)}))
        return 1
    except (OSError, ValueError, TypeError, AttributeError, KeyError, urllib.error.URLError) as exc:
        print(json.dumps({"error": "release/image metadata unavailable or invalid", "kind": type(exc).__name__}))
        return 1
    print(json.dumps(report, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
