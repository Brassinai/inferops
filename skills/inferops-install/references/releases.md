# Resolve the installation version

For a fresh installation, use the newest published stable InferOps version unless
the user explicitly selected another version or an existing deployment is pinned.
Resolve once and keep that tag and source commit fixed for the entire task.
Do not install chart defaults and then run a second upgrade just to become current.

The bundled resolver uses GitHub's latest non-draft, non-prerelease release. If
there is no published release, it enumerates up to 1,000 Git tags and selects the
highest stable numeric version (vMAJOR.MINOR.PATCH or MAJOR.MINOR.PATCH). This
fallback excludes prereleases, branches, latest, dev, and named development tags.
A truncated inventory is not enough to establish the newest version.
Fallback candidates are checked against their release metadata so that a numeric
tag marked draft/prerelease is also excluded; discovery stops if twenty such
publication checks still cannot establish an eligible version.

The helper resolves the selected source ref to a full commit and returns SDK and
CLI pip references at that same commit. This also obtains charts and CRDs from
the selected source, rather than pairing new images with an old installed CLI.
If matching approved InferOps-owned PyPI packages are used instead, verify that
they contain compatible charts and contracts for the selected image release.
Never infer their versions by stripping a v prefix; current package metadata can
use a different version scheme.

## Required image checks

The helper queries GHCR metadata for operator, gateway, downloader, and the
selected runtime, all at the chosen tag and target node platform. It verifies
OCI/Docker manifest-list platform entries or a single-image config, records
digests, and does not download image layers. CPU normally selects llama-cpp;
a validated NVIDIA setup normally starts with nano-vllm unless another packaged
runtime was requested. Pass the NODE's platform, not the agent laptop's platform.

An image check failure stops installation at the chosen version. Do not
silently downgrade, omit --tag, build from an arbitrary branch, or substitute
latest to make the command pass. Missing images may indicate incomplete release
publication, missing registry access, or an unsupported target architecture.
The helper verifies only the selected runtime: other ModelRuntime definitions
registered by install still need image checks before being activated.

Public metadata uses anonymous registry access. For an authorized private source,
GH_TOKEN or GITHUB_TOKEN is used only for api.github.com. Private image metadata
can use GHCR_USER and GHCR_TOKEN supplied through a secure environment. Those
credentials go only to ghcr.io's token endpoint; short-lived pull tokens go to
the registry. The helper never prints credentials, API error bodies, or signed
blob redirect URLs. Registry verification does not configure Kubernetes pull
credentials: provision authorized imagePullSecrets separately when needed and
verify that Pods can actually pull images.

## Explicit overrides and unavailable releases

An explicit --tag always takes precedence over automatic discovery. It normally
maps to a Git tag of the same name. For a named image tag such as cloud with no
matching Git tag, also provide an explicitly verified --source-ref containing its
corresponding source commit/ref. Do not assume that a development branch is the
source of an independently published image. Named mutable tags are a user choice,
not a stable version discovered by this helper; record the source commit and
verified digests and check deployed images.

If the repository has no stable release or version tag, report that publishing
blocker. The user may supply a published image tag plus its verified source ref;
do not invent a release. API errors, private-repository 404s, or rate limits also
need resolution rather than being treated as proof that chart defaults are current.

For heterogeneous clusters, repeat checks at the SAME explicit image tag for
each selected node/runtime platform pair before using those nodes. Preserve a
user's installed version and configuration unless updating it is within scope.
This fresh-install workflow does not implicitly authorize upgrading a live release.

Protocol references: [GitHub releases](https://docs.github.com/en/rest/releases/releases#get-the-latest-release),
[repository tags](https://docs.github.com/en/rest/repos/repos#list-repository-tags),
[registry authentication](https://distribution.github.io/distribution/spec/auth/token/),
[OCI image index](https://github.com/opencontainers/image-spec/blob/main/image-index.md).
