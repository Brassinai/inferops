package metrics

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestControllerMetricsUseBoundedLabels(t *testing.T) {
	t.Parallel()

	registry := prometheus.NewRegistry()
	recorder, err := NewControllerMetrics(registry)
	if err != nil {
		t.Fatalf("NewControllerMetrics() error = %v", err)
	}
	recorder.SetGPUSlots("nvidia.com/gpu", 4, 1, 3)
	recorder.SetActivationQueueDepth(2)
	recorder.ObserveActivationDuration(2 * time.Second)
	recorder.ObserveCacheDownloadDuration(10 * time.Second)
	recorder.IncFailure("modeldeployment", "InsufficientCapacity")
	recorder.IncFailure("modelcache", "DownloadFailed")

	count, err := testutil.GatherAndCount(
		registry,
		"inferops_gpu_slots_total",
		"inferops_activation_queue_depth",
		"inferops_model_activation_duration_seconds",
		"inferops_cache_download_duration_seconds",
		"inferops_controller_failures_total",
		"inferops_activation_failures_total",
		"inferops_cache_download_failures_total",
	)
	if err != nil {
		t.Fatalf("GatherAndCount() error = %v", err)
	}
	if count != 8 {
		t.Fatalf("metric family count = %d, want 8", count)
	}
	output := testutil.ToFloat64(recorder.activationQueue)
	if output != 2 {
		t.Errorf("queue depth = %v, want 2", output)
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	for _, family := range families {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if strings.Contains(label.GetName(), "model") ||
					strings.Contains(label.GetName(), "namespace") ||
					strings.Contains(label.GetName(), "uid") {
					t.Errorf("metric %s has high-cardinality label %q", family.GetName(), label.GetName())
				}
			}
		}
	}
}
