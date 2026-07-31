package main

import (
	"testing"
	"time"

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
			"metadata": map[string]any{"name": "z-model"},
			"status": map[string]any{
				"phase":        "Active",
				"endpoint":     "/models/z-model",
				"modelName":    "org/z-model",
				"runtime":      "llama.cpp",
				"endpointPath": "/v1",
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
			"status": map[string]any{"phase": "Active"},
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
	offerings := modelOfferings(items, "team-a", "us-test-1", gpuSnapshot{Vendor: "unknown", Type: "unknown", Count: 1, MemoryGB: 1})
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
	if offerings[1].Model != "jc-builds/SmolLM2-135M-Instruct-Q4_K_M-GGUF" || offerings[1].Runtime != "llama-cpp" {
		t.Fatalf("yaml model/runtime not discovered: %#v", offerings[1])
	}
	if offerings[1].Endpoint != "/models/cpu-smollm" || offerings[1].EndpointPath != "/v1" || offerings[1].CustomEndpointsPresent {
		t.Fatalf("yaml endpoint eligibility not discovered: %#v", offerings[1])
	}
	if offerings[2].Model != "org/status-model-map" {
		t.Fatalf("status model map was not unwrapped: %#v", offerings[2])
	}
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
