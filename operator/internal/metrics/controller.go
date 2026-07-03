package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Recorder is the bounded-cardinality observability contract used by
// controllers.
type Recorder interface {
	SetGPUSlots(resource string, total, occupied, available float64)
	SetActivationQueueDepth(depth float64)
	ObserveActivationDuration(duration time.Duration)
	ObserveCacheDownloadDuration(duration time.Duration)
	IncFailure(controller, reason string)
}

// NoOpRecorder discards controller metrics.
type NoOpRecorder struct{}

func (NoOpRecorder) SetGPUSlots(string, float64, float64, float64) {}
func (NoOpRecorder) SetActivationQueueDepth(float64)               {}
func (NoOpRecorder) ObserveActivationDuration(time.Duration)       {}
func (NoOpRecorder) ObserveCacheDownloadDuration(time.Duration)    {}
func (NoOpRecorder) IncFailure(string, string)                     {}

// ControllerMetrics owns Prometheus collectors for lifecycle controllers.
// Labels deliberately exclude object names, repositories, and UIDs.
type ControllerMetrics struct {
	gpuSlotsTotal       *prometheus.GaugeVec
	gpuSlotsOccupied    *prometheus.GaugeVec
	gpuSlotsAvailable   *prometheus.GaugeVec
	activationQueue     prometheus.Gauge
	activationDuration  prometheus.Histogram
	cacheDownload       prometheus.Histogram
	reconciliationError *prometheus.CounterVec
	activationFailures  *prometheus.CounterVec
	cacheFailures       *prometheus.CounterVec
}

// NewControllerMetrics registers controller collectors with the supplied
// registry.
func NewControllerMetrics(registerer prometheus.Registerer) (*ControllerMetrics, error) {
	metrics := &ControllerMetrics{
		gpuSlotsTotal: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "inferops_gpu_slots_total",
			Help: "Allocatable whole-GPU slots visible to InferOps.",
		}, []string{"resource"}),
		gpuSlotsOccupied: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "inferops_gpu_slots_occupied",
			Help: "Whole-GPU slots reserved by InferOps activations.",
		}, []string{"resource"}),
		gpuSlotsAvailable: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "inferops_gpu_slots_available",
			Help: "Whole-GPU slots available for InferOps activations.",
		}, []string{"resource"}),
		activationQueue: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "inferops_activation_queue_depth",
			Help: "Model deployments currently waiting for GPU capacity.",
		}),
		activationDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "inferops_model_activation_duration_seconds",
			Help:    "Time from runtime Deployment creation to readiness.",
			Buckets: prometheus.ExponentialBuckets(1, 2, 12),
		}),
		cacheDownload: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "inferops_cache_download_duration_seconds",
			Help:    "Time from downloader Job creation to verified completion.",
			Buckets: prometheus.ExponentialBuckets(5, 2, 13),
		}),
		reconciliationError: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "inferops_controller_failures_total",
			Help: "Terminal lifecycle failures by controller and stable reason.",
		}, []string{"controller", "reason"}),
		activationFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "inferops_activation_failures_total",
			Help: "Terminal model activation failures by stable reason.",
		}, []string{"reason"}),
		cacheFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "inferops_cache_download_failures_total",
			Help: "Terminal model cache failures by stable reason.",
		}, []string{"reason"}),
	}
	collectors := []prometheus.Collector{
		metrics.gpuSlotsTotal,
		metrics.gpuSlotsOccupied,
		metrics.gpuSlotsAvailable,
		metrics.activationQueue,
		metrics.activationDuration,
		metrics.cacheDownload,
		metrics.reconciliationError,
		metrics.activationFailures,
		metrics.cacheFailures,
	}
	for _, collector := range collectors {
		if err := registerer.Register(collector); err != nil {
			return nil, err
		}
	}
	return metrics, nil
}

func (m *ControllerMetrics) SetGPUSlots(resource string, total, occupied, available float64) {
	m.gpuSlotsTotal.WithLabelValues(resource).Set(total)
	m.gpuSlotsOccupied.WithLabelValues(resource).Set(occupied)
	m.gpuSlotsAvailable.WithLabelValues(resource).Set(available)
}

func (m *ControllerMetrics) SetActivationQueueDepth(depth float64) {
	m.activationQueue.Set(depth)
}

func (m *ControllerMetrics) ObserveActivationDuration(duration time.Duration) {
	m.activationDuration.Observe(duration.Seconds())
}

func (m *ControllerMetrics) ObserveCacheDownloadDuration(duration time.Duration) {
	m.cacheDownload.Observe(duration.Seconds())
}

func (m *ControllerMetrics) IncFailure(controller, reason string) {
	m.reconciliationError.WithLabelValues(controller, reason).Inc()
	switch controller {
	case "modeldeployment":
		m.activationFailures.WithLabelValues(reason).Inc()
	case "modelcache":
		m.cacheFailures.WithLabelValues(reason).Inc()
	}
}
