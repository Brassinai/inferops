GO ?= go
PYTHON ?= python3
HELM ?= helm

.PHONY: help fmt test vet verify helm-lint helm-template python-check

help:
	@printf '%s\n' 'Available targets: fmt test vet helm-lint helm-template python-check verify'

fmt:
	$(GO) fmt ./...

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

helm-lint:
	@for chart in deploy/helm/*; do \
		if [ -f "$$chart/Chart.yaml" ]; then \
			$(HELM) lint "$$chart"; \
		fi; \
	done

helm-template:
	@for chart in deploy/helm/*; do \
		if [ -f "$$chart/Chart.yaml" ]; then \
			name=$$(basename "$$chart"); \
			$(HELM) template "$$name" "$$chart" >/dev/null; \
		fi; \
	done

python-check:
	$(PYTHON) -c "import ast,pathlib; roots=('sdk/python/inferops','cli/inferops_cli'); [ast.parse(path.read_text(), filename=str(path)) for root in roots for path in pathlib.Path(root).glob('*.py')]"

verify: fmt test vet helm-lint helm-template python-check
