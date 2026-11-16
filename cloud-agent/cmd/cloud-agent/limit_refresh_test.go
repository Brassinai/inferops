package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestThroughputGaugesDoNotPreventMissingOutputLimitProbe(t *testing.T) {
	requests := 0
	d := &kubernetesDiscovery{
		namespace: "default", benchmarkEnabled: true, benchmarkTimeout: time.Second,
		benchmarkMaxTokens: 8, benchmarkMaxOutputTokens: 64,
		benchmarkSamples: map[string]runtimeMetricSnapshot{},
		metricsClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body := "# TYPE llamacpp:predicted_tokens_seconds gauge\nllamacpp:predicted_tokens_seconds 20\n"
			if r.Method == http.MethodPost {
				requests++
				body = `{"usage":{"prompt_tokens":10,"completion_tokens":1,"total_tokens":11}}`
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		})},
	}
	item := unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "coder", "namespace": "default", "generation": int64(1)},
		"spec":     map[string]any{"model": map[string]any{"repo": "test/coder"}, "runtime": map[string]any{"ref": "custom", "maxModelLen": int64(8192)}},
		"status":   map[string]any{"phase": "Active"},
	}}
	configs := map[string]runtimeConfig{"custom": {Engine: "llama-cpp", Port: 8000, MetricsPath: "/metrics"}}
	first := d.runtimeMetricSnapshots(context.Background(), []unstructured.Unstructured{item}, configs)["coder"]
	if first.MaxOutputTokens != 64 || first.MaxOutputTokensSource != "benchmark" || requests == 0 {
		t.Fatalf("missing limit evidence was not rebuilt: %+v requests=%d", first, requests)
	}
	before := requests
	second := d.runtimeMetricSnapshots(context.Background(), []unstructured.Unstructured{item}, configs)["coder"]
	if second.MaxOutputTokens != 64 || requests != before {
		t.Fatalf("cached limit evidence was not reused: %+v requests=%d", second, requests)
	}
}

func TestLlamaCppNativeOutputSettingIsReported(t *testing.T) {
	item := unstructured.Unstructured{Object: map[string]any{}}
	got := declaredRuntimeLimitsForDeployment(item, runtimeConfig{Engine: "llama-cpp", Env: map[string]string{"LLAMA_ARG_N_PREDICT": "256", "MAX_NUM_SEQS": "1"}})
	if got.OutputTokens != 256 || got.OutputSource != "runtime-config" || got.Concurrency != 1 {
		t.Fatalf("native settings not reported: %+v", got)
	}
	metrics := applyDeclaredRuntimeLimits(runtimeMetricSnapshot{MaxConcurrentRequests: 12}, got)
	if metrics.MaxConcurrentRequests != 1 || metrics.MaxConcurrentRequestsSource != "runtime-config" {
		t.Fatalf("configured slot limit was not enforced: %+v", metrics)
	}
}
