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
	Policy      TrafficPolicy
}

// RoutingStrategy controls how the gateway selects among ready backends that
// share one external route prefix.
type RoutingStrategy string

const (
	RoutingStrategyLeastLoaded RoutingStrategy = "LeastLoaded"
	RoutingStrategyWeighted    RoutingStrategy = "Weighted"
)

const DefaultTrafficWeight = 100

// TrafficPolicy is the gateway-facing subset of ModelDeployment routing
// policy. Nil fields inherit gateway defaults.
type TrafficPolicy struct {
	RoutingStrategy RoutingStrategy
	Weight          *int
	RateLimit       *RateLimitPolicy
	RequestLogging  *RequestLoggingPolicy
}

// RateLimitPolicy bounds requests admitted by one gateway process for one
// backend. A zero RequestsPerMinute disables local rate limiting.
type RateLimitPolicy struct {
	RequestsPerMinute int
	Burst             int
}

// RequestLoggingPolicy controls structured request logging for a backend.
type RequestLoggingPolicy struct {
	Enabled *bool
}

// Registry resolves an external request path to a model backend and upstream
// path. Implementations must be safe for concurrent proxy and discovery use.
type Registry interface {
	Lookup(requestPath string) (Backend, string, bool)
}

// SelectingRegistry resolves a route while considering live backend load.
type SelectingRegistry interface {
	Registry
	Select(requestPath string, load func(Backend) int) (Backend, string, bool)
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
	mu       sync.Mutex
	routes   map[string][]Backend
	prefixes []string
	rr       map[string]map[backendKey]int
}

// NewMemoryRegistry creates an empty fake registry.
func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{
		routes: make(map[string][]Backend),
		rr:     make(map[string]map[backendKey]int),
	}
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
	return r.Select(requestPath, nil)
}

// Select resolves a path and picks one backend. When a route has multiple
// ready candidates, least-loaded selection uses process-local active request
// counts and weighted round-robin only for ties. This keeps canaries respected
// without routing around a clearly less busy backend.
func (r *MemoryRegistry) Select(requestPath string, load func(Backend) int) (Backend, string, bool) {
	if r == nil {
		return Backend{}, "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, prefix := range r.prefixes {
		if requestPath != prefix && !strings.HasPrefix(requestPath, prefix+"/") {
			continue
		}
		backend := r.selectBackendLocked(prefix, load)
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
	candidates := r.routes[normalized.RoutePrefix]
	replaced := false
	for index, candidate := range candidates {
		if candidate.identity() == normalized.identity() {
			candidates[index] = normalized
			replaced = true
			break
		}
	}
	if !replaced {
		candidates = append(candidates, normalized)
	}
	sortBackends(candidates)
	r.routes[normalized.RoutePrefix] = candidates
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
	delete(r.rr, routePrefix)
	r.rebuildPrefixes()
}

// Backends returns the current discovered backends sorted by route prefix.
func (r *MemoryRegistry) Backends() []Backend {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var backends []Backend
	for _, prefix := range r.prefixes {
		for _, backend := range r.routes[prefix] {
			backends = append(backends, cloneBackend(backend))
		}
	}
	return backends
}

// Replace atomically publishes a discovery snapshot. Multiple backends may
// share a route prefix for explicit canary traffic, but duplicate backend
// identities are rejected so a discovery bug cannot create unstable choices.
func (r *MemoryRegistry) Replace(backends []Backend) error {
	if r == nil {
		return errors.New("registry is required")
	}

	grouped := make(map[string][]Backend, len(backends))
	seen := make(map[string]map[backendKey]struct{})
	var errs []error
	for _, backend := range backends {
		normalized, err := normalizeBackend(backend)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if seen[normalized.RoutePrefix] == nil {
			seen[normalized.RoutePrefix] = make(map[backendKey]struct{})
		}
		key := normalized.identity()
		if _, found := seen[normalized.RoutePrefix][key]; found {
			errs = append(errs, fmt.Errorf(
				"route prefix %q has duplicate backend %s/%s",
				normalized.RoutePrefix,
				normalized.Namespace,
				normalized.Name,
			))
			continue
		}
		seen[normalized.RoutePrefix][key] = struct{}{}
		grouped[normalized.RoutePrefix] = append(grouped[normalized.RoutePrefix], normalized)
	}

	next := make(map[string][]Backend, len(grouped))
	for prefix, candidates := range grouped {
		sortBackends(candidates)
		next[prefix] = candidates
	}

	r.mu.Lock()
	r.routes = next
	r.pruneRoundRobinLocked()
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
	for prefix, candidates := range r.routes {
		for index, backend := range candidates {
			if backend.State != StateReady {
				continue
			}
			backend.State = StateUnavailable
			backend.Endpoint = nil
			backend.Message = message
			candidates[index] = backend
		}
		r.routes[prefix] = candidates
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

func (r *MemoryRegistry) selectBackendLocked(prefix string, load func(Backend) int) Backend {
	candidates := r.routes[prefix]
	if len(candidates) == 0 {
		return Backend{}
	}
	ready := make([]Backend, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.State == StateReady && candidate.effectiveWeight() > 0 {
			ready = append(ready, candidate)
		}
	}
	if len(ready) == 0 {
		return cloneBackend(noPositiveWeightBackend(candidates))
	}
	if len(ready) == 1 {
		return cloneBackend(ready[0])
	}
	if routeStrategy(ready) == RoutingStrategyWeighted || load == nil {
		return cloneBackend(r.weightedBackendLocked(prefix, ready))
	}

	leastLoaded := make([]Backend, 0, len(ready))
	minLoad := 0
	for index, candidate := range ready {
		current := load(candidate)
		if index == 0 || current < minLoad {
			minLoad = current
			leastLoaded = leastLoaded[:0]
		}
		if current == minLoad {
			leastLoaded = append(leastLoaded, candidate)
		}
	}
	return cloneBackend(r.weightedBackendLocked(prefix, leastLoaded))
}

func noPositiveWeightBackend(candidates []Backend) Backend {
	for _, candidate := range candidates {
		if candidate.State != StateReady {
			continue
		}
		candidate.State = StateUnavailable
		candidate.Endpoint = nil
		candidate.Message = "model route has no positive traffic weight"
		return candidate
	}
	return candidates[0]
}

func (r *MemoryRegistry) weightedBackendLocked(prefix string, candidates []Backend) Backend {
	if len(candidates) == 1 {
		return candidates[0]
	}
	if r.rr[prefix] == nil {
		r.rr[prefix] = make(map[backendKey]int)
	}
	total := 0
	for _, candidate := range candidates {
		total += candidate.effectiveWeight()
		r.rr[prefix][candidate.identity()] += candidate.effectiveWeight()
	}
	chosen := candidates[0]
	chosenScore := r.rr[prefix][chosen.identity()]
	for _, candidate := range candidates[1:] {
		score := r.rr[prefix][candidate.identity()]
		if score > chosenScore {
			chosen = candidate
			chosenScore = score
		}
	}
	r.rr[prefix][chosen.identity()] -= total
	return chosen
}

func (r *MemoryRegistry) pruneRoundRobinLocked() {
	for prefix, scores := range r.rr {
		candidates := r.routes[prefix]
		if len(candidates) == 0 {
			delete(r.rr, prefix)
			continue
		}
		live := make(map[backendKey]struct{}, len(candidates))
		for _, candidate := range candidates {
			live[candidate.identity()] = struct{}{}
		}
		for key := range scores {
			if _, found := live[key]; !found {
				delete(scores, key)
			}
		}
	}
}

func routeStrategy(candidates []Backend) RoutingStrategy {
	for _, candidate := range candidates {
		if candidate.Policy.RoutingStrategy == RoutingStrategyWeighted {
			return RoutingStrategyWeighted
		}
	}
	return RoutingStrategyLeastLoaded
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
	if err := normalizeTrafficPolicy(&backend.Policy); err != nil {
		return Backend{}, fmt.Errorf("model %q has invalid routing policy: %w", backend.Name, err)
	}
	return cloneBackend(backend), nil
}

func normalizeTrafficPolicy(policy *TrafficPolicy) error {
	switch policy.RoutingStrategy {
	case "", RoutingStrategyLeastLoaded:
		policy.RoutingStrategy = RoutingStrategyLeastLoaded
	case RoutingStrategyWeighted:
	default:
		return fmt.Errorf("unsupported routing strategy %q", policy.RoutingStrategy)
	}
	if policy.Weight != nil && *policy.Weight < 0 {
		return errors.New("traffic weight must be non-negative")
	}
	if policy.RateLimit != nil {
		if policy.RateLimit.RequestsPerMinute < 0 {
			return errors.New("rateLimit.requestsPerMinute must be non-negative")
		}
		if policy.RateLimit.Burst < 0 {
			return errors.New("rateLimit.burst must be non-negative")
		}
	}
	return nil
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
	if backend.Policy.Weight != nil {
		weight := *backend.Policy.Weight
		backend.Policy.Weight = &weight
	}
	if backend.Policy.RateLimit != nil {
		rateLimit := *backend.Policy.RateLimit
		backend.Policy.RateLimit = &rateLimit
	}
	if backend.Policy.RequestLogging != nil {
		requestLogging := *backend.Policy.RequestLogging
		if requestLogging.Enabled != nil {
			enabled := *requestLogging.Enabled
			requestLogging.Enabled = &enabled
		}
		backend.Policy.RequestLogging = &requestLogging
	}
	return backend
}

func (b Backend) effectiveWeight() int {
	if b.Policy.Weight == nil {
		return DefaultTrafficWeight
	}
	return *b.Policy.Weight
}

func (b Backend) identity() backendKey {
	return backendKey{namespace: b.Namespace, name: b.Name}
}

type backendKey struct {
	namespace string
	name      string
}

func sortBackends(backends []Backend) {
	sort.Slice(backends, func(i, j int) bool {
		left := backends[i]
		right := backends[j]
		if left.Namespace == right.Namespace {
			return left.Name < right.Name
		}
		return left.Namespace < right.Namespace
	})
}
