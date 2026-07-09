package controllers

import (
	"strings"
	"testing"

	"github.com/prometheus/common/expfmt"
)

func TestRuntimeMetricsFromFamilies(t *testing.T) {
	t.Parallel()

	parser := expfmt.TextParser{}
	families, err := parser.TextToMetricFamilies(strings.NewReader(`
# HELP vllm:num_requests_waiting Number of requests waiting to be scheduled.
# TYPE vllm:num_requests_waiting gauge
vllm:num_requests_waiting{model_name="qwen"} 2
vllm:num_requests_waiting{model_name="coder"} 1
# HELP vllm:num_requests_running Number of requests currently running.
# TYPE vllm:num_requests_running gauge
vllm:num_requests_running{model_name="qwen"} 4
`))
	if err != nil {
		t.Fatalf("parse metric families: %v", err)
	}

	metrics := runtimeMetricsFromFamilies(families)
	if metrics.PendingRequests != 3 {
		t.Fatalf("pending requests = %d, want 3", metrics.PendingRequests)
	}
	if metrics.RunningRequests != 4 {
		t.Fatalf("running requests = %d, want 4", metrics.RunningRequests)
	}
}

func TestRuntimeMetricsFromFamiliesTreatsMissingMetricsAsZero(t *testing.T) {
	t.Parallel()

	metrics := runtimeMetricsFromFamilies(nil)
	if metrics.PendingRequests != 0 || metrics.RunningRequests != 0 {
		t.Fatalf("runtime metrics = %#v, want zero values", metrics)
	}
}
