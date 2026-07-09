package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGatewayHandlerDoesNotCanonicalizeProxyPaths(t *testing.T) {
	t.Parallel()
	healthRequests := 0
	proxyPaths := make(chan string, 1)
	handler := gatewayHandler(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			healthRequests++
		}),
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
			proxyPaths <- request.URL.Path
		}),
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	)

	handler.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/models/qwen/v1/../metrics", nil),
	)
	select {
	case got := <-proxyPaths:
		if got != "/models/qwen/v1/../metrics" {
			t.Fatalf("proxy path = %q, want original non-canonical path", got)
		}
	default:
		t.Fatal("non-canonical path was not passed to the rejecting proxy")
	}
	if healthRequests != 0 {
		t.Fatalf("health handler received %d proxy requests", healthRequests)
	}
}

func TestReadinessIncludesAuthenticationSource(t *testing.T) {
	t.Parallel()
	source := &readinessTokenSource{tokens: []string{"token"}}
	ready := readinessWithTokenSource(func() bool { return true }, source)
	if !ready() {
		t.Fatal("readiness = false with discovery and auth ready")
	}
	source.err = errors.New("secret unavailable")
	if ready() {
		t.Fatal("readiness = true with auth unavailable")
	}
	source.err = nil
	ready = readinessWithTokenSource(func() bool { return false }, source)
	if ready() {
		t.Fatal("readiness = true with discovery unavailable")
	}
}

func TestProxyOptionsFromEnvironment(t *testing.T) {
	t.Setenv("INFEROPS_GATEWAY_RATE_LIMIT_RPM", "120")
	t.Setenv("INFEROPS_GATEWAY_RATE_LIMIT_BURST", "20")
	t.Setenv("INFEROPS_GATEWAY_REQUEST_LOGGING", "true")

	options, err := proxyOptionsFromEnvironment()
	if err != nil {
		t.Fatalf("proxyOptionsFromEnvironment() error = %v", err)
	}
	if options.DefaultRateLimit == nil ||
		options.DefaultRateLimit.RequestsPerMinute != 120 ||
		options.DefaultRateLimit.Burst != 20 {
		t.Fatalf("DefaultRateLimit = %#v, want 120 rpm burst 20", options.DefaultRateLimit)
	}
	if options.RequestLoggingEnabled == nil || !*options.RequestLoggingEnabled {
		t.Fatalf("RequestLoggingEnabled = %#v, want true", options.RequestLoggingEnabled)
	}
}

func TestProxyOptionsRejectBurstWithoutRate(t *testing.T) {
	t.Setenv("INFEROPS_GATEWAY_RATE_LIMIT_BURST", "20")
	if _, err := proxyOptionsFromEnvironment(); err == nil {
		t.Fatal("proxyOptionsFromEnvironment() error = nil, want burst without rpm error")
	}
}

type readinessTokenSource struct {
	tokens []string
	err    error
}

func (s *readinessTokenSource) Tokens() ([]string, error) {
	return s.tokens, s.err
}

func TestGatewayHandlerReservesExactHealthPaths(t *testing.T) {
	t.Parallel()
	healthRequests := 0
	metricsRequests := 0
	proxyRequests := 0
	handler := gatewayHandler(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			healthRequests++
		}),
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			metricsRequests++
		}),
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			proxyRequests++
		}),
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	)

	for _, requestPath := range []string{"/healthz", "/readyz"} {
		handler.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, requestPath, nil),
		)
	}
	handler.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/metrics", nil),
	)
	handler.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/healthz/model", nil),
	)

	if healthRequests != 2 || metricsRequests != 1 || proxyRequests != 1 {
		t.Fatalf(
			"health requests = %d, metrics requests = %d, proxy requests = %d; want 2, 1, and 1",
			healthRequests,
			metricsRequests,
			proxyRequests,
		)
	}
}

func TestGatewayHandlerReservesExactDrainPath(t *testing.T) {
	t.Parallel()
	drainRequests := 0
	proxyRequests := 0
	handler := gatewayHandler(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			proxyRequests++
		}),
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			drainRequests++
		}),
	)

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/drainz", nil))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/drainz/model", nil))

	if drainRequests != 1 || proxyRequests != 1 {
		t.Fatalf("drain requests = %d, proxy requests = %d; want 1 and 1", drainRequests, proxyRequests)
	}
}

func TestGatewayHandlerReservesExactMetricsPath(t *testing.T) {
	t.Parallel()
	metricsRequests := 0
	proxyRequests := 0
	handler := gatewayHandler(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			metricsRequests++
		}),
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			proxyRequests++
		}),
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	)

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/metrics", nil))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/metrics/model", nil))

	if metricsRequests != 1 || proxyRequests != 1 {
		t.Fatalf("metrics requests = %d, proxy requests = %d; want 1 and 1", metricsRequests, proxyRequests)
	}
}
