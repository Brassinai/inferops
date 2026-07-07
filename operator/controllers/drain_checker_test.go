package controllers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
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
