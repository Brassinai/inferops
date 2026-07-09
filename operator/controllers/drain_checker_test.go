package controllers

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestHTTPDrainCheckerReportsCompletion(t *testing.T) {
	t.Parallel()

	seenRequest := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		seenRequest <- request.URL.Path + "?" + request.URL.RawQuery
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"backends":[{"drainComplete":true}]}`))
	}))
	defer server.Close()

	checker, err := NewHTTPDrainChecker(server.URL + "/drainz")
	if err != nil {
		t.Fatalf("NewHTTPDrainChecker() error = %v", err)
	}
	complete, err := checker.DrainComplete(context.Background(), "inferops", "qwen")
	if err != nil {
		t.Fatalf("DrainComplete() error = %v", err)
	}
	if !complete {
		t.Fatal("DrainComplete() = false, want true")
	}
	got := <-seenRequest
	if got != "/drainz?model=qwen&namespace=inferops" {
		t.Fatalf("request = %q, want /drainz?model=qwen&namespace=inferops", got)
	}
}

func TestHTTPDrainCheckerRejectsAmbiguousResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"backends":[]}`))
	}))
	defer server.Close()

	checker, err := NewHTTPDrainChecker(server.URL + "/drainz")
	if err != nil {
		t.Fatalf("NewHTTPDrainChecker() error = %v", err)
	}
	if _, err := checker.DrainComplete(context.Background(), "inferops", "qwen"); err == nil {
		t.Fatal("DrainComplete() expected an error for an ambiguous response")
	}
}

func TestHTTPDrainCheckerSendsBearerToken(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer drain-token" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"backends":[{"drainComplete":true}]}`))
	}))
	defer server.Close()
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("drain-token\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	checker, err := NewHTTPDrainCheckerWithTokenFile(server.URL+"/drainz", tokenPath)
	if err != nil {
		t.Fatalf("NewHTTPDrainCheckerWithTokenFile() error = %v", err)
	}
	complete, err := checker.DrainComplete(context.Background(), "inferops", "qwen")
	if err != nil {
		t.Fatalf("DrainComplete() error = %v", err)
	}
	if !complete {
		t.Fatal("DrainComplete() = false, want true")
	}
}

func TestEndpointSliceDrainCheckerQueriesReadyGatewayEndpoints(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/drainz" {
			t.Fatalf("path = %q, want /drainz", request.URL.Path)
		}
		if request.URL.Query().Get("namespace") != "inferops" ||
			request.URL.Query().Get("model") != "qwen" {
			t.Fatalf("query = %q, want namespace/model filters", request.URL.RawQuery)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"backends":[{"drainComplete":true}]}`))
	}))
	defer server.Close()

	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	_, portValue, err := net.SplitHostPort(serverURL.Host)
	if err != nil {
		t.Fatalf("split server host: %v", err)
	}
	port, err := strconv.Atoi(portValue)
	if err != nil {
		t.Fatalf("parse server port: %v", err)
	}

	kubernetesClient := fake.NewClientBuilder().
		WithScheme(drainCheckerScheme(t)).
		WithObjects(gatewayEndpointSlice(int32(port), true)).
		Build()
	checker, err := NewEndpointSliceDrainChecker(kubernetesClient, EndpointSliceDrainCheckerConfig{
		Namespace:   "inferops-system",
		ServiceName: "inferops-gateway",
		Scheme:      "http",
		Port:        int32(port),
	})
	if err != nil {
		t.Fatalf("NewEndpointSliceDrainChecker() error = %v", err)
	}
	complete, err := checker.DrainComplete(context.Background(), "inferops", "qwen")
	if err != nil {
		t.Fatalf("DrainComplete() error = %v", err)
	}
	if !complete {
		t.Fatal("DrainComplete() = false, want true")
	}
}

func TestEndpointSliceDrainCheckerRequiresEveryGatewayEndpointComplete(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"backends":[{"drainComplete":false}]}`))
	}))
	defer server.Close()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	_, portValue, err := net.SplitHostPort(serverURL.Host)
	if err != nil {
		t.Fatalf("split server host: %v", err)
	}
	port, err := strconv.Atoi(portValue)
	if err != nil {
		t.Fatalf("parse server port: %v", err)
	}

	kubernetesClient := fake.NewClientBuilder().
		WithScheme(drainCheckerScheme(t)).
		WithObjects(gatewayEndpointSlice(int32(port), true)).
		Build()
	checker, err := NewEndpointSliceDrainChecker(kubernetesClient, EndpointSliceDrainCheckerConfig{
		Namespace:   "inferops-system",
		ServiceName: "inferops-gateway",
		Scheme:      "http",
		Port:        int32(port),
	})
	if err != nil {
		t.Fatalf("NewEndpointSliceDrainChecker() error = %v", err)
	}
	complete, err := checker.DrainComplete(context.Background(), "inferops", "qwen")
	if err != nil {
		t.Fatalf("DrainComplete() error = %v", err)
	}
	if complete {
		t.Fatal("DrainComplete() = true, want false")
	}
}

func drainCheckerScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := discoveryv1.AddToScheme(scheme); err != nil {
		t.Fatalf("register discovery scheme: %v", err)
	}
	return scheme
}

func gatewayEndpointSlice(port int32, ready bool) *discoveryv1.EndpointSlice {
	return &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "inferops-gateway-test",
			Namespace: "inferops-system",
			Labels: map[string]string{
				discoveryv1.LabelServiceName: "inferops-gateway",
			},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Ports: []discoveryv1.EndpointPort{{
			Port: &port,
		}},
		Endpoints: []discoveryv1.Endpoint{{
			Addresses: []string{"127.0.0.1"},
			Conditions: discoveryv1.EndpointConditions{
				Ready: &ready,
			},
		}},
	}
}
