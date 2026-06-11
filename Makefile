GO ?= go
GOFMT ?= gofmt
PYTHON ?= python3
HELM ?= helm
KUBECONFORM ?= kubeconform

GO_VERSION ?= 1.22
PYTHON_VERSION ?= 3.10
HELM_VERSION ?= 3.15.4
KUBECONFORM_VERSION ?= 0.6.7

GO_FILES := $(shell find . -type f -name '*.go' -not -path './vendor/*' -not -path './.git/*' | sort)
CHARTS := $(sort $(dir $(wildcard deploy/helm/*/Chart.yaml)))

.PHONY: help tools-check fmt fmt-check test vet python-check python-test \
	helm-lint helm-template yaml-check schema-check verify

help:
	@printf '%s\n' \
		'Available targets:' \
		'  tools-check    Verify required local tools are installed' \
		'  fmt            Format Go source files' \
		'  fmt-check      Fail when Go source files need formatting' \
		'  test           Run Go tests' \
		'  vet            Run Go vet' \
		'  python-check   Parse Python source files' \
		'  python-test    Run Python unit tests' \
		'  helm-lint      Lint all Helm charts' \
		'  helm-template  Render all Helm charts' \
		'  yaml-check     Parse checked-in YAML files' \
		'  schema-check   Validate CRDs and manifests with kubeconform' \
		'  verify         Run the same required checks as CI'

tools-check:
	@command -v $(GO) >/dev/null || { echo "error: Go $(GO_VERSION)+ is required"; exit 1; }
	@command -v $(GOFMT) >/dev/null || { echo "error: gofmt is required"; exit 1; }
	@command -v $(PYTHON) >/dev/null || { echo "error: Python $(PYTHON_VERSION)+ is required"; exit 1; }
	@command -v $(HELM) >/dev/null || { echo "error: Helm $(HELM_VERSION)+ is required"; exit 1; }
	@command -v $(KUBECONFORM) >/dev/null || { echo "error: kubeconform $(KUBECONFORM_VERSION) is required"; exit 1; }
	@$(PYTHON) -c "import yaml" >/dev/null 2>&1 || { echo "error: Python verification dependencies are missing; run: $(PYTHON) -m pip install --requirement requirements-dev.txt"; exit 1; }
	@$(PYTHON) scripts/check_tool_versions.py \
		--go-command "$(GO)" --minimum-go "$(GO_VERSION)" \
		--python-command "$(PYTHON)" --minimum-python "$(PYTHON_VERSION)" \
		--helm-command "$(HELM)" --minimum-helm "$(HELM_VERSION)" \
		--kubeconform-command "$(KUBECONFORM)" --minimum-kubeconform "$(KUBECONFORM_VERSION)"

fmt:
	@$(GOFMT) -w $(GO_FILES)

fmt-check:
	@unformatted="$$($(GOFMT) -l $(GO_FILES))"; \
	if [ -n "$$unformatted" ]; then \
		echo "Go files need formatting:"; \
		echo "$$unformatted"; \
		echo "Run: make fmt"; \
		exit 1; \
	fi

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

python-check:
	$(PYTHON) scripts/check_python.py

python-test:
	PYTHONPATH=sdk/python:cli $(PYTHON) -m unittest discover -s tests/python -p 'test_*.py'

helm-lint:
	@for chart in $(CHARTS); do \
		$(HELM) lint "$$chart"; \
	done

helm-template:
	@rm -rf .verify/helm
	@mkdir -p .verify/helm
	@for chart in $(CHARTS); do \
		name=$$(basename "$$chart"); \
		$(HELM) template "$$name" "$$chart" > ".verify/helm/$$name.yaml"; \
	done

yaml-check:
	$(PYTHON) scripts/check_yaml.py

schema-check: helm-template
	$(KUBECONFORM) -strict -summary -ignore-missing-schemas deploy/manifests/crds
	$(KUBECONFORM) -strict -summary -ignore-missing-schemas deploy/manifests/examples examples .verify/helm

verify: tools-check fmt-check test vet python-check python-test helm-lint yaml-check schema-check
