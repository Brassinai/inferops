package metrics

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecorderCapturesRequestMetrics(t *testing.T) {
	t.Parallel()
	registry := prometheus.NewRegistry()
	recorder, err := NewRecorder(registry)
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	handler := recorder.Middleware(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		if got := testutil.ToFloat64(recorder.activeRequests); got != 1 {
			t.Errorf("active requests = %v, want 1", got)
		}
		response.WriteHeader(http.StatusCreated)
	}))
	handler.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/models/test/v1/completions", nil),
	)

	if got := testutil.ToFloat64(recorder.activeRequests); got != 0 {
		t.Errorf("active requests after response = %v, want 0", got)
	}
	if got := testutil.ToFloat64(recorder.requests.WithLabelValues("POST", "201")); got != 1 {
		t.Errorf("request count = %v, want 1", got)
	}
	if got := testutil.CollectAndCount(recorder.latency); got != 1 {
		t.Errorf("latency collector count = %d, want 1", got)
	}
}

func TestRecorderLabelsCanceledRequestAsClientClosed(t *testing.T) {
	t.Parallel()
	registry := prometheus.NewRegistry()
	recorder, err := NewRecorder(registry)
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	handler := recorder.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	handler.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx),
	)
	if got := testutil.ToFloat64(recorder.requests.WithLabelValues("POST", "499")); got != 1 {
		t.Errorf("canceled request count = %v, want 1", got)
	}
}

func TestRecorderBoundsLabelsAndCountsUpstreamErrors(t *testing.T) {
	t.Parallel()
	registry := prometheus.NewRegistry()
	recorder, err := NewRecorder(registry)
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	handler := recorder.Middleware(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(response, "ok")
	}))
	handler.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest("CUSTOM-"+t.Name(), "/", nil),
	)
	recorder.ObserveUpstreamError("transport")
	recorder.ObserveUpstreamError("unbounded-" + t.Name())

	if got := testutil.ToFloat64(recorder.requests.WithLabelValues("OTHER", "200")); got != 1 {
		t.Errorf("OTHER request count = %v, want 1", got)
	}
	if got := testutil.ToFloat64(recorder.upstreamErrors.WithLabelValues("transport")); got != 1 {
		t.Errorf("transport errors = %v, want 1", got)
	}
	if got := testutil.ToFloat64(recorder.upstreamErrors.WithLabelValues("other")); got != 1 {
		t.Errorf("other errors = %v, want 1", got)
	}
}

func TestMiddlewarePreservesStreamingFlushes(t *testing.T) {
	t.Parallel()
	registry := prometheus.NewRegistry()
	recorder, err := NewRecorder(registry)
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	server := httptest.NewServer(recorder.Middleware(http.HandlerFunc(
		func(response http.ResponseWriter, _ *http.Request) {
			flusher, ok := response.(http.Flusher)
			if !ok {
				t.Error("wrapped ResponseWriter does not implement http.Flusher")
				return
			}
			_, _ = io.WriteString(response, "data: first\n\n")
			flusher.Flush()
			<-release
			_, _ = io.WriteString(response, "data: second\n\n")
			flusher.Flush()
		},
	)))
	defer server.Close()

	response, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	firstLine := make(chan string, 1)
	go func() {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			firstLine <- "error: " + readErr.Error()
			return
		}
		firstLine <- line
	}()
	select {
	case line := <-firstLine:
		if line != "data: first\n" {
			t.Fatalf("first streamed line = %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("metrics middleware buffered the first stream chunk")
	}
	releaseOnce.Do(func() { close(release) })
	remaining, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read remaining stream: %v", err)
	}
	if !strings.Contains(string(remaining), "data: second") {
		t.Errorf("remaining stream = %q, want second event", remaining)
	}
}
