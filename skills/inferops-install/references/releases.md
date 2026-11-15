# Resolve the installation version

## Default: newest published operator image

The installation skill owns SDK/CLI setup, Kubernetes prerequisites, persistent
cache preparation, and platform installation or upgrade. Its default version
source is the published `ghcr.io/brassinai/inferops-operator` image inventory.
A GitHub release or semantic Git tag is not a prerequisite for this path.
Use `resolve_release.py --platform "$TARGET_PLATFORM" --runtime "$RUNTIME"`.
The platform is the Linux Kubernetes NODE architecture, not the client host OS.

The helper reads the complete registry tag inventory up to 200 entries, examines
image-config creation times, and selects the newest image. Registry tag order
and lexical tag sorting do not establish recency. It prefers a commit/version
alias over a floating alias of the same digest. Unknown timestamps, tied times
for different digests, or an incomplete inventory require an explicit selection;
the helper never silently chooses an older image to bypass a failure.
An image build timestamp establishes newest build, not a stability guarantee.

Resolve that image's source through its OCI revision metadata, an unambiguous
commit-shaped tag, or a corresponding upstream version tag. Verify the resulting
full commit through the upstream repository. Do not map an arbitrary label such
as cloud to the current tip of a similarly named branch. If an image has no
source mapping, use an explicitly verified --source-ref with --tag. Report the
missing mapping instead of calling the whole problem a missing stable release.

The returned sdk_spec and cli_spec install BOTH packages from that upstream
commit, obtaining matching packaged charts and CRDs without requiring a local
source checkout. Use an isolated environment and pip install --upgrade. Check
that the selected CLI supports the install/cache/upgrade options needed by this
workflow; an old image's matching source may lack newer contracts. Do not pair
incompatible charts and images simply to bypass that limitation.

Resolve once and keep the operator tag, source commit, chart defaults, and
verified image digests fixed for the operation. The operator tag belongs to the
operator package; it is not a universal tag for every InferOps image.

For default operator-image selection, the helper reads the packaged operator and
gateway chart defaults at the resolved upstream source commit. It returns
chart_default_images and verifies those concrete gateway/downloader/runtime
versions, the chart's initial operator image, and the selected latest operator.
It does not look for inferops-gateway:<operator-commit-tag> unless that exact tag
is actually declared by the upstream gateway chart. Preserve the chart-declared
companion versions; do not infer their newest tag or downgrade them implicitly.

On a fresh cluster, install the verified packaged defaults using inferops install
without --tag. If operator_upgrade_required is true, then upgrade only the
operator to image_tag using inferops upgrade --component operator --tag. This is
a deliberate component upgrade, not a second install of the whole platform.
For an existing installation, upgrade only the operator while retaining its
companion versions and release values. Verify custom retained images separately.
An identical tag across every component is not a requirement for compatibility;
the selected source charts declare the installation defaults.
Installing the SDK/CLI alone does not install or upgrade the Kubernetes platform.

## Explicit tag or stable-only request

--tag selects the operator image tag and overrides automatic discovery on the
default operator channel; companion versions still come from its source charts.
For a named image tag with a different
source ref, also pass --source-ref with the verified matching upstream commit.
For example, an explicitly selected published cloud image can use its verified
source commit; do not assume that today's cloud branch tip produced that image.

Use the source choice from the current request. A later explicit container
package/tag selection supersedes an earlier stable-only selection.
Only when the user explicitly requests a stable release, use --channel stable.
That channel uses GitHub's latest non-draft, non-prerelease release, or the highest
stable numeric Git tag (vMAJOR.MINOR.PATCH or MAJOR.MINOR.PATCH) after publication
checks. Prereleases and named development tags do not satisfy a stable-only
request. No stable publication is a blocker for THAT channel, not for default
operator-image discovery. The stable channel verifies a uniform release tag
across the required packages before using install --tag.
Preserve an explicit version, digest, or stable-only choice rather than silently replacing it with a development build.

## Required image and source checks

Verify GHCR metadata for the selected operator, gateway, downloader, and selected
runtime at their resolved individual tags and target node platform. CPU normally
selects llama-cpp; validated NVIDIA normally starts with nano-vllm. Record the manifest
and platform digests. The helper reads metadata without downloading image layers.
The default operator-only upgrade leaves the dashboard unchanged. Verify its own
package/version separately if a dashboard upgrade is explicitly requested.

Missing companion images, unsupported target architecture, invalid provenance,
or inaccessible metadata remain specific blockers. Do not downgrade silently,
substitute unverified chart defaults, assume a locally cached image is current,
or build an arbitrary local checkout. Building and loading images from an upstream source
commit is a separate explicitly selected development source path. Do not label
it a published upgrade or push images to a registry as part of installation.

A tag's presence does not prove compatibility or stability. Recheck the recorded
digests before changing a cluster; mutable tags may move. Verify pulled imageIDs
and rollout readiness afterward. Existing runtime definitions or model workloads
must not be overwritten just to force every image onto one tag. Unused runtimes
registered by fresh installation remain unverified until their images are checked.
For heterogeneous nodes, repeat the same resolved per-component image checks
for each required node/runtime platform. Respect existing custom images and documented compatibility.

Source API errors, permission failures, and registry failures need specific fixes;
do not interpret them as proof that no release or no image exists. A no-longer-
available pinned image does not authorize resetting user state or losing cache.

## Credential boundaries

Public metadata uses anonymous access. GH_TOKEN or GITHUB_TOKEN goes only to
api.github.com. Private image lookup can use GHCR_USER and GHCR_TOKEN through a
secure environment; these go only to ghcr.io's token endpoint. Pull credentials
go only to the registry. Helpers never print credentials, remote error bodies,
or signed blob URLs. Metadata verification does not provision Kubernetes image
pull credentials: configure authorized imagePullSecrets separately if required.

Protocol references: [GitHub releases](https://docs.github.com/en/rest/releases/releases#get-the-latest-release),
[registry tags](https://distribution.github.io/distribution/spec/api/#listing-image-tags),
[registry authentication](https://distribution.github.io/distribution/spec/auth/token/),
[OCI image config](https://github.com/opencontainers/image-spec/blob/main/config.md).
