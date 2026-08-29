package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/brassinai/inferops/internal/runtimecontract"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestParseBool(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    bool
		wantErr bool
	}{
		{name: "true", value: "true", want: true},
		{name: "false", value: "false", want: false},
		{name: "one", value: "1", want: true},
		{name: "zero", value: "0", want: false},
		{name: "invalid", value: "sometimes", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseBool(test.value)
			if test.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseBool returned error: %v", err)
			}
			if got != test.want {
				t.Fatalf("parseBool=%v, want %v", got, test.want)
			}
		})
	}
}

func TestMissingConfig(t *testing.T) {
	cfg := config{
		cloudAPIURL:       "https://cloud.example.test",
		providerAccountID: "provider",
		providerClusterID: "cluster",
		namespace:         "inferops-system",
		meshProvider:      "tailscale",
		meshHostname:      "inferops",
		meshEndpointURL:   "https://inferops",
		credentialFile:    "/var/run/secrets/inferops-cloud/agentCredential",
		gatewayTokenFile:  "/var/run/secrets/inferops-gateway-auth/token",
	}
	if missing := missingConfig(cfg); len(missing) != 0 {
		t.Fatalf("missingConfig returned %v, want none", missing)
	}
	cfg.credentialFile = ""
	missing := missingConfig(cfg)
	if len(missing) != 1 || missing[0] != "INFEROPS_CLOUD_AGENT_CREDENTIAL_FILE" {
		t.Fatalf("missingConfig returned %v, want credential file", missing)
	}
}

func TestValidateURL(t *testing.T) {
	if err := validateURL("https://cloud.example.test", true); err != nil {
		t.Fatalf("validateURL returned error for https URL: %v", err)
	}
	if err := validateURL("http://127.0.0.1:8080", true); err != nil {
		t.Fatalf("validateURL returned error for localhost http URL: %v", err)
	}
	if err := validateURL("http://host.docker.internal:8080", true); err != nil {
		t.Fatalf("validateURL returned error for Docker host http URL: %v", err)
	}
	if err := validateURL("http://cloud.example.test", true); err == nil {
		t.Fatal("expected remote cloud http URL to fail")
	}
	if err := validateURL("http://100.64.0.5:80", false); err != nil {
		t.Fatalf("validateURL returned error for private mesh http URL: %v", err)
	}
}

func TestHeartbeatTimeout(t *testing.T) {
	if got := heartbeatTimeout(5 * time.Second); got != 5*time.Second {
		t.Fatalf("heartbeatTimeout=%v, want 5s", got)
	}
	if got := heartbeatTimeout(time.Minute); got != 30*time.Second {
		t.Fatalf("heartbeatTimeout=%v, want 30s", got)
	}
}

func TestModelOfferings(t *testing.T) {
	items := []unstructured.Unstructured{
		{Object: map[string]any{
			"apiVersion": runtimecontract.ModelDeploymentAPIVersion,
			"kind":       runtimecontract.ModelDeploymentKind,
			"metadata": map[string]any{
				"name":            "z-model",
				"namespace":       "team-a",
				"resourceVersion": "should-not-drift",
			},
			"spec": map[string]any{
				"runtime": map[string]any{
					"image": "ghcr.io/inferops/runtime:v1.2.3",
				},
				"secrets": map[string]any{
					"huggingFaceTokenSecretName": "hf-secret",
				},
			},
			"status": map[string]any{
				"phase":        "Active",
				"endpoint":     "/models/z-model",
				"modelName":    "org/z-model",
				"runtime":      "llama.cpp",
				"endpointPath": "/v1",
				"metrics": map[string]any{
					"activeRequests": int64(9),
				},
				"gpu": map[string]any{
					"vendor":   "NVIDIA",
					"type":     "L4",
					"count":    int64(1),
					"memoryGB": int64(24),
				},
				"pricing": map[string]any{
					"unit":     "million_input_tokens",
					"priceUSD": "0.02",
				},
				"capacity": map[string]any{
					"maxConcurrentRequests":   int64(4),
					"tokensPerSecondEstimate": float64(42),
				},
				"rateLimits": map[string]any{
					"maxConcurrentRequests": int64(4),
					"maxRequestsPerMinute":  int64(120),
					"maxTokensPerMinute":    int64(4096),
				},
			},
		}},
		{Object: map[string]any{
			"metadata": map[string]any{"name": "a-model"},
			"status":   map[string]any{"phase": "Cached"},
		}},
		{Object: map[string]any{
			"metadata": map[string]any{"name": "cpu-smollm"},
			"spec": map[string]any{
				"model": map[string]any{
					"name": "cpu-smollm",
					"repo": "jc-builds/SmolLM2-135M-Instruct-Q4_K_M-GGUF",
				},
				"runtime": map[string]any{
					"ref": "llama-cpp",
				},
				"routing": map[string]any{
					"path":             "/models/cpu-smollm",
					"openAICompatible": true,
				},
			},
			"status": map[string]any{
				"phase":        "Active",
				"endpoint":     "/models/cpu-smollm/v1",
				"endpointPath": "/v1",
			},
		}},
		{Object: map[string]any{
			"metadata": map[string]any{"name": "status-model-map"},
			"spec": map[string]any{
				"runtime": map[string]any{
					"ref": "llama-cpp",
				},
				"routing": map[string]any{
					"path":             "/models/status-model-map",
					"openAICompatible": true,
				},
			},
			"status": map[string]any{
				"phase": "Active",
				"model": map[string]any{
					"loaded": true,
					"repo":   "org/status-model-map",
				},
			},
		}},
	}
	pods := map[string][]unstructured.Unstructured{
		"z-model": {
			{Object: map[string]any{
				"metadata": map[string]any{
					"name":      "z-model-runtime-abc",
					"namespace": "team-a",
					"labels": map[string]any{
						"inferops.dev/modeldeployment": "z-model",
					},
				},
				"spec": map[string]any{
					"nodeName":           "node-a",
					"serviceAccountName": "default",
					"containers": []any{
						map[string]any{
							"name":  "runtime",
							"image": "ghcr.io/inferops/runtime:v1.2.3",
							"env": []any{
								map[string]any{"name": "API_KEY", "value": "runtime-secret"},
							},
							"resources": map[string]any{
								"limits": map[string]any{"cpu": "2"},
							},
						},
					},
				},
				"status": map[string]any{
					"phase": "Running",
					"podIP": "10.42.0.12",
					"containerStatuses": []any{
						map[string]any{
							"name":         "runtime",
							"imageID":      "ghcr.io/inferops/runtime@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
							"ready":        true,
							"restartCount": int64(1),
						},
					},
				},
			}},
		},
	}
	offerings := modelOfferings(items, "team-a", "us-test-1", gpuSnapshot{Vendor: "unknown", Type: "unknown", Count: 1, MemoryGB: 1}, pods)
	if len(offerings) != 4 {
		t.Fatalf("got %d offerings, want 4", len(offerings))
	}
	if offerings[0].Name != "a-model" || offerings[1].Name != "cpu-smollm" || offerings[2].Name != "status-model-map" || offerings[3].Name != "z-model" {
		t.Fatalf("offerings not sorted by name: %#v", offerings)
	}
	if offerings[3].Endpoint != "/models/z-model" {
		t.Fatalf("endpoint=%q, want /models/z-model", offerings[3].Endpoint)
	}
	if offerings[3].Model != "org/z-model" || offerings[3].Runtime != "llama.cpp" {
		t.Fatalf("model/runtime not discovered: %#v", offerings[3])
	}
	if offerings[3].EndpointPath != "/v1" || offerings[3].CustomEndpointsPresent {
		t.Fatalf("endpoint eligibility not discovered: %#v", offerings[3])
	}
	if offerings[3].GPU.Type != "L4" || offerings[3].GPU.MemoryGB != 24 {
		t.Fatalf("GPU evidence not discovered: %#v", offerings[3].GPU)
	}
	if offerings[3].ObservedCapacity.MaxConcurrentRequests != 4 || offerings[3].ObservedCapacity.TokensPerSecondEstimate != 42 {
		t.Fatalf("capacity evidence not discovered: %#v", offerings[3].ObservedCapacity)
	}
	if offerings[3].Evidence.ImageMetadata["runtimeImage"] != "ghcr.io/inferops/runtime:v1.2.3" {
		t.Fatalf("image evidence not discovered: %#v", offerings[3].Evidence.ImageMetadata)
	}
	if !containsString(offerings[3].Evidence.ImageMetadata["podImageIDs"], "ghcr.io/inferops/runtime@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Fatalf("pod image ID evidence not discovered: %#v", offerings[3].Evidence.ImageMetadata)
	}
	if offerings[3].Evidence.PodMetadata["totalPods"] != 1 {
		t.Fatalf("pod metadata did not include matched runtime pod: %#v", offerings[3].Evidence.PodMetadata)
	}
	if offerings[3].Evidence.PodMetadata["bindingVerified"] != true {
		t.Fatalf("pod binding was not marked verified: %#v", offerings[3].Evidence.PodMetadata)
	}
	if strings.Contains(fmt.Sprint(offerings[3].Evidence.PodMetadata), "runtime-secret") {
		t.Fatalf("pod metadata includes sensitive values: %#v", offerings[3].Evidence.PodMetadata)
	}
	if !strings.Contains(offerings[3].Evidence.ManifestYAML, "kind: ModelDeployment") {
		t.Fatalf("manifest evidence missing kind: %s", offerings[3].Evidence.ManifestYAML)
	}
	if strings.Contains(offerings[3].Evidence.ManifestYAML, "resourceVersion") || strings.Contains(offerings[3].Evidence.ManifestYAML, "activeRequests") {
		t.Fatalf("manifest evidence includes volatile fields: %s", offerings[3].Evidence.ManifestYAML)
	}
	if strings.Contains(offerings[3].Evidence.ManifestYAML, "hf-secret") || strings.Contains(offerings[3].Evidence.ManifestYAML, "runtime-secret") {
		t.Fatalf("manifest evidence includes sensitive values: %s", offerings[3].Evidence.ManifestYAML)
	}
	if offerings[1].Model != "jc-builds/SmolLM2-135M-Instruct-Q4_K_M-GGUF" || offerings[1].Runtime != "llama-cpp" {
		t.Fatalf("yaml model/runtime not discovered: %#v", offerings[1])
	}
	if offerings[1].Endpoint != "/models/cpu-smollm/v1" || offerings[1].EndpointPath != "/v1" || offerings[1].CustomEndpointsPresent {
		t.Fatalf("yaml endpoint eligibility not discovered: %#v", offerings[1])
	}
	if offerings[2].Model != "org/status-model-map" {
		t.Fatalf("status model map was not unwrapped: %#v", offerings[2])
	}
}

func TestPodsByModelDeploymentRequiresInferOpsOwnership(t *testing.T) {
	trustedPod := unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{
			"name":      "cpu-smollm-runtime-trusted",
			"namespace": "team-a",
			"labels": map[string]any{
				runtimecontract.ModelDeploymentLabel: "cpu-smollm",
				runtimecontract.LabelManagedBy:       runtimecontract.ValueManagedBy,
				runtimecontract.LabelPartOf:          runtimecontract.ValuePartOf,
				runtimecontract.LabelComponent:       runtimecontract.ValueRuntimeComponent,
			},
			"ownerReferences": []any{
				map[string]any{
					"apiVersion": "apps/v1",
					"kind":       "ReplicaSet",
					"name":       "cpu-smollm-runtime-rs",
				},
			},
		},
	}}
	copiedLabelPod := unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{
			"name":      "cpu-smollm-runtime-copy",
			"namespace": "team-a",
			"labels": map[string]any{
				runtimecontract.ModelDeploymentLabel: "cpu-smollm",
				runtimecontract.LabelManagedBy:       runtimecontract.ValueManagedBy,
				runtimecontract.LabelPartOf:          runtimecontract.ValuePartOf,
				runtimecontract.LabelComponent:       runtimecontract.ValueRuntimeComponent,
			},
		},
	}}
	replicaSet := unstructured.Unstructured{}
	replicaSet.SetName("cpu-smollm-runtime-rs")
	replicaSet.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "cpu-smollm-runtime",
	}})
	deployment := unstructured.Unstructured{}
	deployment.SetName("cpu-smollm-runtime")
	deployment.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: runtimecontract.ModelDeploymentAPIVersion,
		Kind:       runtimecontract.ModelDeploymentKind,
		Name:       "cpu-smollm",
	}})

	grouped := podsByModelDeployment([]unstructured.Unstructured{copiedLabelPod, trustedPod}, []unstructured.Unstructured{replicaSet}, []unstructured.Unstructured{deployment})

	pods := grouped["cpu-smollm"]
	if len(pods) != 1 || pods[0].GetName() != "cpu-smollm-runtime-trusted" {
		t.Fatalf("trusted pod grouping = %#v", grouped)
	}
}

func TestModelDeploymentOwnerNameRejectsCyclicOwnerReferences(t *testing.T) {
	replicaSet := unstructured.Unstructured{}
	replicaSet.SetName("loop-rs")
	replicaSet.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: "apps/v1",
		Kind:       "ReplicaSet",
		Name:       "loop-rs",
	}})

	got := modelDeploymentOwnerName(
		[]metav1.OwnerReference{{
			APIVersion: "apps/v1",
			Kind:       "ReplicaSet",
			Name:       "loop-rs",
		}},
		map[string]unstructured.Unstructured{"loop-rs": replicaSet},
		nil,
		0,
		map[string]struct{}{},
	)
	if got != "" {
		t.Fatalf("cyclic owner reference resolved to %q", got)
	}
}

func TestModelOfferingAutomaticallyEstimatesCapacityAndTokenLimit(t *testing.T) {
	item := unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "qwen-7b"},
		"spec": map[string]any{
			"model": map[string]any{
				"repo": "Qwen/Qwen2.5-7B-Instruct-GPTQ-Int4",
			},
			"runtime": map[string]any{
				"ref":         "vllm",
				"dtype":       "int4",
				"maxModelLen": int64(2048),
			},
			"routing": map[string]any{
				"path":             "/models/qwen-7b",
				"openAICompatible": true,
			},
		},
		"status": map[string]any{
			"phase":        "Active",
			"endpoint":     "/models/qwen-7b/v1",
			"endpointPath": "/v1",
		},
	}}
	offering, ok := modelDeploymentOffering(item, "team-a", "us-test-1", gpuSnapshot{
		Vendor:   "NVIDIA",
		Type:     "L4",
		Count:    1,
		MemoryGB: 24,
	}, nil)
	if !ok {
		t.Fatal("expected offering")
	}
	if offering.ObservedCapacity.CapacitySource != "hardware-model-estimate" {
		t.Fatalf("capacity source = %q", offering.ObservedCapacity.CapacitySource)
	}
	if offering.ObservedCapacity.TokensPerSecondEstimate <= 1 {
		t.Fatalf("tokens/sec was not estimated: %#v", offering.ObservedCapacity)
	}
	if offering.ObservedCapacity.TokensPerMinuteEstimate != int(offering.ObservedCapacity.TokensPerSecondEstimate*60) {
		t.Fatalf("tokens/minute = %d, tokens/sec = %v", offering.ObservedCapacity.TokensPerMinuteEstimate, offering.ObservedCapacity.TokensPerSecondEstimate)
	}
	if offering.RateLimits.MaxTokensPerMinute != offering.ObservedCapacity.TokensPerMinuteEstimate {
		t.Fatalf("rate limit tokens/minute = %d, want %d", offering.RateLimits.MaxTokensPerMinute, offering.ObservedCapacity.TokensPerMinuteEstimate)
	}
	if offering.ObservedCapacity.MaxConcurrentRequests <= 1 {
		t.Fatalf("concurrency was not estimated: %#v", offering.ObservedCapacity)
	}
	if offering.Pricing.Unit != "million_input_tokens" || offering.Pricing.PriceUSD != "0.01" {
		t.Fatalf("pricing should remain provider/cloud contract defaults: %#v", offering.Pricing)
	}
}

func TestDeploymentRuntimeProbeEligibility(t *testing.T) {
	activeDefault := unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{"phase": "Active", "endpointPath": "/v1"},
		"spec":   map[string]any{"routing": map[string]any{"openAICompatible": true}},
	}}
	if !deploymentRuntimeProbeEligible(activeDefault) {
		t.Fatal("active default OpenAI runtime should be probe eligible")
	}
	modelPrefixedDefault := unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{"phase": "Active", "endpointPath": "/models/cpu-smollm/v1"},
		"spec":   map[string]any{"routing": map[string]any{"openAICompatible": true}},
	}}
	if !deploymentRuntimeProbeEligible(modelPrefixedDefault) {
		t.Fatal("active model-prefixed OpenAI runtime should be probe eligible")
	}
	inactive := unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{"phase": "Cached", "endpointPath": "/v1"},
	}}
	if deploymentRuntimeProbeEligible(inactive) {
		t.Fatal("inactive runtime should not be probe eligible")
	}
	customEndpoint := unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{"phase": "Active", "endpointPath": "/custom"},
	}}
	if deploymentRuntimeProbeEligible(customEndpoint) {
		t.Fatal("custom runtime endpoint should not be probe eligible")
	}
	customModelEndpoint := unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{"phase": "Active", "endpointPath": "/models/cpu-smollm/custom"},
	}}
	if deploymentRuntimeProbeEligible(customModelEndpoint) {
		t.Fatal("custom model-prefixed runtime endpoint should not be probe eligible")
	}
}

func TestModelOfferingKeepsModelPrefixedEndpointPath(t *testing.T) {
	item := unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "cpu-smollm"},
		"spec": map[string]any{
			"model":   map[string]any{"repo": "jc-builds/SmolLM2-135M-Instruct-Q4_K_M-GGUF"},
			"runtime": map[string]any{"ref": "llama-cpp", "maxModelLen": int64(512)},
			"routing": map[string]any{"openAICompatible": true},
		},
		"status": map[string]any{
			"phase":       "Active",
			"endpoint":    "/models/cpu-smollm/v1",
			"serviceName": "cpu-smollm-runtime",
		},
	}}
	if !deploymentRuntimeProbeEligible(item) {
		t.Fatal("model-prefixed default endpoint should be benchmark eligible")
	}
	offering, ok := modelDeploymentOfferingWithMetrics(item, "default", "local-dev", gpuSnapshot{}, nil, runtimeMetricSnapshot{
		InputTokensPerSecondEstimate:  26.91,
		OutputTokensPerSecondEstimate: 11.12,
		TokensPerSecondEstimate:       38.03,
		Source:                        "runtime-metrics-gauge",
	})
	if !ok {
		t.Fatal("expected offering")
	}
	if offering.EndpointPath != "/models/cpu-smollm/v1" || offering.CustomEndpointsPresent {
		t.Fatalf("offering endpoint should remain the public default path: %#v", offering)
	}
}

func TestRuntimeMetricsCountersComputeTokenRates(t *testing.T) {
	discovery := &kubernetesDiscovery{tokenSamples: map[string]tokenCounterSample{}}
	first := discovery.runtimeMetricsFromFamilies("team-a/qwen-runtime", time.Unix(100, 0), parseMetricFamilies(t, `
# TYPE vllm:prompt_tokens_total counter
vllm:prompt_tokens_total{model_name="qwen"} 50
# TYPE vllm:generation_tokens_total counter
vllm:generation_tokens_total{model_name="qwen"} 100
# TYPE vllm:num_requests_running gauge
vllm:num_requests_running{model_name="qwen"} 3
# TYPE vllm:num_requests_waiting gauge
vllm:num_requests_waiting{model_name="qwen"} 2
# TYPE vllm:kv_cache_usage_perc gauge
vllm:kv_cache_usage_perc{model_name="qwen"} 0.5
# TYPE vllm:prefix_cache_hits counter
vllm:prefix_cache_hits{model_name="qwen"} 10
# TYPE vllm:prefix_cache_queries counter
vllm:prefix_cache_queries{model_name="qwen"} 20
# TYPE vllm:prompt_tokens_cached_total counter
vllm:prompt_tokens_cached_total{model_name="qwen"} 100
`))
	if first.TokensPerSecondEstimate != 0 || first.ActiveRequests != 3 || first.QueueDepth != 2 || first.GPUMemoryPressurePercent != 50 || first.KVCacheUsagePercent != 50 {
		t.Fatalf("first metric sample = %#v", first)
	}
	if !first.PromptCacheSupported || first.PrefixCacheHitRatePercent != 50 {
		t.Fatalf("first cache metrics = %#v", first)
	}

	second := discovery.runtimeMetricsFromFamilies("team-a/qwen-runtime", time.Unix(110, 0), parseMetricFamilies(t, `
# TYPE vllm:prompt_tokens_total counter
vllm:prompt_tokens_total{model_name="qwen"} 80
# TYPE vllm:generation_tokens_total counter
vllm:generation_tokens_total{model_name="qwen"} 250
# TYPE vllm:num_requests_running gauge
vllm:num_requests_running{model_name="qwen"} 4
# TYPE vllm:prefix_cache_hits counter
vllm:prefix_cache_hits{model_name="qwen"} 40
# TYPE vllm:prefix_cache_queries counter
vllm:prefix_cache_queries{model_name="qwen"} 80
# TYPE vllm:prompt_tokens_cached_total counter
vllm:prompt_tokens_cached_total{model_name="qwen"} 250
`))
	if second.Source != "runtime-metrics-counter-rate" {
		t.Fatalf("source = %q", second.Source)
	}
	if second.InputTokensPerSecondEstimate != 3 || second.OutputTokensPerSecondEstimate != 15 || second.TokensPerSecondEstimate != 18 {
		t.Fatalf("token rates = %#v", second)
	}
	if second.ActiveRequests != 4 {
		t.Fatalf("active requests = %d", second.ActiveRequests)
	}
	if second.PrefixCacheHitRatePercent != 50 || second.CachedPromptTokensPerSecond != 15 || second.CachedTokenReportingSupported {
		t.Fatalf("cache metric deltas = %#v", second)
	}
}

func TestBenchmarkRuntimeUsesOpenAIUsageTokens(t *testing.T) {
	calls := 0
	discovery := &kubernetesDiscovery{
		namespace:                 "team-a",
		benchmarkEnabled:          true,
		benchmarkTimeout:          time.Second,
		benchmarkMaxTokens:        8,
		benchmarkMaxContextTokens: 0,
		benchmarkMaxOutputTokens:  0,
		benchmarkMaxConcurrency:   0,
		metricsClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			body := `{"usage":{"prompt_tokens":128,"completion_tokens":1,"total_tokens":129}}`
			if calls == 2 {
				body = `{"usage":{"prompt_tokens":12,"completion_tokens":8,"total_tokens":20}}`
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		})},
		benchmarkSamples: map[string]runtimeMetricSnapshot{},
	}
	item := unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{
			"name":       "qwen",
			"namespace":  "team-a",
			"generation": int64(1),
		},
		"spec": map[string]any{
			"model":   map[string]any{"repo": "Qwen/Qwen2.5-7B-Instruct"},
			"runtime": map[string]any{"ref": "vllm"},
		},
	}}

	sample := discovery.benchmarkRuntime(context.Background(), item, "qwen-runtime", runtimeConfig{Port: 8000, MetricsPath: "/metrics"})
	if sample.Source != "runtime-microbenchmark" {
		t.Fatalf("benchmark source = %q", sample.Source)
	}
	if sample.InputTokens != 128 || sample.OutputTokens != 8 {
		t.Fatalf("usage tokens = %#v", sample)
	}
	if sample.InputTokensPerSecondEstimate <= 0 || sample.OutputTokensPerSecondEstimate <= 0 {
		t.Fatalf("token rates were not measured: %#v", sample)
	}
	if calls != 3 {
		t.Fatalf("benchmark calls = %d, want 3", calls)
	}
	cached := discovery.benchmarkRuntime(context.Background(), item, "qwen-runtime", runtimeConfig{Port: 8000, MetricsPath: "/metrics"})
	if cached.Source != "runtime-microbenchmark" || calls != 3 {
		t.Fatalf("cached benchmark = %#v calls=%d", cached, calls)
	}
}

func TestBenchmarkRuntimeBacksOffAfterFailure(t *testing.T) {
	calls := 0
	discovery := &kubernetesDiscovery{
		namespace:                 "team-a",
		benchmarkEnabled:          true,
		benchmarkTimeout:          time.Second,
		benchmarkMaxTokens:        8,
		benchmarkMaxContextTokens: 0,
		benchmarkMaxOutputTokens:  0,
		benchmarkMaxConcurrency:   0,
		metricsClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("unavailable")),
			}, nil
		})},
		benchmarkAttempts: map[string]time.Time{},
	}
	item := unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{
			"name":       "qwen",
			"namespace":  "team-a",
			"generation": int64(1),
		},
		"spec": map[string]any{
			"model":   map[string]any{"repo": "Qwen/Qwen2.5-7B-Instruct"},
			"runtime": map[string]any{"ref": "vllm"},
		},
	}}

	first := discovery.benchmarkRuntime(context.Background(), item, "qwen-runtime", runtimeConfig{Port: 8000})
	second := discovery.benchmarkRuntime(context.Background(), item, "qwen-runtime", runtimeConfig{Port: 8000})
	if first.Source != "" || second.Source != "" {
		t.Fatalf("failed benchmark should not produce samples: first=%#v second=%#v", first, second)
	}
	if calls != 1 {
		t.Fatalf("benchmark calls = %d, want 1 because failure is backed off", calls)
	}
}

func TestBenchmarkRuntimeMeasuresContextOutputAndConcurrency(t *testing.T) {
	discovery := &kubernetesDiscovery{
		namespace:                 "team-a",
		benchmarkEnabled:          true,
		benchmarkTimeout:          time.Second,
		benchmarkMaxTokens:        8,
		benchmarkMaxOutputTokens:  128,
		benchmarkMaxContextTokens: 8192,
		benchmarkMaxConcurrency:   4,
		benchmarkLatencySLOMillis: 1000,
		metricsClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if stream, _ := body["stream"].(bool); stream {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")),
				}, nil
			}
			maxTokens := intFromAny(body["max_tokens"])
			promptTokens := 12
			if messages, _ := body["messages"].([]any); len(messages) > 0 {
				if message, _ := messages[0].(map[string]any); message != nil {
					promptTokens = len(strings.Fields(fmt.Sprint(message["content"])))
				}
			}
			response := fmt.Sprintf(`{"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}}`, promptTokens, maxTokens, promptTokens+maxTokens)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(response)),
			}, nil
		})},
		benchmarkSamples: map[string]runtimeMetricSnapshot{},
	}
	item := unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{
			"name":       "qwen",
			"namespace":  "team-a",
			"generation": int64(1),
		},
		"spec": map[string]any{
			"model":   map[string]any{"repo": "Qwen/Qwen2.5-7B-Instruct"},
			"runtime": map[string]any{"ref": "vllm"},
		},
	}}

	sample := discovery.benchmarkRuntime(context.Background(), item, "qwen-runtime", runtimeConfig{Port: 8000})
	if sample.ContextWindowTokens < 8192 {
		t.Fatalf("context window was not probed: %#v", sample)
	}
	if sample.ContextWindowSource != "benchmark" {
		t.Fatalf("context source = %q", sample.ContextWindowSource)
	}
	if sample.MaxOutputTokens != 128 {
		t.Fatalf("max output tokens = %d", sample.MaxOutputTokens)
	}
	if sample.MaxOutputTokensSource != "benchmark" {
		t.Fatalf("max output source = %q", sample.MaxOutputTokensSource)
	}
	if sample.MaxConcurrentRequests != 4 {
		t.Fatalf("max concurrent requests = %d", sample.MaxConcurrentRequests)
	}
	if sample.MaxConcurrentRequestsSource != "benchmark" {
		t.Fatalf("max concurrency source = %q", sample.MaxConcurrentRequestsSource)
	}
	if sample.P50FirstTokenLatencyMillis < 0 || sample.P95TotalLatencyMillis < 0 {
		t.Fatalf("latencies were invalid: %#v", sample)
	}
}

func TestBenchmarkRuntimeUsesDeclaredContextAndOutputLimitsWithoutLargeProbe(t *testing.T) {
	var requests int
	discovery := &kubernetesDiscovery{
		namespace:                 "team-a",
		benchmarkEnabled:          true,
		benchmarkTimeout:          time.Second,
		benchmarkMaxTokens:        8,
		benchmarkMaxOutputTokens:  4096,
		benchmarkMaxContextTokens: 32768,
		benchmarkMaxConcurrency:   0,
		benchmarkLatencySLOMillis: 1000,
		metricsClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if stream, _ := body["stream"].(bool); stream {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")),
				}, nil
			}
			if prompt := fmt.Sprint(body["messages"]); len(prompt) > 2500 {
				t.Fatalf("declared context should not trigger a large context probe: %s", prompt[:120])
			}
			maxTokens := intFromAny(body["max_tokens"])
			response := fmt.Sprintf(`{"usage":{"prompt_tokens":16,"completion_tokens":%d,"total_tokens":%d}}`, maxTokens, 16+maxTokens)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(response)),
			}, nil
		})},
	}
	item := unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "smollm", "namespace": "team-a", "generation": int64(1)},
		"spec": map[string]any{
			"model": map[string]any{"repo": "llama/smollm"},
			"runtime": map[string]any{
				"ref":         "llama-cpp",
				"maxModelLen": int64(65536),
			},
		},
	}}

	sample := discovery.benchmarkRuntime(context.Background(), item, "smollm-runtime", runtimeConfig{
		Port: 8000,
		Env:  map[string]string{"MAX_OUTPUT_TOKENS": "128"},
	})
	if requests != 3 {
		t.Fatalf("benchmark request count = %d, want 3 tiny probes", requests)
	}
	if sample.ContextWindowTokens != 65536 || sample.ContextWindowSource != "runtime-config" {
		t.Fatalf("context limit = %#v", sample)
	}
	if sample.MaxOutputTokens != 128 || sample.MaxOutputTokensSource != "runtime-config" {
		t.Fatalf("output limit = %#v", sample)
	}
}

func TestBenchmarkRuntimeCachesPerDeployment(t *testing.T) {
	var alphaCalls, betaCalls int
	discovery := &kubernetesDiscovery{
		namespace:                 "team-a",
		benchmarkEnabled:          true,
		benchmarkTimeout:          time.Second,
		benchmarkMaxTokens:        4,
		benchmarkMaxOutputTokens:  4,
		benchmarkMaxContextTokens: 0,
		benchmarkMaxConcurrency:   0,
		benchmarkLatencySLOMillis: 1000,
		metricsClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if strings.HasPrefix(request.URL.Host, "alpha-runtime.") {
				alphaCalls++
			}
			if strings.HasPrefix(request.URL.Host, "beta-runtime.") {
				betaCalls++
			}
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if stream, _ := body["stream"].(bool); stream {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")),
				}, nil
			}
			maxTokens := intFromAny(body["max_tokens"])
			promptTokens := 10
			if strings.HasPrefix(request.URL.Host, "beta-runtime.") {
				promptTokens = 20
			}
			response := fmt.Sprintf(`{"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}}`, promptTokens, maxTokens, promptTokens+maxTokens)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(response)),
			}, nil
		})},
		benchmarkSamples: map[string]runtimeMetricSnapshot{},
	}
	alpha := unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "alpha", "namespace": "team-a", "generation": int64(1)},
		"spec":     map[string]any{"model": map[string]any{"repo": "Qwen/Qwen2.5-7B"}, "runtime": map[string]any{"ref": "vllm"}},
	}}
	beta := unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "beta", "namespace": "team-a", "generation": int64(1)},
		"spec":     map[string]any{"model": map[string]any{"repo": "Qwen/Qwen2.5-7B"}, "runtime": map[string]any{"ref": "vllm"}},
	}}

	alphaSample := discovery.benchmarkRuntime(context.Background(), alpha, "alpha-runtime", runtimeConfig{Port: 8000})
	betaSample := discovery.benchmarkRuntime(context.Background(), beta, "beta-runtime", runtimeConfig{Port: 8000})
	alphaCached := discovery.benchmarkRuntime(context.Background(), alpha, "alpha-runtime", runtimeConfig{Port: 8000})
	if alphaSample.Source == "" || betaSample.Source == "" || alphaCached.Source == "" {
		t.Fatalf("expected benchmark samples: alpha=%#v beta=%#v cached=%#v", alphaSample, betaSample, alphaCached)
	}
	if len(discovery.benchmarkSamples) != 2 {
		t.Fatalf("benchmark sample count = %d, want 2", len(discovery.benchmarkSamples))
	}
	if alphaCalls == 0 || betaCalls == 0 {
		t.Fatalf("expected both deployments to be called: alpha=%d beta=%d", alphaCalls, betaCalls)
	}
	if alphaCached.InputTokens != alphaSample.InputTokens || betaSample.InputTokens == alphaSample.InputTokens {
		t.Fatalf("cached/deployment-specific samples incorrect: alpha=%#v beta=%#v cached=%#v", alphaSample, betaSample, alphaCached)
	}
}

func TestBenchmarkProbeLimitsPreferRuntimeConfiguration(t *testing.T) {
	discovery := &kubernetesDiscovery{
		benchmarkMaxTokens:        16,
		benchmarkMaxOutputTokens:  4096,
		benchmarkMaxContextTokens: 32768,
		benchmarkMaxConcurrency:   64,
		benchmarkLatencySLOMillis: 1000,
	}
	item := unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "small-ctx", "namespace": "team-a"},
		"spec": map[string]any{
			"model": map[string]any{"repo": "Qwen/Qwen2.5-7B-Instruct"},
			"runtime": map[string]any{
				"ref":         "llama-cpp",
				"maxModelLen": int64(512),
			},
		},
	}}

	limits := discovery.benchmarkProbeLimits(item, runtimeConfig{
		Env: map[string]string{
			"MAX_MODEL_LEN":     "8192",
			"MAX_NUM_SEQS":      "32",
			"MAX_OUTPUT_TOKENS": "1024",
		},
	})
	if limits.ContextTokens != 512 || limits.ContextSource != "runtime-config" {
		t.Fatalf("context limit = %#v", limits)
	}
	if limits.OutputTokens != 512 || limits.OutputSource != "runtime-config-capped" {
		t.Fatalf("output limit should be capped by context window: %#v", limits)
	}
	if limits.Concurrency != 32 || limits.ConcurrencySource != "runtime-config" {
		t.Fatalf("concurrency limit = %#v", limits)
	}
}

func TestAutomaticCapacityPreservesConfiguredSmallContextWindow(t *testing.T) {
	estimate := estimateAutomaticCapacity(
		"jc-builds/SmolLM2-135M-Instruct-Q4_K_M-GGUF",
		"llama-cpp",
		gpuSnapshot{Vendor: "unknown", Type: "cpu", Count: 1, MemoryGB: 1},
		map[string]any{"maxModelLen": int64(512)},
	)
	if estimate.ContextWindowTokens != 512 {
		t.Fatalf("context window = %d, want 512", estimate.ContextWindowTokens)
	}
}

func TestAutomaticCapacityDoesNotPublishUnknownContextWindow(t *testing.T) {
	estimate := estimateAutomaticCapacity(
		"unknown-model",
		"vllm",
		gpuSnapshot{Vendor: "NVIDIA", Type: "H100", Count: 1, MemoryGB: 80},
		map[string]any{},
	)
	if estimate.ContextWindowTokens != 0 {
		t.Fatalf("context window = %d, want unknown/0", estimate.ContextWindowTokens)
	}
	if estimate.TokensPerSecond <= 0 || estimate.MaxConcurrentRequests <= 0 {
		t.Fatalf("capacity estimate should still calculate internal throughput: %#v", estimate)
	}
}

func TestModelOfferingPrefersRuntimeMeasurementsOverStatusDefaults(t *testing.T) {
	item := unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "qwen"},
		"spec": map[string]any{
			"model":   map[string]any{"repo": "Qwen/Qwen2.5-7B-Instruct"},
			"runtime": map[string]any{"ref": "vllm", "maxModelLen": int64(4096)},
		},
		"status": map[string]any{
			"phase":        "Active",
			"endpointPath": "/v1",
			"capacity": map[string]any{
				"tokensPerSecondEstimate": int64(1),
				"tokensPerMinuteEstimate": int64(60),
				"maxConcurrentRequests":   int64(1),
			},
			"rateLimits": map[string]any{
				"maxTokensPerMinute": int64(1),
			},
		},
	}}
	offering, ok := modelDeploymentOfferingWithMetrics(item, "team-a", "us-test-1", gpuSnapshot{
		Vendor:   "NVIDIA",
		Type:     "H100",
		Count:    1,
		MemoryGB: 80,
	}, nil, runtimeMetricSnapshot{
		InputTokensPerSecondEstimate:  900,
		OutputTokensPerSecondEstimate: 300,
		TokensPerSecondEstimate:       1200,
		MaxConcurrentRequests:         8,
		MaxConcurrentRequestsSource:   "benchmark",
		Source:                        "runtime-microbenchmark",
	})
	if !ok {
		t.Fatal("expected offering")
	}
	if offering.ObservedCapacity.TokensPerSecondEstimate != 1200 || offering.ObservedCapacity.TokensPerMinuteEstimate != 72000 {
		t.Fatalf("runtime token throughput did not override status defaults: %#v", offering.ObservedCapacity)
	}
	if offering.ObservedCapacity.MaxConcurrentRequests != 8 {
		t.Fatalf("runtime concurrency did not override status default: %#v", offering.ObservedCapacity)
	}
	if offering.RateLimits.MaxTokensPerMinute != 72000 {
		t.Fatalf("rate limit TPM did not move off default: %#v", offering.RateLimits)
	}
	if offering.ObservedCapacity.CapacitySource != "runtime-microbenchmark" {
		t.Fatalf("capacity source = %q", offering.ObservedCapacity.CapacitySource)
	}
}

func TestModelOfferingIgnoresPlaceholderStatusThroughput(t *testing.T) {
	item := unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "qwen"},
		"spec": map[string]any{
			"model":   map[string]any{"repo": "Qwen/Qwen2.5-7B-Instruct"},
			"runtime": map[string]any{"ref": "vllm", "maxModelLen": int64(4096)},
		},
		"status": map[string]any{
			"phase":        "Active",
			"endpointPath": "/v1",
			"capacity": map[string]any{
				"tokensPerSecondEstimate": int64(1),
				"tokensPerMinuteEstimate": int64(60),
				"maxConcurrentRequests":   int64(1),
			},
			"rateLimits": map[string]any{
				"maxTokensPerMinute": int64(1),
			},
		},
	}}
	offering, ok := modelDeploymentOfferingWithMetrics(item, "team-a", "us-test-1", gpuSnapshot{
		Vendor:   "NVIDIA",
		Type:     "H100",
		Count:    1,
		MemoryGB: 80,
	}, nil, runtimeMetricSnapshot{})
	if !ok {
		t.Fatal("expected offering")
	}
	if offering.ObservedCapacity.TokensPerSecondEstimate <= 1 || offering.ObservedCapacity.TokensPerMinuteEstimate <= 60 {
		t.Fatalf("placeholder status throughput should not be preserved: %#v", offering.ObservedCapacity)
	}
	if offering.ObservedCapacity.MaxConcurrentRequests <= 1 {
		t.Fatalf("placeholder status concurrency should not beat automatic capacity: %#v", offering.ObservedCapacity)
	}
	if offering.ObservedCapacity.CapacitySource != "hardware-model-estimate" {
		t.Fatalf("capacity source = %q", offering.ObservedCapacity.CapacitySource)
	}
	if offering.RateLimits.MaxTokensPerMinute <= 60 {
		t.Fatalf("rate limit TPM should move off placeholder: %#v", offering.RateLimits)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func parseMetricFamilies(t *testing.T, text string) map[string]*dto.MetricFamily {
	t.Helper()
	parser := expfmt.TextParser{}
	families, err := parser.TextToMetricFamilies(bytes.NewBufferString(text))
	if err != nil {
		t.Fatalf("parse metrics: %v", err)
	}
	return families
}

func containsString(value any, want string) bool {
	items, ok := value.([]string)
	if ok {
		for _, item := range items {
			if item == want {
				return true
			}
		}
		return false
	}
	anyItems, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range anyItems {
		if text, ok := item.(string); ok && text == want {
			return true
		}
	}
	return false
}

func TestNodeCapacityFiltersRelevantResources(t *testing.T) {
	items := []unstructured.Unstructured{
		{Object: map[string]any{
			"metadata": map[string]any{"name": "node-a"},
			"status": map[string]any{
				"allocatable": map[string]any{
					"cpu":                "8",
					"memory":             "32Gi",
					"nvidia.com/gpu":     "1",
					"gpu.intel.com/i915": "2",
					"pods":               "110",
				},
			},
		}},
	}
	nodes := nodeCapacity(items)
	if len(nodes) != 1 {
		t.Fatalf("got %d nodes, want 1", len(nodes))
	}
	if _, found := nodes[0].Allocatable["pods"]; found {
		t.Fatalf("pods resource should not be reported: %#v", nodes[0].Allocatable)
	}
	if nodes[0].Allocatable["nvidia.com/gpu"] != "1" {
		t.Fatalf("GPU allocatable missing: %#v", nodes[0].Allocatable)
	}
	if nodes[0].Allocatable["gpu.intel.com/i915"] != "2" {
		t.Fatalf("Intel GPU allocatable missing: %#v", nodes[0].Allocatable)
	}
}
