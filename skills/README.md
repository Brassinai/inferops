# InferOps agent skills

Ten portable Agent Skills for operating InferOps through its existing CLI and
contracts. Each entrypoint links to a complete local runbook. Each skill can be
installed independently; required references and helpers are bundled inside it.
No InferOps source checkout, sibling skill, MCP server, or vendor-specific shell
substitution is required at runtime.

## Install

From an InferOps GitHub branch containing these skills:

~~~sh
npx skills add brassinai/inferops --skill inferops-install -g -a claude-code codex
~~~

Select other names below, or use --skill '*' to install the complete collection.
For local development from the InferOps repository:

~~~sh
npx skills add ./skills --skill '*' -a claude-code codex
~~~

The Skills installer needs its supported Node/npm environment; using the skill
itself does not require Node. Installation adds instructions and helpers, not a
running Kubernetes cluster. The agent performs the requested operations using
its available shell access and the user's authorization.

## Runtime requirements and limits

Host inspection, model planning, credential creation, and inference helpers use
Python 3.10+ standard library only. The operational workflows require the InferOps
CLI and SDK, kubectl, and Helm 3.15+ as applicable; the installation skill can
bootstrap these. A fresh install resolves the latest stable release/tag,
verifies the required image/platform metadata, pins the SDK/CLI source commit,
and supplies that image tag directly to install. Explicit version requests win.
No published version or missing artifacts is a blocker, not a chart-default
fallback. Helpers never claim that host measurements describe a remote node. Model plans distinguish estimates and missing data from a real benchmark.
They do not execute remote model code or certify unsupported hardware.

Installation runbooks cover Linux/k3s, WSL2 prerequisites, existing clusters,
and the local kind CPU path with persistent cache mounts. GPU passthrough and
runtime compatibility must be verified in the actual environment. These skills
have offline command, packaging, resource-planning, and helper tests; they have
not been used here to provision live WSL, GPU, or cloud environments. No live
cluster, model download, or provider connection is part of repository verification.

## Layout

Each skill contains SKILL.md, agents/openai.yaml, and references/ with the full
runbook and command-targeting conventions. scripts/ exists only when a tested
helper is needed. Supporting files use paths relative to the installed skill.
Codex presentation metadata is optional; the workflow instructions are portable.

## Workflows and authoring sources

The links to docs below are maintenance sources in this repository, not runtime
dependencies of installed skills. Check installed CLI help for release-dependent
flags. Keep capacity and cache decisions inside the corresponding workflows;
do not introduce extra skills for individual commands.

| Skill | Workflow | Existing sources |
| --- | --- | --- |
| [inferops-install](inferops-install/SKILL.md) | Diagnose the host and target nodes, create or reuse Kubernetes, size cache, install, and verify InferOps. | [Homelab](../docs/homelab.md), [quickstart](../docs/quickstart.md), [CLI](../docs/cli.md), [production](../docs/production.md) |
| [inferops-deploy](inferops-deploy/SKILL.md) | Assess model fit, deploy, activate, test inference, and benchmark. | [Quickstart](../docs/quickstart.md), [SDK](../docs/sdk.md), [CRDs](../docs/crds.md), [GPU scheduling](../docs/gpu-scheduling.md) |
| [inferops-status](inferops-status/SKILL.md) | Inspect cluster, model, cache, and connection state and report blockers without repairs. | [CLI](../docs/cli.md), [CRDs](../docs/crds.md), [architecture](../docs/architecture.md), [cloud connection](../docs/cloud-mesh-connect.md) |
| [inferops-status-fix](inferops-status-fix/SKILL.md) | Diagnose, perform scoped repairs, and verify recovery. | [CLI](../docs/cli.md), [homelab](../docs/homelab.md), [GPU scheduling](../docs/gpu-scheduling.md), [disaster recovery](../docs/disaster-recovery.md) |
| [inferops-observability](inferops-observability/SKILL.md) | Set up monitoring, provide access, and guide metric inspection. | [Observability](../docs/observability.md), [CLI](../docs/cli.md), [production](../docs/production.md) |
| [inferops-gateway](inferops-gateway/SKILL.md) | Forward the gateway and demonstrate model requests. | [CLI](../docs/cli.md), [quickstart](../docs/quickstart.md), [routing](../docs/routing.md), [ingress](../docs/cluster-ingress.md) |
| [inferops-cloudconnect](inferops-cloudconnect/SKILL.md) | Connect using a provider credential, benchmark offerings, and resolve connection blockers. | [Cloud connection](../docs/cloud-mesh-connect.md), [CLI](../docs/cli.md), [cloud command implementation](../cli/inferops_cli/cloud.py) |
| [inferops-deactivate](inferops-deactivate/SKILL.md) | Drain and stop a model while keeping its deployment and cache. | [CLI](../docs/cli.md), [CRDs](../docs/crds.md), [GPU scheduling](../docs/gpu-scheduling.md) |
| [inferops-switchdeploy](inferops-switchdeploy/SKILL.md) | Check the replacement, deactivate the current model, deploy or activate the replacement, and verify or recover. | [CLI](../docs/cli.md), [quickstart](../docs/quickstart.md), [GPU scheduling](../docs/gpu-scheduling.md) |
| [inferops-uninstall](inferops-uninstall/SKILL.md) | Remove InferOps with explicit retention and cleanup scope. | [CLI](../docs/cli.md), [disaster recovery](../docs/disaster-recovery.md), [uninstall implementation](../cli/inferops_cli/uninstall.py) |


## Validation and maintenance

Run make skills-check to validate metadata, standalone resource links, bundled
Python syntax, and every InferOps command in the runbooks against the real CLI
parser. Run make verify for the complete repository checks, including helper
behavior tests. Tests use synthetic model metadata and a local HTTP server;
no cluster operations or weight downloads are required.

Shared targeting/capacity references and the deployment/benchmark helpers are
intentionally copied into dependent bundles to preserve independent installation.
Update the canonical source and its bundled copies together; skills-check rejects
content drift. Keep operational mechanics in the CLI and add scripts only for
reliable inspection, planning, credential input, or verification that the CLI
does not already provide.
