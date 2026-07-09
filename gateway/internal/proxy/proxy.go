// Package proxy implements the OpenAI-compatible gateway reverse proxy.
package proxy

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brassinai/inferops/gateway/internal/routing"
)

const retryAfterSeconds = 5

// Proxy routes requests using a model registry.
type Proxy struct {
	registry   routing.Registry
	transport  http.RoundTripper
	errorLog   *log.Logger
	requestLog *log.Logger
	metrics    metricsRecorder
	tracker    *requestTracker
	limiter    *rateLimiter
	options    Options
}

// Options configures gateway policies that may be overridden per backend.
type Options struct {
	DefaultRateLimit      *routing.RateLimitPolicy
	RequestLoggingEnabled *bool
	RequestLogger         *log.Logger
}

type metricsRecorder interface {
	ObserveUpstreamError(reason string)
}

// New creates a gateway proxy backed by registry.
func New(registry routing.Registry) (*Proxy, error) {
	return NewWithMetrics(registry, nil)
}

// NewWithMetrics creates a gateway proxy and records upstream failures when a
// recorder is supplied.
func NewWithMetrics(registry routing.Registry, recorder metricsRecorder) (*Proxy, error) {
	return NewWithMetricsAndOptions(registry, recorder, Options{})
}

// NewWithMetricsAndOptions creates a gateway proxy with explicit policy
// defaults for backends that do not define their own routing policy.
func NewWithMetricsAndOptions(
	registry routing.Registry,
	recorder metricsRecorder,
	options Options,
) (*Proxy, error) {
	if registry == nil {
		return nil, errors.New("model registry is required")
	}
	if options.DefaultRateLimit != nil {
		if options.DefaultRateLimit.RequestsPerMinute < 0 {
			return nil, errors.New("default rate limit requestsPerMinute must be non-negative")
		}
		if options.DefaultRateLimit.Burst < 0 {
			return nil, errors.New("default rate limit burst must be non-negative")
		}
	}
	requestLog := options.RequestLogger
	if requestLog == nil {
		requestLog = log.New(os.Stdout, "gateway request: ", log.LstdFlags)
	}
	return &Proxy{
		registry:   registry,
		transport:  http.DefaultTransport,
		errorLog:   log.New(os.Stderr, "gateway proxy: ", log.LstdFlags),
		requestLog: requestLog,
		metrics:    recorder,
		tracker:    newRequestTracker(),
		limiter:    newRateLimiter(),
		options:    cloneOptions(options),
	}, nil
}

// ServeHTTP resolves a model route, enforces lifecycle state, and forwards only
// OpenAI-compatible /v1 paths to a ready backend.
func (p *Proxy) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if !canonicalRequestPath(request.URL) {
		writeError(response, http.StatusBadRequest, "invalid_path", "invalid_request_error", "request path must not contain escapes, backslashes, or traversal segments")
		return
	}
	backend, upstreamPath, found := p.lookup(request.URL.Path)
	if !found {
		writeError(response, http.StatusNotFound, "model_not_found", "unknown_model", "model route was not found")
		return
	}
	logging := p.requestLoggingEnabled(backend)
	if logging {
		writer := &accessLogWriter{ResponseWriter: response}
		started := time.Now()
		defer p.logRequest(writer, request, backend, started)
		response = writer
	}

	switch backend.State {
	case routing.StateReady:
		if upstreamPath != "/v1" && !strings.HasPrefix(upstreamPath, "/v1/") {
			writeError(response, http.StatusNotFound, "invalid_path", "invalid_request_error", "model routes accept only /v1 endpoints")
			return
		}
		if allowed, retryAfter := p.rateLimitAllowed(backend); !allowed {
			response.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			writeError(response, http.StatusTooManyRequests, "rate_limit_exceeded", "rate_limit_error", "model route rate limit exceeded")
			return
		}
		p.forward(response, request, backend, upstreamPath)
	case routing.StateInactive:
		writeError(response, http.StatusConflict, "model_inactive", "inactive_model", stateMessage(backend, "model is inactive"))
	case routing.StateActivating:
		response.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
		writeError(response, http.StatusServiceUnavailable, "model_activating", "activating_model", stateMessage(backend, "model is activating"))
	case routing.StateDraining:
		response.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
		writeError(response, http.StatusServiceUnavailable, "model_draining", "unavailable_model", stateMessage(backend, "model is draining"))
	default:
		response.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
		writeError(response, http.StatusServiceUnavailable, "model_unavailable", "unavailable_model", stateMessage(backend, "model is unavailable"))
	}
}

func (p *Proxy) lookup(requestPath string) (routing.Backend, string, bool) {
	if selectable, ok := p.registry.(routing.SelectingRegistry); ok {
		return selectable.Select(requestPath, func(backend routing.Backend) int {
			return p.tracker.activeCount(backend)
		})
	}
	return p.registry.Lookup(requestPath)
}

func canonicalRequestPath(requestURL *url.URL) bool {
	if requestURL == nil ||
		requestURL.Path == "" ||
		requestURL.RawPath != "" ||
		strings.Contains(requestURL.Path, "\\") {
		return false
	}
	withoutTrailingSlash := strings.TrimSuffix(requestURL.Path, "/")
	if withoutTrailingSlash == "" {
		withoutTrailingSlash = "/"
	}
	return path.Clean(requestURL.Path) == withoutTrailingSlash
}

func (p *Proxy) forward(
	response http.ResponseWriter,
	request *http.Request,
	backend routing.Backend,
	upstreamPath string,
) {
	done := p.tracker.begin(backend)
	defer done()

	reverseProxy := &httputil.ReverseProxy{
		Rewrite: func(proxyRequest *httputil.ProxyRequest) {
			proxyRequest.SetURL(backend.Endpoint)
			proxyRequest.SetXForwarded()
			proxyRequest.Out.URL.Path = joinURLPath(backend.Endpoint, upstreamPath)
			proxyRequest.Out.URL.RawPath = ""
		},
		Transport:     p.transport,
		FlushInterval: -1,
		ErrorLog:      p.errorLog,
		ModifyResponse: func(upstreamResponse *http.Response) error {
			upstreamResponse.Header.Set("X-Accel-Buffering", "no")
			if upstreamResponse.StatusCode >= http.StatusInternalServerError && p.metrics != nil {
				p.metrics.ObserveUpstreamError("response_5xx")
			}
			return nil
		},
		ErrorHandler: func(writer http.ResponseWriter, upstreamRequest *http.Request, err error) {
			if upstreamRequest.Context().Err() != nil {
				return
			}
			if p.metrics != nil {
				p.metrics.ObserveUpstreamError("transport")
			}
			p.errorLog.Printf("model=%q upstream request failed: %v", backend.Name, err)
			writeError(writer, http.StatusBadGateway, "upstream_error", "api_error", "model runtime request failed")
		},
	}
	reverseProxy.ServeHTTP(response, request)
}

func (p *Proxy) rateLimitAllowed(backend routing.Backend) (bool, int) {
	policy := p.rateLimitPolicy(backend)
	if policy == nil || policy.RequestsPerMinute <= 0 {
		return true, 0
	}
	return p.limiter.allow(keyForBackend(backend), *policy)
}

func (p *Proxy) rateLimitPolicy(backend routing.Backend) *routing.RateLimitPolicy {
	if backend.Policy.RateLimit != nil {
		return backend.Policy.RateLimit
	}
	return p.options.DefaultRateLimit
}

func (p *Proxy) requestLoggingEnabled(backend routing.Backend) bool {
	if backend.Policy.RequestLogging != nil && backend.Policy.RequestLogging.Enabled != nil {
		return *backend.Policy.RequestLogging.Enabled
	}
	return p.options.RequestLoggingEnabled != nil && *p.options.RequestLoggingEnabled
}

func (p *Proxy) logRequest(
	writer *accessLogWriter,
	request *http.Request,
	backend routing.Backend,
	started time.Time,
) {
	status := writer.status
	if status == 0 {
		status = http.StatusOK
		if request.Context().Err() != nil {
			status = 499
		}
	}
	p.requestLog.Printf(
		"model=%q namespace=%q route=%q method=%q path=%q status=%d duration_ms=%d",
		backend.Name,
		backend.Namespace,
		backend.RoutePrefix,
		request.Method,
		request.URL.Path,
		status,
		time.Since(started).Milliseconds(),
	)
}

// ServeDrainStatus reports active request counts and drain completion state for
// the operator. It is intentionally read-only and derived from the gateway's
// current registry snapshot plus requests already admitted by this process.
func (p *Proxy) ServeDrainStatus(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed", "invalid_request_error", "method not allowed")
		return
	}
	snapshot, ok := p.registry.(routing.SnapshotRegistry)
	if !ok {
		writeError(response, http.StatusServiceUnavailable, "drain_status_unavailable", "api_error", "backend registry does not expose drain status")
		return
	}

	namespaceFilter := strings.TrimSpace(request.URL.Query().Get("namespace"))
	modelFilter := strings.TrimSpace(request.URL.Query().Get("model"))
	backends := snapshot.Backends()
	statuses := make([]drainBackendStatus, 0, len(backends))
	for _, backend := range backends {
		if namespaceFilter != "" && backend.Namespace != namespaceFilter {
			continue
		}
		if modelFilter != "" && backend.Name != modelFilter {
			continue
		}
		active := p.tracker.activeCount(backend)
		statuses = append(statuses, drainBackendStatus{
			Namespace:      backend.Namespace,
			Model:          backend.Name,
			RoutePrefix:    backend.RoutePrefix,
			State:          string(backend.State),
			ActiveRequests: active,
			Draining:       backend.State == routing.StateDraining,
			DrainComplete:  backend.State == routing.StateDraining && active == 0,
			Message:        backend.Message,
		})
	}
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(drainStatusResponse{Backends: statuses})
}

type drainStatusResponse struct {
	Backends []drainBackendStatus `json:"backends"`
}

type drainBackendStatus struct {
	Namespace      string `json:"namespace,omitempty"`
	Model          string `json:"model"`
	RoutePrefix    string `json:"routePrefix"`
	State          string `json:"state"`
	ActiveRequests int    `json:"activeRequests"`
	Draining       bool   `json:"draining"`
	DrainComplete  bool   `json:"drainComplete"`
	Message        string `json:"message,omitempty"`
}

type requestTracker struct {
	mu     sync.Mutex
	active map[backendKey]int
}

type backendKey struct {
	namespace string
	name      string
}

func newRequestTracker() *requestTracker {
	return &requestTracker{active: make(map[backendKey]int)}
}

func (t *requestTracker) begin(backend routing.Backend) func() {
	if t == nil {
		return func() {}
	}
	key := keyForBackend(backend)
	t.mu.Lock()
	t.active[key]++
	t.mu.Unlock()
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.active[key] <= 1 {
			delete(t.active, key)
			return
		}
		t.active[key]--
	}
}

func (t *requestTracker) activeCount(backend routing.Backend) int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.active[keyForBackend(backend)]
}

func keyForBackend(backend routing.Backend) backendKey {
	return backendKey{namespace: backend.Namespace, name: backend.Name}
}

type rateLimiter struct {
	mu      sync.Mutex
	buckets map[backendKey]*tokenBucket
}

type tokenBucket struct {
	requestsPerMinute int
	capacity          float64
	tokens            float64
	last              time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{buckets: make(map[backendKey]*tokenBucket)}
}

func (l *rateLimiter) allow(key backendKey, policy routing.RateLimitPolicy) (bool, int) {
	if l == nil || policy.RequestsPerMinute <= 0 {
		return true, 0
	}
	burst := policy.Burst
	if burst <= 0 {
		burst = policy.RequestsPerMinute
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	bucket := l.buckets[key]
	if bucket == nil || bucket.requestsPerMinute != policy.RequestsPerMinute || bucket.capacity != float64(burst) {
		bucket = &tokenBucket{
			requestsPerMinute: policy.RequestsPerMinute,
			capacity:          float64(burst),
			tokens:            float64(burst),
			last:              now,
		}
		l.buckets[key] = bucket
	}
	ratePerSecond := float64(policy.RequestsPerMinute) / 60
	elapsed := now.Sub(bucket.last).Seconds()
	bucket.last = now
	bucket.tokens = math.Min(bucket.capacity, bucket.tokens+elapsed*ratePerSecond)
	if bucket.tokens >= 1 {
		bucket.tokens--
		return true, 0
	}
	retry := int(math.Ceil((1 - bucket.tokens) / ratePerSecond))
	if retry < 1 {
		retry = 1
	}
	return false, retry
}

type accessLogWriter struct {
	http.ResponseWriter
	status int
}

func (w *accessLogWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *accessLogWriter) Write(content []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(content)
}

func (w *accessLogWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *accessLogWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *accessLogWriter) ReadFrom(reader io.Reader) (int64, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return io.Copy(w.ResponseWriter, reader)
}

func (w *accessLogWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func cloneOptions(options Options) Options {
	if options.DefaultRateLimit != nil {
		rateLimit := *options.DefaultRateLimit
		options.DefaultRateLimit = &rateLimit
	}
	if options.RequestLoggingEnabled != nil {
		enabled := *options.RequestLoggingEnabled
		options.RequestLoggingEnabled = &enabled
	}
	return options
}

func joinURLPath(endpoint *url.URL, requestPath string) string {
	base := strings.TrimSuffix(endpoint.Path, "/")
	if requestPath == "" || requestPath == "/" {
		if base == "" {
			return "/"
		}
		return base + "/"
	}
	return base + "/" + strings.TrimPrefix(requestPath, "/")
}

func stateMessage(backend routing.Backend, fallback string) string {
	if strings.TrimSpace(backend.Message) != "" {
		return backend.Message
	}
	return fallback
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

func writeError(response http.ResponseWriter, status int, code, errorType, message string) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(errorEnvelope{
		Error: errorBody{
			Message: message,
			Type:    errorType,
			Code:    code,
		},
	})
}
