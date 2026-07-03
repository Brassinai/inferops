// Package metrics implements bounded-cardinality gateway request metrics.
package metrics

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const clientClosedRequestStatus = 499

// Recorder owns the gateway Prometheus collectors.
type Recorder struct {
	requests       *prometheus.CounterVec
	latency        *prometheus.HistogramVec
	activeRequests prometheus.Gauge
	upstreamErrors *prometheus.CounterVec
}

// NewRecorder registers gateway collectors with registerer.
func NewRecorder(registerer prometheus.Registerer) (*Recorder, error) {
	if registerer == nil {
		return nil, errors.New("metrics registerer is required")
	}
	recorder := &Recorder{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "inferops_gateway_requests_total",
			Help: "Gateway requests by bounded HTTP method and response status.",
		}, []string{"method", "status_code"}),
		latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "inferops_gateway_request_duration_seconds",
			Help:    "End-to-end gateway request duration by bounded HTTP method.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method"}),
		activeRequests: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "inferops_gateway_active_requests",
			Help: "Requests currently handled by the gateway.",
		}),
		upstreamErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "inferops_gateway_upstream_errors_total",
			Help: "Runtime upstream failures by stable reason.",
		}, []string{"reason"}),
	}
	collectors := []prometheus.Collector{
		recorder.requests,
		recorder.latency,
		recorder.activeRequests,
		recorder.upstreamErrors,
	}
	registered := make([]prometheus.Collector, 0, len(collectors))
	for _, collector := range collectors {
		if err := registerer.Register(collector); err != nil {
			for _, previous := range registered {
				registerer.Unregister(previous)
			}
			return nil, err
		}
		registered = append(registered, collector)
	}
	return recorder, nil
}

// Middleware records request count, latency, and in-flight request count.
func (r *Recorder) Middleware(next http.Handler) http.Handler {
	if next == nil {
		panic("metrics middleware requires a next handler")
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		started := time.Now()
		method := boundedMethod(request.Method)
		r.activeRequests.Inc()
		defer r.activeRequests.Dec()

		writer := &statusWriter{ResponseWriter: response}
		next.ServeHTTP(writer, request)
		status := writer.status
		if !writer.wroteHeader {
			status = http.StatusOK
			if request.Context().Err() != nil {
				status = clientClosedRequestStatus
			}
		}
		r.requests.WithLabelValues(method, strconv.Itoa(status)).Inc()
		r.latency.WithLabelValues(method).Observe(time.Since(started).Seconds())
	})
}

// ObserveUpstreamError records a stable upstream failure reason.
func (r *Recorder) ObserveUpstreamError(reason string) {
	switch reason {
	case "transport", "response_5xx":
	default:
		reason = "other"
	}
	r.upstreamErrors.WithLabelValues(reason).Inc()
}

func boundedMethod(method string) string {
	method = strings.ToUpper(method)
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions:
		return method
	default:
		return "OTHER"
	}
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(content []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(content)
}

func (w *statusWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *statusWriter) ReadFrom(reader io.Reader) (int64, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return io.Copy(w.ResponseWriter, reader)
}

func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
