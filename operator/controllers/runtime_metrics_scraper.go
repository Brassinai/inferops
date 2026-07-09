package controllers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

const runtimeMetricsTimeout = 2 * time.Second

// RuntimeMetrics contains bounded runtime queue signals used for replica
// planning. Missing metrics are reported as zero by the scraper.
type RuntimeMetrics struct {
	PendingRequests int64
	RunningRequests int64
}

// RuntimeMetricsScraper reads runtime metrics for one managed Service.
type RuntimeMetricsScraper interface {
	ScrapeRuntimeMetrics(
		ctx context.Context,
		namespace, serviceName string,
		port int32,
		path string,
	) (RuntimeMetrics, error)
}

// HTTPRuntimeMetricsScraper reads Prometheus text metrics from the runtime
// Service DNS name. It is intentionally small and has a short timeout so
// metrics outages cannot stall lifecycle reconciliation.
type HTTPRuntimeMetricsScraper struct {
	client *http.Client
}

// NewHTTPRuntimeMetricsScraper creates a runtime metrics scraper.
func NewHTTPRuntimeMetricsScraper() *HTTPRuntimeMetricsScraper {
	return &HTTPRuntimeMetricsScraper{
		client: &http.Client{Timeout: runtimeMetricsTimeout},
	}
}

func (s *HTTPRuntimeMetricsScraper) ScrapeRuntimeMetrics(
	ctx context.Context,
	namespace, serviceName string,
	port int32,
	path string,
) (RuntimeMetrics, error) {
	if s == nil {
		return RuntimeMetrics{}, fmt.Errorf("runtime metrics scraper is required")
	}
	if port <= 0 {
		return RuntimeMetrics{}, fmt.Errorf("runtime metrics port must be greater than zero")
	}
	if path == "" {
		path = "/metrics"
	}
	u := url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s.%s.svc:%d", serviceName, namespace, port),
		Path:   path,
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return RuntimeMetrics{}, fmt.Errorf("build runtime metrics request: %w", err)
	}
	client := s.client
	if client == nil {
		client = &http.Client{Timeout: runtimeMetricsTimeout}
	}
	response, err := client.Do(request)
	if err != nil {
		return RuntimeMetrics{}, fmt.Errorf("scrape runtime metrics: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return RuntimeMetrics{}, fmt.Errorf("runtime metrics returned HTTP %d", response.StatusCode)
	}
	parser := expfmt.TextParser{}
	families, err := parser.TextToMetricFamilies(response.Body)
	if err != nil {
		return RuntimeMetrics{}, fmt.Errorf("parse runtime metrics: %w", err)
	}
	return runtimeMetricsFromFamilies(families), nil
}

func runtimeMetricsFromFamilies(families map[string]*dto.MetricFamily) RuntimeMetrics {
	return RuntimeMetrics{
		PendingRequests: sumGaugeFamily(families["vllm:num_requests_waiting"]),
		RunningRequests: sumGaugeFamily(families["vllm:num_requests_running"]),
	}
}

func sumGaugeFamily(family *dto.MetricFamily) int64 {
	if family == nil {
		return 0
	}
	var total float64
	for _, metric := range family.Metric {
		if metric == nil || metric.Gauge == nil || metric.Gauge.Value == nil {
			continue
		}
		total += metric.Gauge.GetValue()
	}
	if total < 0 {
		return 0
	}
	return int64(total + 0.5)
}
