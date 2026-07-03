package routing

import (
	"net/url"
	"testing"
)

func TestParseModelPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		path         string
		wantModel    string
		wantUpstream string
		wantOK       bool
	}{
		{name: "chat completions", path: "/models/qwen-chat/v1/chat/completions", wantModel: "qwen-chat", wantUpstream: "/v1/chat/completions", wantOK: true},
		{name: "model root has no upstream", path: "/models/qwen-chat", wantOK: false},
		{name: "empty model", path: "/models//v1/models", wantOK: false},
		{name: "neighboring prefix", path: "/models-extra/qwen/v1/models", wantOK: false},
		{name: "traversal", path: "/models/../v1/models", wantOK: false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			model, upstream, ok := ParseModelPath(test.path)
			if model != test.wantModel || upstream != test.wantUpstream || ok != test.wantOK {
				t.Fatalf(
					"ParseModelPath(%q) = (%q, %q, %t), want (%q, %q, %t)",
					test.path,
					model,
					upstream,
					ok,
					test.wantModel,
					test.wantUpstream,
					test.wantOK,
				)
			}
		})
	}
}

func TestMemoryRegistryLookupUsesPathBoundariesAndLongestPrefix(t *testing.T) {
	t.Parallel()
	registry := NewMemoryRegistry()
	endpoint := mustURL(t, "http://runtime.test:8000")
	for _, backend := range []Backend{
		{Name: "qwen", RoutePrefix: "/models/qwen", State: StateReady, Endpoint: endpoint},
		{Name: "admin", RoutePrefix: "/models/qwen/admin", State: StateReady, Endpoint: endpoint},
	} {
		if err := registry.Upsert(backend); err != nil {
			t.Fatalf("Upsert() error = %v", err)
		}
	}

	tests := []struct {
		path         string
		wantName     string
		wantUpstream string
		wantFound    bool
	}{
		{path: "/models/qwen/v1/models", wantName: "qwen", wantUpstream: "/v1/models", wantFound: true},
		{path: "/models/qwen/admin/v1/models", wantName: "admin", wantUpstream: "/v1/models", wantFound: true},
		{path: "/models/qwen-extra/v1/models", wantFound: false},
		{path: "/models/other/v1/models", wantFound: false},
	}
	for _, test := range tests {
		backend, upstream, found := registry.Lookup(test.path)
		if backend.Name != test.wantName || upstream != test.wantUpstream || found != test.wantFound {
			t.Errorf(
				"Lookup(%q) = (%q, %q, %t), want (%q, %q, %t)",
				test.path,
				backend.Name,
				upstream,
				found,
				test.wantName,
				test.wantUpstream,
				test.wantFound,
			)
		}
	}
}

func TestMemoryRegistryReplaceOmitsAmbiguousRoutesAndRefreshesOthers(t *testing.T) {
	t.Parallel()
	registry := NewMemoryRegistry()
	if err := registry.Upsert(Backend{Name: "old", State: StateInactive}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	err := registry.Replace([]Backend{
		{Name: "first", RoutePrefix: "/shared", State: StateReady, Endpoint: mustURL(t, "http://first.test")},
		{Name: "second", RoutePrefix: "/shared", State: StateReady, Endpoint: mustURL(t, "http://second.test")},
		{Name: "safe", State: StateDraining},
	})
	if err == nil {
		t.Fatal("Replace() error = nil, want ambiguous-route error")
	}
	if _, _, found := registry.Lookup("/shared/v1/models"); found {
		t.Fatal("ambiguous route was published")
	}
	backend, _, found := registry.Lookup("/models/safe/v1/models")
	if !found || backend.State != StateDraining {
		t.Fatalf("safe route = (%+v, %t), want draining route", backend, found)
	}
	if _, _, found := registry.Lookup("/models/old/v1/models"); found {
		t.Fatal("stale route remained after snapshot replacement")
	}
}

func TestMemoryRegistryRejectsReadyBackendWithoutEndpoint(t *testing.T) {
	t.Parallel()
	registry := NewMemoryRegistry()
	if err := registry.Upsert(Backend{Name: "qwen", State: StateReady}); err == nil {
		t.Fatal("Upsert() error = nil, want missing endpoint error")
	}
}

func TestMemoryRegistryRejectsUnsafeRoutePrefixes(t *testing.T) {
	t.Parallel()
	tests := []Backend{
		{Name: "qwen", RoutePrefix: "/", State: StateInactive},
		{Name: "qwen", RoutePrefix: "/readyz", State: StateInactive},
		{Name: "qwen", RoutePrefix: "/models/../readyz", State: StateInactive},
		{Name: "qwen", RoutePrefix: "/models/qwen%2fother", State: StateInactive},
		{Name: "qwen", RoutePrefix: "/models/qwen?other", State: StateInactive},
		{Name: "qwen", RoutePrefix: "/models/qwen\\other", State: StateInactive},
		{Name: "qwen/other", State: StateInactive},
	}
	for _, backend := range tests {
		registry := NewMemoryRegistry()
		if err := registry.Upsert(backend); err == nil {
			t.Errorf("Upsert(%+v) error = nil, want unsafe route error", backend)
		}
	}
}

func TestMemoryRegistryMarkReadyUnavailable(t *testing.T) {
	t.Parallel()
	registry := NewMemoryRegistry()
	if err := registry.Replace([]Backend{
		{Name: "ready", State: StateReady, Endpoint: mustURL(t, "http://ready.test")},
		{Name: "inactive", State: StateInactive},
	}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}

	registry.MarkReadyUnavailable("discovery is stale")
	ready, _, found := registry.Lookup("/models/ready/v1/models")
	if !found || ready.State != StateUnavailable || ready.Endpoint != nil || ready.Message != "discovery is stale" {
		t.Fatalf("ready backend after fail-close = (%+v, %t)", ready, found)
	}
	inactive, _, found := registry.Lookup("/models/inactive/v1/models")
	if !found || inactive.State != StateInactive {
		t.Fatalf("inactive backend after fail-close = (%+v, %t)", inactive, found)
	}
}

func mustURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", value, err)
	}
	return parsed
}
