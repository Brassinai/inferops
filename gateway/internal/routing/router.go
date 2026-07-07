// Package routing contains the gateway's model registry and path matching.
package routing

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/brassinai/inferops/internal/routingpath"
)

// State describes whether a model can currently accept new requests.
type State string

const (
	StateInactive    State = "inactive"
	StateActivating  State = "activating"
	StateReady       State = "ready"
	StateUnavailable State = "unavailable"
	StateDraining    State = "draining"
)

// Backend is an immutable registry record for one externally addressable model.
type Backend struct {
	Name        string
	Namespace   string
	RoutePrefix string
	Endpoint    *url.URL
	State       State
	Message     string
}

// Registry resolves an external request path to a model backend and upstream
// path. Implementations must be safe for concurrent proxy and discovery use.
type Registry interface {
	Lookup(requestPath string) (Backend, string, bool)
}

// ReplacingRegistry atomically replaces the complete discovered model set.
type ReplacingRegistry interface {
	Registry
	Replace(backends []Backend) error
	MarkReadyUnavailable(message string)
}

// SnapshotRegistry exposes the current backend snapshot for status endpoints.
type SnapshotRegistry interface {
	Backends() []Backend
}

// MemoryRegistry is a concurrency-safe fake registry. It is used by tests and
// local gateway development, and is also the snapshot target for discovery.
type MemoryRegistry struct {
	mu       sync.RWMutex
	routes   map[string]Backend
	prefixes []string
}

// NewMemoryRegistry creates an empty fake registry.
func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{routes: make(map[string]Backend)}
}

// DefaultRoutePrefix returns the stable external route for a model name.
func DefaultRoutePrefix(model string) string {
	return routingpath.DefaultModelRoute(model)
}

// ParseModelPath parses /models/<name>/<upstream-path>. It rejects paths that
// could accidentally select a neighboring model route.
func ParseModelPath(requestPath string) (model, upstreamPath string, ok bool) {
	if !strings.HasPrefix(requestPath, routingpath.DefaultModelPrefix) {
		return "", "", false
	}
	remainder := strings.TrimPrefix(requestPath, routingpath.DefaultModelPrefix)
	separator := strings.IndexByte(remainder, '/')
	if separator <= 0 {
		return "", "", false
	}
	model = remainder[:separator]
	if model == "." || model == ".." {
		return "", "", false
	}
	return model, remainder[separator:], true
}

// Lookup resolves a path using longest-prefix matching with segment-boundary
// checks. The returned Backend does not share mutable URL state with callers.
func (r *MemoryRegistry) Lookup(requestPath string) (Backend, string, bool) {
	if r == nil {
		return Backend{}, "", false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, prefix := range r.prefixes {
		if requestPath != prefix && !strings.HasPrefix(requestPath, prefix+"/") {
			continue
		}
		backend := cloneBackend(r.routes[prefix])
		upstreamPath := strings.TrimPrefix(requestPath, prefix)
		if upstreamPath == "" {
			upstreamPath = "/"
		}
		return backend, upstreamPath, true
	}
	return Backend{}, "", false
}

// Upsert adds or updates one fake-registry backend.
func (r *MemoryRegistry) Upsert(backend Backend) error {
	if r == nil {
		return errors.New("registry is required")
	}
	normalized, err := normalizeBackend(backend)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, found := r.routes[normalized.RoutePrefix]; found && existing.Name != normalized.Name {
		return fmt.Errorf(
			"route prefix %q is already assigned to model %q",
			normalized.RoutePrefix,
			existing.Name,
		)
	}
	r.routes[normalized.RoutePrefix] = normalized
	r.rebuildPrefixes()
	return nil
}

// Delete removes a route from the fake registry.
func (r *MemoryRegistry) Delete(routePrefix string) {
	if r == nil {
		return
	}
	routePrefix = normalizeRoutePrefix(routePrefix)
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.routes, routePrefix)
	r.rebuildPrefixes()
}

// Backends returns the current discovered backends sorted by route prefix.
func (r *MemoryRegistry) Backends() []Backend {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	backends := make([]Backend, 0, len(r.prefixes))
	for _, prefix := range r.prefixes {
		backends = append(backends, cloneBackend(r.routes[prefix]))
	}
	return backends
}

// Replace atomically publishes a discovery snapshot. Ambiguous route prefixes
// are omitted rather than choosing a backend, while all unambiguous routes are
// still refreshed so lifecycle changes such as draining are never held back.
func (r *MemoryRegistry) Replace(backends []Backend) error {
	if r == nil {
		return errors.New("registry is required")
	}

	grouped := make(map[string][]Backend, len(backends))
	var errs []error
	for _, backend := range backends {
		normalized, err := normalizeBackend(backend)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		grouped[normalized.RoutePrefix] = append(grouped[normalized.RoutePrefix], normalized)
	}

	next := make(map[string]Backend, len(grouped))
	for prefix, candidates := range grouped {
		if len(candidates) != 1 {
			names := make([]string, 0, len(candidates))
			for _, candidate := range candidates {
				names = append(names, candidate.Name)
			}
			sort.Strings(names)
			errs = append(errs, fmt.Errorf(
				"route prefix %q is ambiguous between models %s",
				prefix,
				strings.Join(names, ", "),
			))
			continue
		}
		next[prefix] = candidates[0]
	}

	r.mu.Lock()
	r.routes = next
	r.rebuildPrefixes()
	r.mu.Unlock()
	return errors.Join(errs...)
}

// MarkReadyUnavailable atomically stops new requests to every currently ready
// backend while preserving stable routes and already-unavailable lifecycle
// states. Discovery uses this to fail closed when its snapshot becomes stale.
func (r *MemoryRegistry) MarkReadyUnavailable(message string) {
	if r == nil {
		return
	}
	if strings.TrimSpace(message) == "" {
		message = "model registry is unavailable"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for prefix, backend := range r.routes {
		if backend.State != StateReady {
			continue
		}
		backend.State = StateUnavailable
		backend.Endpoint = nil
		backend.Message = message
		r.routes[prefix] = backend
	}
}

func (r *MemoryRegistry) rebuildPrefixes() {
	r.prefixes = r.prefixes[:0]
	for prefix := range r.routes {
		r.prefixes = append(r.prefixes, prefix)
	}
	sort.Slice(r.prefixes, func(i, j int) bool {
		if len(r.prefixes[i]) == len(r.prefixes[j]) {
			return r.prefixes[i] < r.prefixes[j]
		}
		return len(r.prefixes[i]) > len(r.prefixes[j])
	})
}

func normalizeBackend(backend Backend) (Backend, error) {
	backend.Name = strings.TrimSpace(backend.Name)
	if backend.Name == "" {
		return Backend{}, errors.New("backend model name is required")
	}
	if strings.Contains(backend.Name, "/") {
		return Backend{}, fmt.Errorf("backend model name %q must be one path segment", backend.Name)
	}
	if backend.RoutePrefix == "" {
		backend.RoutePrefix = DefaultRoutePrefix(backend.Name)
	}
	normalizedPrefix, err := routingpath.Normalize(backend.RoutePrefix)
	if err != nil {
		return Backend{}, fmt.Errorf("model %q has invalid route prefix: %w", backend.Name, err)
	}
	backend.RoutePrefix = normalizedPrefix
	switch backend.State {
	case StateInactive, StateActivating, StateReady, StateUnavailable, StateDraining:
	default:
		return Backend{}, fmt.Errorf("model %q has invalid state %q", backend.Name, backend.State)
	}
	if backend.State == StateReady {
		if backend.Endpoint == nil || backend.Endpoint.Scheme == "" || backend.Endpoint.Host == "" {
			return Backend{}, fmt.Errorf("ready model %q requires an absolute backend endpoint", backend.Name)
		}
		if backend.Endpoint.Scheme != "http" && backend.Endpoint.Scheme != "https" {
			return Backend{}, fmt.Errorf("model %q endpoint scheme must be http or https", backend.Name)
		}
	}
	return cloneBackend(backend), nil
}

func normalizeRoutePrefix(prefix string) string {
	normalized, err := routingpath.Normalize(prefix)
	if err != nil {
		return ""
	}
	return normalized
}

func cloneBackend(backend Backend) Backend {
	if backend.Endpoint != nil {
		endpoint := *backend.Endpoint
		backend.Endpoint = &endpoint
	}
	return backend
}
