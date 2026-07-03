# Development

`make verify` is the required local and CI verification entry point. It is
dependency-free with respect to Kubernetes infrastructure: it does not require
a cluster, GPU, device plugin, model registry credential, or network service.

## Required Tools

Install these tools before running verification:

| Tool | Minimum | CI version |
| --- | --- | --- |
| Go | 1.22 | latest `1.22.x` |
| Python | 3.10 | 3.11 |
| Helm | 3.15.4 | 3.15.4 |
| kubeconform | 0.6.7 | 0.6.7 |

After Go, Python, and Helm are installed, set up the pinned local verification
dependencies and run verification:

```bash
make setup
make verify
```

`make setup` creates an ignored virtual environment under `.verify/venv`,
installs the Python verification dependencies there, and installs the pinned
kubeconform binary under `.verify/bin`. The Makefile automatically uses those
local tools, so no virtual-environment activation or `PATH` export is required.
Set `SYSTEM_PYTHON`, `PYTHON`, `GO`, `VENV`, `TOOLS_BIN`, or `KUBECONFORM` to
override these defaults.

## Verification Commands

```bash
make setup          # install pinned local verification dependencies
make fmt            # write Go formatting changes
make fmt-check      # check Go formatting without modifying files
make test           # run Go tests
make vet            # run Go vet
make python-check   # parse all Python source
make python-test    # run Python unit tests
make python-package # build the CLI sdist and wheel, including packaged charts
make helm-lint      # lint every Helm chart
make helm-template  # render every Helm chart into .verify/helm
make yaml-check     # parse YAML and check required CRD contracts
make schema-check   # validate CRDs, manifests, examples, and rendered charts
make verify         # run every required CI check
```

`make verify` first checks required tool versions and fails with an actionable
message when a dependency is missing. Verification output under `.verify/` is
generated locally and ignored by Git.

`yaml-check` verifies that each required CRD has a served OpenAPI schema and
enforces the checked-in v1alpha1 compatibility contract: established fields
must retain their types, enum values cannot disappear, and additive fields
cannot become newly required. It also recursively compares current schemas
with `tests/fixtures/crds/pre-mvp508` to exercise the supported upgrade path.
`schema-check` then strictly validates Kubernetes resources known to
kubeconform and skips custom-resource schemas that are not available locally.
`helm-template` renders both the default GPU runtime chart and its CPU-only
profile, and fails if the CPU profile contains GPU resources or settings.

## Required Pull Request Check

The workflow in `.github/workflows/verify.yml` runs `make verify` for every pull
request and push to `main`. Repository administrators must configure the
`Required verification` check as required in the `main` branch ruleset and
disable bypasses for normal contributors. GitHub branch protection is an
external repository setting and cannot be enforced by a workflow file alone.
