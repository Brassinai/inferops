#!/usr/bin/env python3
"""Resolve an InferOps version and verify its required GHCR image manifests."""

from __future__ import annotations

import argparse
import base64
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


def image_manifest(repository, tag, platform, read):
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
    headers = {"Authorization": "Bearer " + bearer, "Accept": ACCEPT}
    root = f"https://ghcr.io/v2/brassinai/{repository}"
    manifest, digest = read(root + "/manifests/" + urllib.parse.quote(tag, safe=""), headers)
    if not isinstance(manifest, dict):
        raise ResolutionError("invalid image manifest")
    if "manifests" in manifest:
        matches = [entry for entry in manifest["manifests"]
                   if platform_matches(entry.get("platform", {}), platform)]
        if not matches:
            raise ResolutionError(f"{repository}:{tag} has no image for {platform}")
        selected = matches[0].get("digest", "")
        if not DIGEST.fullmatch(selected):
            raise ResolutionError("invalid platform manifest digest")
        child, _ = read(root + "/manifests/" + selected, headers, expected_digest=selected)
        if not isinstance(child, dict) or not isinstance(child.get("config"), dict):
            raise ResolutionError("platform image manifest is incomplete")
    else:
        config_digest = manifest.get("config", {}).get("digest", "")
        if not DIGEST.fullmatch(config_digest):
            raise ResolutionError("single-platform image has no valid config digest")
        config, _ = read(root + "/blobs/" + config_digest, headers,
                         expected_digest=config_digest, blob_redirect=True)
        if not platform_matches(config, platform):
            raise ResolutionError(f"{repository}:{tag} does not target {platform}")
        selected = digest
    return {
        "image": f"ghcr.io/brassinai/{repository}:{tag}",
        "manifest_digest": digest,
        "platform_digest": selected,
        "platform": platform,
    }


def resolve(platform, runtime, tag=None, source_ref=None, read=read_json):
    if not re.fullmatch(r"linux/[a-z0-9_]+(?:/v[0-9]+)?", platform):
        raise ResolutionError("use the target node's OCI platform, for example linux/amd64")
    if runtime not in ("llama-cpp", "nano-vllm", "vllm", "sglang"):
        raise ResolutionError("unsupported runtime selection")
    if source_ref and tag is None:
        raise ResolutionError("source-ref requires an explicit image tag")
    source = "explicit_tag"
    if tag is None:
        tag, source = latest_tag(read)
    if not isinstance(tag, str) or not IMAGE_TAG.fullmatch(tag):
        raise ResolutionError("resolved tag is not a valid image tag")
    if tag in ("latest", "dev", "main") and source != "explicit_tag":
        raise ResolutionError("automatic discovery must select a concrete stable version")
    ref = source_ref or "refs/tags/" + tag
    if not re.fullmatch(r"[A-Za-z0-9_./-]+", ref) or ref.startswith("-") or ".." in ref:
        raise ResolutionError("invalid source ref")
    commit = github("commits/" + urllib.parse.quote(ref, safe=""), read)
    sha = commit.get("sha")
    if not isinstance(sha, str) or not re.fullmatch(r"[0-9a-fA-F]{40}", sha):
        raise ResolutionError("could not resolve the selected source ref to a commit")
    images = [
        image_manifest(repository, tag, platform, read)
        for repository in ("inferops-operator", "inferops-gateway", "model-downloader", runtime)
    ]
    return {
        "image_tag": tag, "selection": source, "source_ref": ref, "source_commit": sha,
        "sdk_spec": f"inferops @ git+{GIT}@{sha}#subdirectory=sdk/python",
        "cli_spec": f"inferops-cli @ git+{GIT}@{sha}#subdirectory=cli",
        "verified_images": images,
        "note": "metadata-only verification for the selected node/runtime; unused registered runtimes are not verified; manifests do not prove a Pod can pull or run",
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--platform", required=True)
    parser.add_argument("--runtime", required=True, choices=("llama-cpp", "nano-vllm", "vllm", "sglang"))
    parser.add_argument("--tag", help="explicit image tag; otherwise discover latest stable")
    parser.add_argument("--source-ref", help="verified source commit/ref for an explicitly selected image tag")
    args = parser.parse_args()
    try:
        report = resolve(args.platform, args.runtime, args.tag, args.source_ref)
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
