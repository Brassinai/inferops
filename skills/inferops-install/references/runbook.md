# Install or upgrade the complete InferOps environment

## Inspect before changing the host

Run the bundled inspector on the actual node when it is the execution host:

~~~sh
python3 "$SKILL_DIR/scripts/inspect_system.py" --cache-path /var/lib/inferops/models
kubectl config get-contexts -o name
~~~

A missing kubectl is expected on a fresh host; continue host discovery. Also
inspect /etc/os-release, PID 1 (ps -p 1 -o comm=), cgroup support, and any existing
container engine. For a remote cluster, use explicit node access to measure disk
and VRAM; the local inspector never claims to measure remote or container nodes.
Read [capacity sizing](capacity.md) and choose a cache node/path/budget. Rerun the
inspector with --artifact-bytes when a verified complete snapshot size is known.
A null recommendation or unknown disk measurement is a blocker to allocation.
Record the active Python environment and CLI source, existing Helm releases,
namespace, desired compute profile, and cache roots/annotations. An invocation to
install or bring InferOps current covers its client packages AND required cluster
components; do not stop after installing pip packages or ask again merely because
the platform is absent. Do not install a second Kubernetes cluster when a healthy
existing target is available. Preserve explicitly selected versions and budgets.

## Install client prerequisites

Require Kubernetes 1.28+, Python 3.10+, kubectl compatible with the cluster, and Helm 3.15+ from
the Helm 3 release line. Use the detected OS package manager for Python and an
isolated venv (or pipx), respecting externally managed Python environments.
For Debian/Ubuntu the prerequisite packages include python3, python3-venv,
ca-certificates, and curl. Check versions after installation. On other distros
use the official package instructions for that distro, not Debian commands.

## Resolve the current InferOps version

Once Python and host inspection are available, read [release selection](releases.md).
For a fresh install OR an update, discover the newest published operator image
and check its installation images BEFORE changing InferOps. Set TARGET_PLATFORM
from the actual node architecture (for example linux/amd64 or linux/arm64), and RUNTIME from the compute
plan (llama-cpp for CPU or a validated packaged GPU runtime). If no cluster exists
yet, establish its provisioner and planned node platform first, then verify the
actual node architecture and storage after creating it, before installing InferOps:

~~~sh
python3 "$SKILL_DIR/scripts/resolve_release.py" --platform "$TARGET_PLATFORM" --runtime "$RUNTIME"
~~~

Continue only on exit 0. Read image_tag into INFEROPS_TAG, source_commit into
INFEROPS_REF, sdk_spec into SDK_SPEC, and cli_spec into CLI_SPEC. Record
verified_images and their digests. Parse this JSON as data; do not eval it.
Keep those values and chart_default_images fixed, including packaged charts/CRDs.
On the default operator channel, INFEROPS_TAG is the operator's target tag; it is
not the gateway/downloader/runtime tag. The helper verifies companion versions
from that source's packaged charts. Record operator_upgrade_required as well.
The returned source package references require Git, so install it with the other
client prerequisites if that source installation path is used.

If the user specified a version, run the resolver with --tag "$REQUESTED_TAG"
instead. If the user explicitly requires a stable release, add --channel stable;
missing GitHub releases alone do not block default operator-image selection.
For an explicitly selected named image tag with a different source ref:

~~~sh
python3 "$SKILL_DIR/scripts/resolve_release.py" --platform "$TARGET_PLATFORM" --runtime "$RUNTIME" \
  --tag "$REQUESTED_TAG" --source-ref "$REQUESTED_SOURCE_REF"
~~~

An unavailable selected image, unsupported platform, inaccessible source, or
unknown image-to-source mapping is a specific blocker. Resolve that exact problem
without falling back to chart defaults. A request to bring the installation to
the latest operator authorizes its platform upgrade; retain existing values,
authentication, cache data, and workload configuration. An explicit version pin
wins unless the user asks to change it. Resolve missing prerequisites and proceed
with all ordinary setup steps already within the user's request.

## Install the selected InferOps client packages

Install BOTH Python packages in the selected environment; the CLI imports the
SDK even though its package dependency declaration currently omits it. Set
SDK_SPEC and CLI_SPEC to the resolver's commit-pinned references before running
the commands below. If using approved InferOps-owned PyPI distributions instead,
verify that both packages and their bundled charts match the selected release.
Keep INFEROPS_TAG fixed to the verified operator image tag and retain the
separately verified companion image versions.

~~~sh
python3 -m venv "$HOME/.local/share/inferops/venv"
"$HOME/.local/share/inferops/venv/bin/python" -m pip install --upgrade "$SDK_SPEC" "$CLI_SPEC"
~~~

Reuse an existing environment when suitable. If matching distributions are
unavailable, use the verified upstream repository at one approved INFEROPS_REF
(full commit or release tag), with Git installed as a prerequisite. For example,
SDK_SPEC="inferops @ git+https://github.com/brassinai/inferops.git@$INFEROPS_REF#subdirectory=sdk/python"
and CLI_SPEC="inferops-cli @ git+https://github.com/brassinai/inferops.git@$INFEROPS_REF#subdirectory=cli".
Use a local approved checkout only when the user explicitly selected local source;
the normal installation source is upstream. Do not invent a published version or use an unrelated package with the same name. Put
that venv's bin directory on PATH for this session, then verify inferops --help. Give
the user the persistent PATH instructions without rewriting unrelated shell config.
For Helm binaries choose the OS/architecture and an approved v3 release, verify
its published checksum, and install it to a user-writable bin directory. Follow
[Helm installation](https://helm.sh/docs/intro/install/); do not assume a package
manager's unversioned helm package is Helm 3.

## Create or reuse Kubernetes

| Environment | Procedure |
| --- | --- |
| Existing cluster | Reuse its explicit context; inspect Ready nodes, permissions, architecture, and storage. Do not install k3s over it. |
| Linux bare metal / Linux VM | Default to k3s after checking requirements, service manager, ports, and GPU prerequisites. |
| WSL | Confirm WSL2, systemd, effective VM memory/storage limits, and Linux filesystem cache path. Run k3s inside the distribution, not in PowerShell. |
| macOS or chosen local container cluster | Use kind with a working Docker/Podman provider and sufficient VM capacity; CPU llama-cpp is the portable path. |
| Native Windows without WSL2 | Establish WSL2 first or use an existing remote cluster. Do not run Linux installation commands directly on Windows. |

For new Linux k3s, download its installer to a temporary local file, review it,
and execute it with the selected supported release and private kubeconfig mode:

~~~sh
curl -fsSL https://get.k3s.io -o /tmp/inferops-k3s-install.sh
sudo env INSTALL_K3S_VERSION="$K3S_VERSION" sh /tmp/inferops-k3s-install.sh --write-kubeconfig-mode 600
~~~

Set K3S_VERSION to the chosen approved release; do not leave it empty. Use a
unique temporary path if the sample path already exists. On a fresh NVIDIA
node install the supported host driver and NVIDIA container runtime BEFORE k3s;
add --default-runtime nvidia to this install command for the documented homelab
path. On an existing node, preserve its runtime configuration and diagnose before
restarting its service. K3s detects installed alternative runtimes on startup.
Install a pinned compatible NVIDIA device plugin via its official Helm chart or
manifest and verify nvidia.com/gpu is allocatable; host nvidia-smi alone is not
proof that Pods can use the GPU. Do not enable sharing or time slicing.

Copy the new k3s kubeconfig into a private user file with mode 600, selecting it
explicitly rather than overwriting an existing kubeconfig. Never make it world
readable. Verify kubectl get nodes with that file and the intended context.

For WSL, inspect wsl.exe --list --verbose and PID 1. Merge [boot] systemd=true
into /etc/wsl.conf only when required, preserving its other settings. A WSL
restart interrupts the agent session; explain the restart and resume after the
user's environment returns rather than issuing wsl.exe --shutdown silently.
GPU support uses the Windows NVIDIA WSL driver; do not install a Linux display
or kernel driver inside WSL. Verify passthrough, container runtime, and actual
Kubernetes GPU allocation. If any layer is unavailable report it and offer CPU
or a remote node, without silently changing the user's model requirement.

For kind, create a persistent host cache directory first, then use a config:

~~~yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    extraMounts:
      - hostPath: /absolute/host/path/inferops-models
        containerPath: /var/lib/inferops/models
~~~

Replace hostPath with the measured path shared with the container engine. Run
kind create cluster --name inferops --config kind.yaml --wait 120s and select
its resulting context. Reuse an existing named cluster; do not recreate it to
change mounts. GPU passthrough requires a specifically validated kind/provider
setup; this CPU recipe does not establish GPU support. Verify permissions and
free space inside the node, not only the host directory or a Docker disk limit.

## Configure persistent disk cache and compute headroom

Separate storage cache from memory: --cache-capacity is persistent disk capacity,
not RAM or VRAM. Do not reuse a previous 2Gi example just because it was retained
in an annotation. Measure df/du at the real cache backing filesystem and compute
CACHE_CAPACITY using the storage policy in capacity.md. Respect a tighter explicit
user budget and existing reservations; do not shrink below retained artifacts or
active reservations. Report the measured numbers and the selected budget.

The inspector also reports total/available RAM and a memory_plan reserving at
least 2Gi or 20% of RAM for the OS, Kubernetes, and platform. On macOS it uses
sysctl/vm_stat and labels reclaimable RAM as an estimate. For a container cluster,
measure the Linux node/VM limits too and use the lower effective budget after
existing workload requests. Host RAM is not evidence of node allocatable RAM.
Measure GPU VRAM separately where applicable. A provisional compute budget is
not a certification that an arbitrary model will fit; model deployment sizes
weights, loading workspace, context/KV cache, and CPU/GPU requests later.

Create a new persistent cache directory/mount when absent rather than stopping
because an old path disappeared. Use a dedicated writable root on the measured
filesystem and permissions for non-root cache jobs. For macOS/kind verify both
the host mount and the node container see the same files; for an existing OrbStack
node use its supported persistent storage path and verify it survives restarts.
Do not recreate a cluster to change mounts or silently restart a shared VM.

## Install or upgrade and verify the platform

Prepare the chosen node cache root with permissions suitable for the configured
non-root cache jobs (the homelab guide uses owner 65532:65532, mode 0750). Do not
recursively chown existing caches. Set NODE, CACHE_PATH, CACHE_CAPACITY and
COMPUTE_PROFILE from inspection; CPU is cpu and validated NVIDIA is nvidia-gpu.

~~~sh
inferops install --context "$CTX" --namespace "$NS" --profile homelab --compute-profile "$COMPUTE_PROFILE" \
  --cache-path "$CACHE_PATH" --cache-node "$NODE" --cache-capacity "$CACHE_CAPACITY" \
  --output json
kubectl --context "$CTX" -n "$NS" rollout status deployment/inferops-operator --timeout=120s
kubectl --context "$CTX" -n "$NS" rollout status deployment/inferops-gateway --timeout=120s
kubectl --context "$CTX" -n "$NS" get pods,deployments,services
kubectl --context "$CTX" -n "$NS" get modelruntimes
inferops doctor --context "$CTX" --namespace "$NS" --output json
inferops gpu list --context "$CTX" --namespace "$NS" --output json
~~~

For a fresh default-channel install the command above intentionally omits --tag:
it installs the individually verified packaged chart defaults. Check those initial
rollouts, then, when operator_upgrade_required is true, apply only the operator
upgrade below and repeat its readiness check. Do not pass the operator commit tag
to inferops install --tag: that flag overrides gateway/downloader/runtime tags too.
For a stable-channel uniform release plan instead, pass --tag "$INFEROPS_TAG" to
the initial install only after verifying the whole set at that release tag.

For an EXISTING installation, use its actual namespace and selected upstream
SDK/CLI, verify inferops upgrade --help, and upgrade only the operator while
preserving companion versions and release values:

~~~sh
inferops upgrade --context "$CTX" --namespace "$NS" --component operator --tag "$INFEROPS_TAG" --output json
kubectl --context "$CTX" annotate node "$NODE" "inferops.dev/cache-capacity=$CACHE_CAPACITY" --overwrite
~~~

The operator-only command leaves the gateway and dashboard unchanged. Upgrade
other components only with their separately verified package versions when
requested. This upgrade
applies packaged CRDs and reuses existing release values; it does not update
running model images, downloader settings or custom ModelRuntime definitions.
Inspect compatibility with those retained images. Preserve them unless their
migration is explicitly part of the requested update, and report their versions
separately rather than claiming every component was upgraded.
If only part of the platform exists, inspect and install the missing releases
from the selected packaged charts while preserving existing values and secrets.
The operator-only upgrade must not depend on a gateway having the operator tag.
Cache-root migration is not an incidental part of an upgrade. Retain a usable existing root,
or plan the new persistent root before changing its chart setting.

The first install command and all rollout/doctor checks above apply to fresh
installations; rerun those verification checks after an upgrade too. The upgrade
command itself is not proof of rollout readiness.

InferOps install wraps packaged Helm charts and prepares cache-node annotations;
it needs node/Job permissions. It can upgrade an existing release, so inspect
existing releases and settings before rerunning it. For multi-node setups use
the default profile and explicit per-node capacities instead of copying homelab
assumptions. A no-GPU result is expected for CPU installs. Explain any optional
Tailscale check without treating it as required for local CPU inference.

Inspect the operator/gateway Pod container image references and the selected
ModelRuntime's spec.defaultImage without dumping full Pod specs or Helm values.
Verify the final operator uses INFEROPS_TAG; compare gateway/downloader/runtime
references with their own chart_default_images entries on a fresh install, or
with the verified retained versions on an upgrade;
where imageIDs are exposed, reconcile their manifest/platform digests with the
recorded verification results. A declared image tag alone does not prove a Pod
pulled it successfully. Registered unused runtimes remain unverified until checked.
A fresh installation must finish with the operator at the resolved target tag;
its initial verified chart-default install may require the scoped operator upgrade.
An existing installation is upgraded while retaining compatible companion settings.
Investigate any mismatch before reporting success.

Completion means Ready nodes, ready operator/gateway Pods at the selected image
versions, registered compatible ModelRuntimes, and a usable measured cache root.
Operator and gateway versions can differ; their tags are package-specific.
Return SDK/CLI versions, interpreter/PATH, upstream source commit, Kubernetes
context/version, namespace, prior and selected operator/gateway tags, observed
image verification, runtime definitions, compute/RAM/VRAM budgets, measured disk
reserve, cache node/path/capacity/persistence, and any remaining blockers.
Do not claim that a model was deployed by installation alone.

Sources: [K3s](https://docs.k3s.io/quick-start), [K3s runtimes](https://docs.k3s.io/advanced),
[kind](https://kind.sigs.k8s.io/docs/user/configuration/),
[WSL systemd](https://learn.microsoft.com/en-us/windows/wsl/systemd),
[NVIDIA WSL](https://docs.nvidia.com/cuda/wsl-user-guide/index.html),
[NVIDIA toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html).
