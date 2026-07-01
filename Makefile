GO ?= go
GOFMT ?= gofmt
HELM ?= helm

GO_VERSION ?= 1.22
PYTHON_VERSION ?= 3.10
HELM_VERSION ?= 3.15.4
KUBECONFORM_VERSION ?= 0.6.7

TOOLS_BIN ?= .verify/bin
VENV ?= .verify/venv
SYSTEM_PYTHON ?= python3
LOCAL_KUBECONFORM := $(TOOLS_BIN)/kubeconform
LOCAL_PYTHON := $(VENV)/bin/python
KUBECONFORM ?= $(if $(wildcard $(LOCAL_KUBECONFORM)),$(LOCAL_KUBECONFORM),kubeconform)
PYTHON ?= $(if $(wildcard $(LOCAL_PYTHON)),$(LOCAL_PYTHON),$(SYSTEM_PYTHON))

GO_FILES := $(shell find . -type f -name '*.go' -not -path './vendor/*' -not -path './.git/*' | sort)
CHARTS := $(sort $(dir $(wildcard deploy/helm/*/Chart.yaml)))

.PHONY: help setup tools-check fmt fmt-check test vet python-check python-test python-package \
	helm-lint helm-template yaml-check schema-check verify

help:
	@printf '%s\n' \
		'Available targets:' \
		'  setup          Install pinned local verification dependencies' \
		'  tools-check    Verify required local tools are installed' \
		'  fmt            Format Go source files' \
		'  fmt-check      Fail when Go source files need formatting' \
		'  test           Run Go tests' \
		'  vet            Run Go vet' \
		'  python-check   Parse Python source files' \
		'  python-test    Run Python unit tests' \
		'  python-package Build the CLI source distribution and wheel' \
		'  helm-lint      Lint all Helm charts' \
		'  helm-template  Render all Helm charts' \
		'  yaml-check     Parse checked-in YAML files' \
		'  schema-check   Validate CRDs and manifests with kubeconform' \
		'  verify         Run the same required checks as CI'

setup:
	@command -v $(GO) >/dev/null || { echo "error: Go $(GO_VERSION)+ is required"; exit 1; }
	@command -v $(SYSTEM_PYTHON) >/dev/null || { echo "error: Python $(PYTHON_VERSION)+ is required"; exit 1; }
	@mkdir -p $(TOOLS_BIN)
	$(SYSTEM_PYTHON) -m venv $(VENV)
	$(LOCAL_PYTHON) -m pip install --requirement requirements-dev.txt
	GOBIN="$(abspath $(TOOLS_BIN))" $(GO) install github.com/yannh/kubeconform/cmd/kubeconform@v$(KUBECONFORM_VERSION)
	@echo "Installed verification dependencies. Run: make verify"

tools-check:
	@command -v $(GO) >/dev/null || { echo "error: Go $(GO_VERSION)+ is required"; exit 1; }
	@command -v $(GOFMT) >/dev/null || { echo "error: gofmt is required"; exit 1; }
	@command -v $(PYTHON) >/dev/null || { echo "error: Python $(PYTHON_VERSION)+ is required"; exit 1; }
	@command -v $(HELM) >/dev/null || { echo "error: Helm $(HELM_VERSION)+ is required"; exit 1; }
	@command -v $(KUBECONFORM) >/dev/null || { echo "error: kubeconform $(KUBECONFORM_VERSION) is required; run: make setup"; exit 1; }
	@$(PYTHON) -c "import yaml" >/dev/null 2>&1 || { echo "error: Python verification dependencies are missing; run: make setup"; exit 1; }
	@$(PYTHON) -c "import build" >/dev/null 2>&1 || { echo "error: Python build dependency is missing; run: make setup"; exit 1; }
	@$(PYTHON) -c "import hatchling" >/dev/null 2>&1 || { echo "error: Python build backend is missing; run: make setup"; exit 1; }
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

python-package:
	@rm -rf .verify/dist
	@mkdir -p .verify/dist
	$(PYTHON) -m build --no-isolation cli --outdir .verify/dist

helm-lint:
	@for crd in deploy/manifests/crds/*.yaml; do \
		chart_crd="deploy/helm/inferops-operator/crds/$$(basename "$$crd")"; \
		cmp -s "$$crd" "$$chart_crd" || { \
			echo "error: Helm CRD is out of sync: $$chart_crd"; \
			exit 1; \
		}; \
	done
	@for chart in inferops-operator inferops-gateway; do \
		diff -qr "deploy/helm/$$chart" "cli/inferops_cli/charts/$$chart" >/dev/null || { \
			echo "error: packaged Helm chart is out of sync: $$chart"; \
			exit 1; \
		}; \
	done
	@for chart in $(CHARTS); do \
		$(HELM) lint "$$chart"; \
	done
	@$(HELM) lint deploy/helm/inferops-runtime \
		--values deploy/helm/inferops-runtime/values-cpu.yaml
	@if $(HELM) template invalid deploy/helm/inferops-operator \
		--set replicaCount=2 >/dev/null 2>&1; then \
		echo "error: operator chart accepted multiple replicas without leader election"; \
		exit 1; \
	fi
	@if $(HELM) template invalid deploy/helm/inferops-operator \
		--set-string cache.root=relative/path >/dev/null 2>&1; then \
		echo "error: operator chart accepted an unsafe cache root"; \
		exit 1; \
	fi

helm-template:
	@rm -rf .verify/helm
	@mkdir -p .verify/helm
	@for chart in $(CHARTS); do \
		name=$$(basename "$$chart"); \
		$(HELM) template "$$name" "$$chart" > ".verify/helm/$$name.yaml"; \
	done
	@$(HELM) template inferops-runtime-cpu deploy/helm/inferops-runtime \
		--values deploy/helm/inferops-runtime/values-cpu.yaml \
		> .verify/helm/inferops-runtime-cpu.yaml
	@$(HELM) template inferops-operator-homelab deploy/helm/inferops-operator \
		--namespace inferops-system \
		--values deploy/helm/inferops-operator/values-homelab.yaml \
		> .verify/helm/inferops-operator-homelab.yaml
	@$(HELM) template inferops-gateway-homelab deploy/helm/inferops-gateway \
		--namespace inferops-system \
		--values deploy/helm/inferops-gateway/values-homelab.yaml \
		--set tailscale.enabled=true \
		--set-string tailscale.hostname=inferops \
		> .verify/helm/inferops-gateway-homelab.yaml
	@runtime_count=$$(grep -c '^kind: ModelRuntime$$' .verify/helm/inferops-operator.yaml); \
	[ "$$runtime_count" -eq 4 ] || { \
		echo "error: operator chart rendered $$runtime_count packaged runtimes, want 4"; \
		exit 1; \
	}
	@! grep -Eq '^[[:space:]]+- (pods|secrets|\*)$$' \
		.verify/helm/inferops-operator.yaml || { \
		echo "error: operator RBAC contains a forbidden broad or sensitive resource"; \
		exit 1; \
	}
	@grep -q 'pathType: Prefix' .verify/helm/inferops-gateway-homelab.yaml
	@! grep -q 'tailscale.com/hostname' .verify/helm/inferops-gateway-homelab.yaml || { \
		echo "error: Tailscale Ingress hostname must be configured through spec.tls.hosts"; \
		exit 1; \
	}
	@! grep -Eq 'nvidia.com/gpu|TENSOR_PARALLEL_SIZE|GPU_MEMORY_UTILIZATION' \
		.verify/helm/inferops-runtime-cpu.yaml || { \
		echo "error: CPU runtime chart contains GPU-only settings"; \
		exit 1; \
	}

yaml-check:
	$(PYTHON) scripts/check_yaml.py

schema-check: helm-template
	$(KUBECONFORM) -strict -summary -ignore-missing-schemas deploy/manifests/crds
	$(KUBECONFORM) -strict -summary -ignore-missing-schemas deploy/manifests/examples examples .verify/helm

verify: tools-check fmt-check test vet python-check python-test python-package helm-lint yaml-check schema-check
