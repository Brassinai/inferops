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

Install the Python verification dependency:

```bash
python3 -m pip install --requirement requirements-dev.txt
```

Install kubeconform with Go:

```bash
go install github.com/yannh/kubeconform/cmd/kubeconform@v0.6.7
```

Ensure `$(go env GOPATH)/bin` is on `PATH`.

## Verification Commands

```bash
make fmt            # write Go formatting changes
make fmt-check      # check Go formatting without modifying files
make test           # run Go tests
make vet            # run Go vet
make python-check   # parse all Python source
make python-test    # run Python unit tests
make helm-lint      # lint every Helm chart
make helm-template  # render every Helm chart into .verify/helm
make yaml-check     # parse YAML and check required CRD contracts
make schema-check   # validate CRDs, manifests, examples, and rendered charts
make verify         # run every required CI check
```

`make verify` first checks required tool versions and fails with an actionable
message when a dependency is missing. Verification output under `.verify/` is
generated locally and ignored by Git.

`yaml-check` verifies that each required CRD has a served OpenAPI schema.
`schema-check` then strictly validates Kubernetes resources known to
kubeconform and skips custom-resource schemas that are not available locally.

## Required Pull Request Check

The workflow in `.github/workflows/verify.yml` runs `make verify` for every pull
request and push to `main`. Repository administrators must configure the
`Required verification` check as required in the `main` branch ruleset and
disable bypasses for normal contributors. GitHub branch protection is an
external repository setting and cannot be enforced by a workflow file alone.
