# Resource and cache sizing

Measure the TARGET NODE, not a laptop that controls it. For kind or Docker
Desktop also measure the node-container backing filesystem and VM limits. WSL
measurements are constrained by its VM, not the Windows machine's advertised RAM.
Host inspection is not proof that a remote Kubernetes node has those resources.

## Storage

Measure df -Pk at the actual cache path (or its existing parent), existing cache
usage with du -sk, and other consumers on that filesystem, including container
images. Check quota and backing-volume limits if present. Do not create a
privileged diagnostic pod merely because a node cannot be accessed; use existing
authorized node access or report the missing measurement. InferOps doctor --check
cache creates temporary read-only hostPath probe Jobs; it needs Job permissions.

The install helper's explicit policy is to retain the greater of 10Gi or 15% of
the filesystem. It reserves twice a NEW model's full snapshot size for download
and staging. It adds that budget to existing cache usage; unrelated existing
artifacts do not count as free space. Round the declared capacity upward only
if it remains within safe available space. Without a selected model it proposes
at most 50Gi or half of safe additional space, with a minimum useful 1Gi budget.
These are conservative policy defaults, not measurements or enforced quotas.
Respect a user's tighter budget and never allocate more than actual free space.

The downloader fetches the entire Hugging Face repository snapshot. A repository
with several quantizations or both safetensors and bin weights costs more disk
than a single selected weight file. Missing file sizes make the disk assessment
unknown; do not silently substitute a model-card parameter count. A shared cache
can be reused only after repository, resolved revision, source, storage root,
namespace, and download credential reference match. Do not deduct it twice.

Before install choose --cache-node for one Ready schedulable node or explicit
--cache-node-capacity values for each node. Do not divide node-local requirements
across nodes or annotate every node with one measured node's capacity.

## Compute

Use kubectl describe node for allocatable resources and allocated requests, all
workloads on that node, quota, and scheduling restrictions. kubectl top is an
optional usage observation, not a substitute for requests. GPU extended-resource
counts are whole exclusive slots. Obtain per-device VRAM, utilization, compute
capability, and driver information on the node using nvidia-smi when available;
its absence is unknown VRAM, not zero GPUs. Check plugin and container runtime
support independently. No GPU slicing or implicit preemption.

Check the model architecture and quantization against the actual runtime image.
For llama-cpp require a supported GGUF. The packaged default auto-selection needs
one top-level GGUF; multiple files need a runtime definition configuring
MODEL_FILE, which is not an App.model keyword. CUDA runtimes require supported
NVIDIA hardware and compatible checkpoint/dtype. Apple Metal is not available
inside the packaged Linux CPU container.

Weight bytes alone are insufficient. Reserve RAM for loading and host work,
VRAM for runtime workspaces, and KV cache for the chosen context and concurrency.
For a standard decoder estimate KV bytes as 2 x layers x KV-heads x head-dimension
x bytes-per-KV-element x context-tokens x concurrent-sequences; derive dimensions
from verified config metadata. Attention variants, cache dtype, paging, and
runtime allocation strategies can change this. If unknown, report unknown and
use a bounded trial with conservative context only if the user accepts that
uncertainty. Do not manufacture model support, exact VRAM, or an optimal setting.

The deployment planner estimates RAM as weights plus 2Gi overhead (1.25x weights
for CPU) plus supplied KV, and GPU VRAM as 1.2x weights plus 2Gi plus KV, rounded
up. Pass free resources AFTER other reservations. These are cautious initial
estimates; they can overestimate or underestimate a particular engine. Multi-GPU
sharding needs explicit runtime support and per-GPU checks; this planner does
not certify it. Only a real readiness check and inference benchmark validate the
chosen configuration. Reduce context/concurrency before increasing memory use;
select a smaller supported model when the requested one cannot fit.
