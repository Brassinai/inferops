GO ?= go
GOFMT ?= gofmt
HELM ?= helm
DOCKER ?= docker
IMAGE_TAG ?= dev

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

.PHONY: help setup tools-check fmt fmt-check test vet python-check python-test python-package runtime-conformance runtime-conformance-matrix \
	operator-image gateway-image control-plane-images model-downloader-build model-downloader-test helm-lint helm-template yaml-check schema-check verify

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
		'  runtime-conformance Validate a running runtime; requires RUNTIME_BASE_URL and RUNTIME_MODEL' \
		'  runtime-conformance-matrix Validate live vLLM and SGLang release candidates' \
		'  operator-image Build the operator container image' \
		'  gateway-image  Build the gateway container image' \
		'  control-plane-images Build operator and gateway container images' \
		'  model-downloader-build Build the cache downloader container image' \
		'  model-downloader-test  Run cache downloader unit tests' \
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
	@$(PYTHON) -c "import kubernetes" >/dev/null 2>&1 || { echo "error: Kubernetes Python client is missing; run: make setup"; exit 1; }
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

runtime-conformance:
	@test -n "$(RUNTIME_BASE_URL)" || { echo "error: RUNTIME_BASE_URL is required"; exit 1; }
	@test -n "$(RUNTIME_MODEL)" || { echo "error: RUNTIME_MODEL is required"; exit 1; }
	$(PYTHON) scripts/runtime_conformance.py \
		--base-url "$(RUNTIME_BASE_URL)" \
		--model "$(RUNTIME_MODEL)" \
		$(RUNTIME_CONFORMANCE_ARGS)

runtime-conformance-matrix:
	@test -n "$(VLLM_BASE_URL)" || { echo "error: VLLM_BASE_URL is required"; exit 1; }
	@test -n "$(VLLM_MODEL)" || { echo "error: VLLM_MODEL is required"; exit 1; }
	@test -n "$(SGLANG_BASE_URL)" || { echo "error: SGLANG_BASE_URL is required"; exit 1; }
	@test -n "$(SGLANG_MODEL)" || { echo "error: SGLANG_MODEL is required"; exit 1; }
	$(PYTHON) scripts/runtime_conformance.py \
		--base-url "$(VLLM_BASE_URL)" \
		--model "$(VLLM_MODEL)"
	$(PYTHON) scripts/runtime_conformance.py \
		--base-url "$(SGLANG_BASE_URL)" \
		--model "$(SGLANG_MODEL)" \
		--readiness-path /health_generate

operator-image:
	$(DOCKER) build \
		--file operator/Dockerfile \
		--tag inferops-operator:$(IMAGE_TAG) \
		.

gateway-image:
	$(DOCKER) build \
		--file gateway/Dockerfile \
		--tag inferops-gateway:$(IMAGE_TAG) \
		.

control-plane-images: operator-image gateway-image

model-downloader-build:
	$(DOCKER) build \
		--file runtimes/model-downloader/Dockerfile \
		--tag inferops/model-downloader:$(IMAGE_TAG) \
		.

model-downloader-test:
	PYTHONPATH=sdk/python:cli $(PYTHON) -m unittest tests.python.test_model_downloader

helm-lint:
	@for crd in deploy/manifests/crds/*.yaml; do \
		chart_crd="deploy/helm/inferops-operator/crds/$$(basename "$$crd")"; \
		cmp -s "$$crd" "$$chart_crd" || { \
			echo "error: Helm CRD is out of sync: $$chart_crd"; \
			exit 1; \
		}; \
	done
	@for chart in inferops-operator inferops-gateway inferops-dashboard; do \
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
		--set-string cache.root=relative/path >/dev/null 2>&1; then \
		echo "error: operator chart accepted an unsafe cache root"; \
		exit 1; \
	fi
	@if $(HELM) template invalid deploy/helm/inferops-operator \
		--set-string diagnostics.cacheProbeImage=busybox:latest >/dev/null 2>&1; then \
		echo "error: operator chart accepted an unpinned diagnostics image"; \
		exit 1; \
	fi
	@if $(HELM) template invalid deploy/helm/inferops-operator \
		--set-string webhook.certDir=relative/path >/dev/null 2>&1; then \
		echo "error: operator chart accepted an invalid webhook certificate directory"; \
		exit 1; \
	fi
	@if $(HELM) template invalid deploy/helm/inferops-operator \
		--set podDisruptionBudget.minAvailable=2 >/dev/null 2>&1; then \
		echo "error: operator chart accepted an impossible PodDisruptionBudget"; \
		exit 1; \
	fi
	@if $(HELM) template invalid deploy/helm/inferops-gateway \
		--set auth.enabled=true >/dev/null 2>&1; then \
		echo "error: gateway chart accepted auth without a Secret name"; \
		exit 1; \
	fi
	@if $(HELM) template invalid deploy/helm/inferops-gateway \
		--set ingress.enabled=true --set-string ingress.path=/inferops >/dev/null 2>&1; then \
		echo "error: gateway chart accepted an ingress path that changes model routes"; \
		exit 1; \
	fi
	@if $(HELM) template invalid deploy/helm/inferops-gateway \
		--set ingress.enabled=true --set-string ingress.pathType=Exact >/dev/null 2>&1; then \
		echo "error: gateway chart accepted an ingress path type that drops model routes"; \
		exit 1; \
	fi
	@if $(HELM) template invalid deploy/helm/inferops-gateway \
		--set gatewayAPI.enabled=true >/dev/null 2>&1; then \
		echo "error: gateway chart accepted Gateway API without a parent Gateway"; \
		exit 1; \
	fi
	@if $(HELM) template invalid deploy/helm/inferops-gateway \
		--set-string service.loadBalancerClass=example.com/internal >/dev/null 2>&1; then \
		echo "error: gateway chart accepted a load balancer class on ClusterIP"; \
		exit 1; \
	fi
	@if $(HELM) template invalid deploy/helm/inferops-gateway \
		--set service.port=0 >/dev/null 2>&1; then \
		echo "error: gateway chart accepted an invalid Service port"; \
		exit 1; \
	fi
	@if $(HELM) template invalid deploy/helm/inferops-gateway \
		--set topologySpread.enabled=true \
		--set topologySpread.maxSkew=0 >/dev/null 2>&1; then \
		echo "error: gateway chart accepted invalid topology spread"; \
		exit 1; \
	fi
	@if $(HELM) template invalid deploy/helm/inferops-dashboard \
		--set service.port=0 >/dev/null 2>&1; then \
		echo "error: dashboard chart accepted an invalid Service port"; \
		exit 1; \
	fi
	@if $(HELM) template invalid deploy/helm/inferops-dashboard \
		--set networkPolicy.apiServerCIDRs={} >/dev/null 2>&1; then \
		echo "error: dashboard chart accepted an empty Kubernetes API CIDR list"; \
		exit 1; \
	fi
	@$(HELM) template inferops-operator-ha deploy/helm/inferops-operator \
		--set replicaCount=2 >/dev/null

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
	@$(HELM) template inferops-operator-observability deploy/helm/inferops-operator \
		--set serviceMonitor.enabled=true \
		--set dashboards.enabled=true \
		> .verify/helm/inferops-operator-observability.yaml
	@$(HELM) template inferops-gateway-observability deploy/helm/inferops-gateway \
		--set serviceMonitor.enabled=true \
		> .verify/helm/inferops-gateway-observability.yaml
	@$(HELM) template inferops-runtime-observability deploy/helm/inferops-runtime \
		--set metrics.serviceMonitor.enabled=true \
		> .verify/helm/inferops-runtime-observability.yaml
	@$(HELM) template inferops-gateway-auth deploy/helm/inferops-gateway \
		--set auth.enabled=true \
		--set-string auth.secretName=inferops-gateway-token \
		> .verify/helm/inferops-gateway-auth.yaml
	@$(HELM) template inferops-gateway-nginx deploy/helm/inferops-gateway \
		--set ingress.enabled=true \
		--set-string ingress.className=nginx \
		--set-string ingress.hostname=models.example.com \
		--set auth.enabled=true \
		--set-string auth.secretName=inferops-gateway-token \
		> .verify/helm/inferops-gateway-nginx.yaml
	@$(HELM) template inferops-gateway-traefik deploy/helm/inferops-gateway \
		--set ingress.enabled=true \
		--set-string ingress.className=traefik \
		--set-string ingress.hostname=models.example.com \
		--set auth.enabled=true \
		--set-string auth.secretName=inferops-gateway-token \
		> .verify/helm/inferops-gateway-traefik.yaml
	@$(HELM) template inferops-gateway-api deploy/helm/inferops-gateway \
		--set gatewayAPI.enabled=true \
		--set-string 'gatewayAPI.parentRefs[0].name=public' \
		--set-string 'gatewayAPI.hostnames[0]=models.example.com' \
		--set auth.enabled=true \
		--set-string auth.secretName=inferops-gateway-token \
		> .verify/helm/inferops-gateway-api.yaml
	@$(HELM) template inferops-gateway-istio deploy/helm/inferops-gateway \
		--namespace inferops-system \
		--set gatewayAPI.enabled=true \
		--set-string 'gatewayAPI.parentRefs[0].name=public' \
		--set-string 'gatewayAPI.parentRefs[0].namespace=istio-ingress' \
		--set-string 'gatewayAPI.parentRefs[0].sectionName=http' \
		--set-string 'gatewayAPI.hostnames[0]=models.example.com' \
		--set auth.enabled=true \
		--set-string auth.secretName=inferops-gateway-token \
		> .verify/helm/inferops-gateway-istio.yaml
	@$(HELM) template inferops-gateway-loadbalancer deploy/helm/inferops-gateway \
		--set-string service.type=LoadBalancer \
		--set auth.enabled=true \
		--set-string auth.secretName=inferops-gateway-token \
		> .verify/helm/inferops-gateway-loadbalancer.yaml
	@$(HELM) template inferops-gateway-multinode deploy/helm/inferops-gateway \
		--values deploy/helm/inferops-gateway/values-multinode.yaml \
		> .verify/helm/inferops-gateway-multinode.yaml
	@$(HELM) template inferops-gateway-tenant deploy/helm/inferops-gateway \
		--namespace team-a \
		--set tenancy.access.enabled=true \
		--set-string 'tenancy.access.subjects[0].kind=Group' \
		--set-string 'tenancy.access.subjects[0].name=inference-team-a' \
		--set-string 'tenancy.access.subjects[0].apiGroup=rbac.authorization.k8s.io' \
		--set tenancy.quota.enabled=true \
		--set tenancy.limitRange.enabled=true \
		> .verify/helm/inferops-gateway-tenant.yaml
	@runtime_count=$$(grep -c '^kind: ModelRuntime$$' .verify/helm/inferops-operator.yaml); \
	[ "$$runtime_count" -eq 4 ] || { \
		echo "error: operator chart rendered $$runtime_count packaged runtimes, want 4"; \
		exit 1; \
	}
	@! grep -q '^kind: ServiceMonitor$$' .verify/helm/inferops-operator.yaml
	@! grep -q '^kind: ServiceMonitor$$' .verify/helm/inferops-gateway.yaml
	@! grep -q '^kind: ServiceMonitor$$' .verify/helm/inferops-runtime-cpu.yaml
	@grep -q '^kind: ServiceMonitor$$' .verify/helm/inferops-operator-observability.yaml
	@grep -q '^kind: ServiceMonitor$$' .verify/helm/inferops-gateway-observability.yaml
	@grep -q '^kind: ServiceMonitor$$' .verify/helm/inferops-runtime-observability.yaml
	@grep -q 'inferops-platform-dashboard.json' .verify/helm/inferops-operator-observability.yaml
	@grep -q 'inferops-vllm-dashboard.json' .verify/helm/inferops-operator-observability.yaml
	@grep -q 'inferops-llama-cpp-dashboard.json' .verify/helm/inferops-operator-observability.yaml
	@grep -q '^kind: Deployment$$' .verify/helm/inferops-dashboard.yaml
	@grep -q '^kind: NetworkPolicy$$' .verify/helm/inferops-dashboard.yaml
	@grep -q '^kind: ClusterRole$$' .verify/helm/inferops-dashboard.yaml
	@! grep -q -- '- secrets$$' .verify/helm/inferops-dashboard.yaml
	@$(PYTHON) scripts/check_operator_rbac.py .verify/helm/inferops-operator.yaml
	@$(PYTHON) scripts/check_tenant_rbac.py .verify/helm/inferops-gateway-tenant.yaml
	@grep -q 'pathType: Prefix' .verify/helm/inferops-gateway-homelab.yaml
	@grep -q 'profile: "homelab"' .verify/helm/inferops-operator-homelab.yaml
	@grep -q 'gpu.required: "false"' .verify/helm/inferops-operator-homelab.yaml
	@grep -q 'INFEROPS_CACHE_REQUIRED_RESOURCES' .verify/helm/inferops-operator-homelab.yaml
	@grep -q 'gpu.required: "false"' .verify/helm/inferops-operator.yaml
	@grep -q 'INFEROPS_GATEWAY_AUTH_TOKEN_FILE' .verify/helm/inferops-gateway-auth.yaml
	@grep -q 'secretName: "inferops-gateway-token"' .verify/helm/inferops-gateway-auth.yaml
	@$(PYTHON) scripts/check_gateway_exposure.py --mode tailscale \
		.verify/helm/inferops-gateway-homelab.yaml
	@$(PYTHON) scripts/check_gateway_exposure.py --mode ingress \
		--expected-class nginx --require-auth \
		.verify/helm/inferops-gateway-nginx.yaml
	@$(PYTHON) scripts/check_gateway_exposure.py --mode ingress \
		--expected-class traefik --require-auth \
		.verify/helm/inferops-gateway-traefik.yaml
	@$(PYTHON) scripts/check_gateway_exposure.py --mode gateway-api \
		--require-auth .verify/helm/inferops-gateway-api.yaml
	@$(PYTHON) scripts/check_gateway_exposure.py --mode gateway-api \
		--expected-parent-namespace istio-ingress \
		--require-auth \
		.verify/helm/inferops-gateway-istio.yaml
	@$(PYTHON) scripts/check_gateway_exposure.py --mode load-balancer \
		--require-auth .verify/helm/inferops-gateway-loadbalancer.yaml
	@$(PYTHON) scripts/check_gateway_exposure.py --mode multi-node \
		.verify/helm/inferops-gateway-multinode.yaml
	@grep -q '^kind: ValidatingWebhookConfiguration$$' .verify/helm/inferops-operator.yaml
	@test "$$(grep -c '^kind: PodDisruptionBudget$$' .verify/helm/inferops-operator.yaml)" -eq 1
	@test "$$(grep -c '^kind: PodDisruptionBudget$$' .verify/helm/inferops-gateway.yaml)" -eq 1
	@test "$$(grep -c '^kind: NetworkPolicy$$' .verify/helm/inferops-operator.yaml)" -eq 1
	@test "$$(grep -c '^kind: NetworkPolicy$$' .verify/helm/inferops-gateway.yaml)" -eq 2
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
	$(KUBECONFORM) -strict -summary -ignore-missing-schemas \
		-ignore-filename-pattern '(^|.*/)\.inferops/.*' \
		deploy/manifests/examples examples .verify/helm

verify: tools-check fmt-check test vet python-check python-test python-package helm-lint yaml-check schema-check
