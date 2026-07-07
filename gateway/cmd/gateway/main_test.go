package main

import (
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

func TestGatewayHandlerReservesExactHealthPaths(t *testing.T) {
	t.Parallel()
	healthRequests := 0
	proxyRequests := 0
	handler := gatewayHandler(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			healthRequests++
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
		httptest.NewRequest(http.MethodGet, "/healthz/model", nil),
	)

	if healthRequests != 2 || proxyRequests != 1 {
		t.Fatalf("health requests = %d, proxy requests = %d; want 2 and 1", healthRequests, proxyRequests)
	}
}

func TestGatewayHandlerReservesExactDrainPath(t *testing.T) {
	t.Parallel()
	drainRequests := 0
	proxyRequests := 0
	handler := gatewayHandler(
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
