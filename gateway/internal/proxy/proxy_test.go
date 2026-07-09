package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brassinai/inferops/gateway/internal/routing"
)

func TestProxyForwardsOpenAIRequest(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", request.Method)
		}
		if request.URL.Path != "/runtime/v1/chat/completions" {
			t.Errorf("path = %q, want /runtime/v1/chat/completions", request.URL.Path)
		}
		if request.URL.Query().Get("trace") != "yes" {
			t.Errorf("query = %q, want trace=yes", request.URL.RawQuery)
		}
		if request.Header.Get("X-Test") != "preserved" {
			t.Errorf("X-Test = %q, want preserved", request.Header.Get("X-Test"))
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		if string(body) != `{"model":"qwen"}` {
			t.Errorf("body = %q", body)
		}
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusCreated)
		_, _ = response.Write([]byte(`{"id":"completion-1"}`))
	}))
	defer upstream.Close()

	endpoint := parseURL(t, upstream.URL+"/runtime")
	handler := newTestProxy(t, routing.Backend{
		Name:     "qwen",
		State:    routing.StateReady,
		Endpoint: endpoint,
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"http://gateway.test/models/qwen/v1/chat/completions?trace=yes",
		strings.NewReader(`{"model":"qwen"}`),
	)
	request.Header.Set("X-Test", "preserved")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	if response.Body.String() != `{"id":"completion-1"}` {
		t.Errorf("body = %q", response.Body.String())
	}
	if got := response.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", got)
	}
}

func TestProxyStreamsWithoutBuffering(t *testing.T) {
	t.Parallel()
	releaseSecondChunk := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			close(releaseSecondChunk)
		})
	}
	defer release()
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		flusher, ok := response.(http.Flusher)
		if !ok {
			t.Error("upstream ResponseWriter does not implement http.Flusher")
			return
		}
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(response, "data: first\n\n")
		flusher.Flush()
		<-releaseSecondChunk
		_, _ = io.WriteString(response, "data: second\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	gateway := httptest.NewServer(newTestProxy(t, routing.Backend{
		Name:     "qwen",
		State:    routing.StateReady,
		Endpoint: parseURL(t, upstream.URL),
	}))
	defer gateway.Close()

	response, err := gateway.Client().Get(gateway.URL + "/models/qwen/v1/chat/completions")
	if err != nil {
		t.Fatalf("GET gateway: %v", err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	firstChunk := make(chan string, 1)
	go func() {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			firstChunk <- "error: " + readErr.Error()
			return
		}
		firstChunk <- line
	}()
	select {
	case line := <-firstChunk:
		if line != "data: first\n" {
			t.Fatalf("first streamed line = %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first chunk was buffered until the upstream response completed")
	}
	release()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read remaining stream: %v", err)
	}
	if !strings.Contains(string(body), "data: second") {
		t.Fatalf("remaining stream = %q, want second event", body)
	}
}

func TestProxyTracksActiveRequestsAndReportsDrainCompletion(t *testing.T) {
	t.Parallel()
	upstreamStarted := make(chan struct{})
	releaseUpstream := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		close(upstreamStarted)
		<-releaseUpstream
		_, _ = io.WriteString(response, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	defer close(releaseUpstream)

	registry := routing.NewMemoryRegistry()
	readyBackend := routing.Backend{
		Namespace: "inferops",
		Name:      "qwen",
		State:     routing.StateReady,
		Endpoint:  parseURL(t, upstream.URL),
	}
	if err := registry.Upsert(readyBackend); err != nil {
		t.Fatalf("registry.Upsert ready: %v", err)
	}
	handler, err := New(registry)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	firstResponseDone := make(chan struct{})
	go func() {
		defer close(firstResponseDone)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/models/qwen/v1/completions", nil))
	}()
	select {
	case <-upstreamStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first upstream request did not start")
	}
	assertDrainStatus(t, handler, "qwen", 1, false)

	drainingBackend := readyBackend
	drainingBackend.State = routing.StateDraining
	drainingBackend.Endpoint = nil
	drainingBackend.Message = "model is draining"
	if err := registry.Upsert(drainingBackend); err != nil {
		t.Fatalf("registry.Upsert draining: %v", err)
	}
	rejected := httptest.NewRecorder()
	handler.ServeHTTP(rejected, httptest.NewRequest(http.MethodPost, "/models/qwen/v1/completions", nil))
	assertAPIError(t, rejected, http.StatusServiceUnavailable, "model_draining")
	assertDrainStatus(t, handler, "qwen", 1, false)

	releaseUpstream <- struct{}{}
	select {
	case <-firstResponseDone:
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not complete")
	}
	assertDrainStatus(t, handler, "qwen", 0, true)
}

func TestProxyRoutesToLeastLoadedReadyBackend(t *testing.T) {
	t.Parallel()
	stableStarted := make(chan struct{})
	releaseStable := make(chan struct{})
	stable := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		close(stableStarted)
		<-releaseStable
		_, _ = response.Write([]byte("stable"))
	}))
	defer stable.Close()
	defer close(releaseStable)
	canary := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte("canary"))
	}))
	defer canary.Close()

	registry := routing.NewMemoryRegistry()
	if err := registry.Replace([]routing.Backend{
		{Name: "a-stable", RoutePrefix: "/models/qwen", State: routing.StateReady, Endpoint: parseURL(t, stable.URL)},
		{Name: "b-canary", RoutePrefix: "/models/qwen", State: routing.StateReady, Endpoint: parseURL(t, canary.URL)},
	}); err != nil {
		t.Fatalf("registry.Replace(): %v", err)
	}
	handler, err := New(registry)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/models/qwen/v1/chat/completions", nil))
	}()
	select {
	case <-stableStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("stable request did not start")
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/models/qwen/v1/chat/completions", nil))
	if response.Code != http.StatusOK || response.Body.String() != "canary" {
		t.Fatalf("second response = %d %q, want canary", response.Code, response.Body.String())
	}

	releaseStable <- struct{}{}
	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("stable request did not complete")
	}
}

func TestProxyAppliesBackendRateLimit(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte("ok"))
	}))
	defer upstream.Close()
	rpm := 1
	handler := newTestProxy(t, routing.Backend{
		Name:     "qwen",
		State:    routing.StateReady,
		Endpoint: parseURL(t, upstream.URL),
		Policy: routing.TrafficPolicy{
			RateLimit: &routing.RateLimitPolicy{RequestsPerMinute: rpm, Burst: 1},
		},
	})

	allowed := httptest.NewRecorder()
	handler.ServeHTTP(allowed, httptest.NewRequest(http.MethodPost, "/models/qwen/v1/completions", nil))
	if allowed.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200", allowed.Code)
	}
	limited := httptest.NewRecorder()
	handler.ServeHTTP(limited, httptest.NewRequest(http.MethodPost, "/models/qwen/v1/completions", nil))
	assertAPIError(t, limited, http.StatusTooManyRequests, "rate_limit_exceeded")
	if limited.Header().Get("Retry-After") == "" {
		t.Fatal("rate-limited response omitted Retry-After")
	}
}

func TestProxyWritesSanitizedRequestLogWhenEnabled(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte("ok"))
	}))
	defer upstream.Close()
	enabled := true
	registry := routing.NewMemoryRegistry()
	if err := registry.Upsert(routing.Backend{
		Namespace: "inferops",
		Name:      "qwen",
		State:     routing.StateReady,
		Endpoint:  parseURL(t, upstream.URL),
		Policy: routing.TrafficPolicy{
			RequestLogging: &routing.RequestLoggingPolicy{Enabled: &enabled},
		},
	}); err != nil {
		t.Fatalf("registry.Upsert(): %v", err)
	}
	handler, err := NewWithMetricsAndOptions(registry, nil, Options{
		RequestLogger: log.New(&logs, "", 0),
	})
	if err != nil {
		t.Fatalf("NewWithMetricsAndOptions(): %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/models/qwen/v1/completions", strings.NewReader(`{"secret":"body"}`))
	request.Header.Set("Authorization", "Bearer sensitive")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	line := logs.String()
	for _, want := range []string{`model="qwen"`, `namespace="inferops"`, `status=200`, `path="/models/qwen/v1/completions"`} {
		if !strings.Contains(line, want) {
			t.Fatalf("request log %q does not contain %s", line, want)
		}
	}
	if strings.Contains(line, "sensitive") || strings.Contains(line, "secret") {
		t.Fatalf("request log included sensitive request data: %q", line)
	}
}

func TestProxyPropagatesRequestCancellation(t *testing.T) {
	t.Parallel()
	upstreamStarted := make(chan struct{})
	upstreamCanceled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(upstreamStarted)
		<-request.Context().Done()
		close(upstreamCanceled)
	}))
	defer upstream.Close()

	handler := newTestProxy(t, routing.Backend{
		Name:     "qwen",
		State:    routing.StateReady,
		Endpoint: parseURL(t, upstream.URL),
	})
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "/models/qwen/v1/completions", nil).WithContext(ctx)
	handlerReturned := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), request)
		close(handlerReturned)
	}()
	select {
	case <-upstreamStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream request did not start")
	}
	cancel()
	select {
	case <-upstreamCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream request context was not canceled")
	}
	select {
	case <-handlerReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("proxy handler did not return after cancellation")
	}
}

func TestProxyReturnsOpenAIErrorForUpstreamFailure(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	endpoint := &url.URL{Scheme: "http", Host: listener.Addr().String()}
	_ = listener.Close()

	handler := newTestProxy(t, routing.Backend{
		Name:     "qwen",
		State:    routing.StateReady,
		Endpoint: endpoint,
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/models/qwen/v1/completions", nil))
	assertAPIError(t, response, http.StatusBadGateway, "upstream_error")
}

func TestProxyRecordsBoundedUpstreamFailureReasons(t *testing.T) {
	t.Parallel()
	recorder := &recordingMetrics{counts: make(map[string]int)}
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusServiceUnavailable)
	}))
	endpoint := parseURL(t, upstream.URL)
	registry := routing.NewMemoryRegistry()
	if err := registry.Upsert(routing.Backend{
		Name:     "qwen",
		State:    routing.StateReady,
		Endpoint: endpoint,
	}); err != nil {
		t.Fatalf("registry.Upsert(): %v", err)
	}
	handler, err := NewWithMetrics(registry, recorder)
	if err != nil {
		t.Fatalf("NewWithMetrics(): %v", err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/models/qwen/v1/completions", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("upstream response status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	upstream.Close()
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/models/qwen/v1/completions", nil))
	assertAPIError(t, response, http.StatusBadGateway, "upstream_error")

	if got := recorder.counts["response_5xx"]; got != 1 {
		t.Errorf("response_5xx count = %d, want 1", got)
	}
	if got := recorder.counts["transport"]; got != 1 {
		t.Errorf("transport count = %d, want 1", got)
	}
}

func TestProxyRejectsLifecycleStatesWithoutReachingUpstream(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer upstream.Close()

	tests := []struct {
		name       string
		backend    routing.Backend
		path       string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "unknown",
			backend:    routing.Backend{Name: "other", State: routing.StateReady, Endpoint: parseURL(t, upstream.URL)},
			path:       "/models/missing/v1/models",
			wantStatus: http.StatusNotFound,
			wantCode:   "model_not_found",
		},
		{
			name:       "inactive",
			backend:    routing.Backend{Name: "qwen", State: routing.StateInactive},
			path:       "/models/qwen/v1/models",
			wantStatus: http.StatusConflict,
			wantCode:   "model_inactive",
		},
		{
			name:       "activating",
			backend:    routing.Backend{Name: "qwen", State: routing.StateActivating},
			path:       "/models/qwen/v1/models",
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "model_activating",
		},
		{
			name:       "draining",
			backend:    routing.Backend{Name: "qwen", State: routing.StateDraining},
			path:       "/models/qwen/v1/models",
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "model_draining",
		},
		{
			name:       "unavailable",
			backend:    routing.Backend{Name: "qwen", State: routing.StateUnavailable},
			path:       "/models/qwen/v1/models",
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "model_unavailable",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			newTestProxy(t, test.backend).ServeHTTP(
				response,
				httptest.NewRequest(http.MethodPost, test.path, nil),
			)
			assertAPIError(t, response, test.wantStatus, test.wantCode)
		})
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("rejected requests reached an unrelated upstream %d times", got)
	}
}

func TestProxyRejectsNonOpenAIPath(t *testing.T) {
	t.Parallel()
	handler := newTestProxy(t, routing.Backend{
		Name:     "qwen",
		State:    routing.StateReady,
		Endpoint: parseURL(t, "http://runtime.test"),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/models/qwen/metrics", nil))
	assertAPIError(t, response, http.StatusNotFound, "invalid_path")
}

func TestProxyRejectsAmbiguousPathsWithoutReachingUpstream(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer upstream.Close()
	handler := newTestProxy(t, routing.Backend{
		Name:     "qwen",
		State:    routing.StateReady,
		Endpoint: parseURL(t, upstream.URL),
	})

	for _, requestPath := range []string{
		"/models/qwen%2Fother/v1/models",
		"/models/qwen/v1/../metrics",
		"/models/qwen//v1/models",
		"/models/qwen\\other/v1/models",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, requestPath, nil))
		assertAPIError(t, response, http.StatusBadRequest, "invalid_path")
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("ambiguous paths reached upstream %d times", got)
	}
}

func newTestProxy(t *testing.T, backends ...routing.Backend) *Proxy {
	t.Helper()
	registry := routing.NewMemoryRegistry()
	for _, backend := range backends {
		if err := registry.Upsert(backend); err != nil {
			t.Fatalf("registry.Upsert(): %v", err)
		}
	}
	handler, err := New(registry)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	return handler
}

func assertDrainStatus(t *testing.T, handler *Proxy, model string, wantActive int, wantComplete bool) {
	t.Helper()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/drainz?namespace=inferops&model="+model, nil)
	handler.ServeDrainStatus(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("drain status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Backends []struct {
			Model          string `json:"model"`
			ActiveRequests int    `json:"activeRequests"`
			DrainComplete  bool   `json:"drainComplete"`
		} `json:"backends"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode drain status: %v", err)
	}
	if len(payload.Backends) != 1 {
		t.Fatalf("drain backends = %#v, want one", payload.Backends)
	}
	got := payload.Backends[0]
	if got.Model != model || got.ActiveRequests != wantActive || got.DrainComplete != wantComplete {
		t.Fatalf("drain status = %+v, want model=%s active=%d complete=%t",
			got, model, wantActive, wantComplete)
	}
}

func parseURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", value, err)
	}
	return parsed
}

func assertAPIError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, status, response.Body.String())
	}
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error response: %v; body=%s", err, response.Body.String())
	}
	if envelope.Error.Code != code {
		t.Errorf("error code = %q, want %q", envelope.Error.Code, code)
	}
	if envelope.Error.Message == "" || envelope.Error.Type == "" {
		t.Errorf("incomplete error response: %+v", envelope.Error)
	}
}

type recordingMetrics struct {
	counts map[string]int
}

func (r *recordingMetrics) ObserveUpstreamError(reason string) {
	r.counts[reason]++
}
