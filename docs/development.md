# Development

`make verify` runs the same checks as CI. It needs no cluster, GPU, or network
service.

## Required tools

| Tool | Minimum |
| --- | --- |
| Go | 1.22 |
| Python | 3.10 |
| Helm | 3.15.4 |
| kubeconform | 0.6.7 |

## Setup

```bash
make setup   # installs pinned Python deps and kubeconform under .verify/
make verify  # runs all checks
```

## Focused commands

```bash
make fmt            # format Go
make fmt-check      # check Go formatting
make test           # Go tests
make vet            # go vet
make python-check   # parse Python
make python-test    # Python unit tests
make python-package # build CLI package
make helm-lint      # lint Helm charts
make helm-template  # render Helm charts
make yaml-check     # parse YAML and check CRD contracts
make schema-check   # validate CRDs, manifests, examples
```

No virtualenv activation or `PATH` changes needed. Override defaults with
`SYSTEM_PYTHON`, `PYTHON`, `GO`, `VENV`, `TOOLS_BIN`, or `KUBECONFORM`.

## Pull request requirement

CI runs `make verify` on every PR and push to `main`. Repository administrators
must set the `Required verification` check as required in branch protection.
