// Package discovery projects Kubernetes ModelDeployments and Services into the
// gateway's in-memory routing registry.
package discovery

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brassinai/inferops/gateway/internal/routing"
	"github.com/brassinai/inferops/internal/runtimecontract"
	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultSyncInterval = 5 * time.Second
	minimumSyncInterval = time.Second
	maximumSyncTimeout  = 10 * time.Second
	staleIntervalCount  = 3
)

// Lister is the read-only Kubernetes client surface used by discovery.
type Lister interface {
	List(context.Context, client.ObjectList, ...client.ListOption) error
}

// Discovery periodically publishes an atomic registry snapshot from Kubernetes.
type Discovery struct {
	client       Lister
	registry     routing.ReplacingRegistry
	namespace    string
	syncInterval time.Duration
	logger       *log.Logger
	freshnessMu  sync.RWMutex
	lastSuccess  time.Time
}

type publicationError struct {
	err error
}

func (e *publicationError) Error() string {
	return e.err.Error()
}

func (e *publicationError) Unwrap() error {
	return e.err
}

// New creates namespace-scoped Kubernetes model discovery.
func New(
	kubernetesClient Lister,
	registry routing.ReplacingRegistry,
	namespace string,
	syncInterval time.Duration,
	logger *log.Logger,
) (*Discovery, error) {
	if kubernetesClient == nil {
		return nil, errors.New("kubernetes client is required")
	}
	if registry == nil {
		return nil, errors.New("model registry is required")
	}
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return nil, errors.New("discovery namespace is required")
	}
	if syncInterval <= 0 {
		syncInterval = defaultSyncInterval
	}
	if syncInterval < minimumSyncInterval {
		return nil, fmt.Errorf("discovery sync interval must be at least %s", minimumSyncInterval)
	}
	if syncInterval > time.Duration(1<<63-1)/staleIntervalCount {
		return nil, errors.New("discovery sync interval is too large")
	}
	if logger == nil {
		logger = log.Default()
	}
	return &Discovery{
		client:       kubernetesClient,
		registry:     registry,
		namespace:    namespace,
		syncInterval: syncInterval,
		logger:       logger,
	}, nil
}

// Ready reports whether at least one complete Kubernetes snapshot was read.
func (d *Discovery) Ready() bool {
	if d == nil {
		return false
	}
	lastSuccess := d.lastSuccessfulSync()
	return !lastSuccess.IsZero() && time.Since(lastSuccess) < d.staleAfter()
}

// Run continuously refreshes the registry until ctx is canceled. Failed reads
// preserve the last complete snapshot and retry with bounded exponential delay.
func (d *Discovery) Run(ctx context.Context) {
	if d == nil {
		return
	}
	retryDelay := min(d.syncInterval, 250*time.Millisecond)
	for {
		syncCtx, cancelSync := context.WithTimeout(ctx, min(d.syncInterval, maximumSyncTimeout))
		err := d.Sync(syncCtx)
		cancelSync()
		delay := d.syncInterval
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			d.logger.Printf("gateway discovery sync failed: %v", err)
			var publishErr *publicationError
			if errors.As(err, &publishErr) {
				retryDelay = min(d.syncInterval, 250*time.Millisecond)
			} else {
				delay = retryDelay
				retryDelay = min(retryDelay*2, d.syncInterval)
				if d.snapshotStale() {
					d.registry.MarkReadyUnavailable("model registry is stale because Kubernetes discovery is unavailable")
				}
			}
		} else {
			retryDelay = min(d.syncInterval, 250*time.Millisecond)
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

// Sync reads one complete object snapshot. The existing registry remains
// untouched if either Kubernetes list fails.
func (d *Discovery) Sync(ctx context.Context) error {
	if d == nil {
		return errors.New("discovery is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	var deployments v1alpha1.ModelDeploymentList
	if err := d.client.List(ctx, &deployments, client.InNamespace(d.namespace)); err != nil {
		return fmt.Errorf("list ModelDeployments: %w", err)
	}
	var services corev1.ServiceList
	if err := d.client.List(ctx, &services, client.InNamespace(d.namespace)); err != nil {
		return fmt.Errorf("list runtime Services: %w", err)
	}
	var endpointSlices discoveryv1.EndpointSliceList
	if err := d.client.List(ctx, &endpointSlices, client.InNamespace(d.namespace)); err != nil {
		return fmt.Errorf("list runtime EndpointSlices: %w", err)
	}

	serviceByName := make(map[string]*corev1.Service, len(services.Items))
	for index := range services.Items {
		service := &services.Items[index]
		serviceByName[service.Name] = service
	}
	readyServices := readyServicesByName(endpointSlices.Items)

	backends := make([]routing.Backend, 0, len(deployments.Items))
	for index := range deployments.Items {
		backends = append(backends, backendFor(&deployments.Items[index], serviceByName, readyServices))
	}
	err := d.registry.Replace(backends)
	d.recordSuccessfulSync(time.Now())
	if err != nil {
		return &publicationError{err: fmt.Errorf("publish model registry: %w", err)}
	}
	return nil
}

func (d *Discovery) staleAfter() time.Duration {
	return staleIntervalCount * d.syncInterval
}

func (d *Discovery) snapshotStale() bool {
	lastSuccess := d.lastSuccessfulSync()
	return !lastSuccess.IsZero() && time.Since(lastSuccess) >= d.staleAfter()
}

func (d *Discovery) recordSuccessfulSync(at time.Time) {
	d.freshnessMu.Lock()
	defer d.freshnessMu.Unlock()
	d.lastSuccess = at
}

func (d *Discovery) lastSuccessfulSync() time.Time {
	d.freshnessMu.RLock()
	defer d.freshnessMu.RUnlock()
	return d.lastSuccess
}

func backendFor(
	deployment *v1alpha1.ModelDeployment,
	services map[string]*corev1.Service,
	readyServices map[string]bool,
) routing.Backend {
	routePrefix := deployment.Spec.Routing.Path
	if routePrefix == "" {
		routePrefix = routing.DefaultRoutePrefix(deployment.Name)
	}
	backend := routing.Backend{
		Name:        deployment.Name,
		Namespace:   deployment.Namespace,
		RoutePrefix: routePrefix,
		State:       routing.StateUnavailable,
		Message:     "model runtime is unavailable",
		Policy:      routingPolicy(deployment.Spec.Routing.Policy),
	}

	if deployment.Status.Phase == v1alpha1.ModelDeploymentPhaseDraining {
		backend.State = routing.StateDraining
		backend.Message = "model is draining and is not accepting new requests"
		return backend
	}
	if deployment.Spec.Activation.DesiredState != v1alpha1.ActivationDesiredStateActive {
		backend.State = routing.StateInactive
		backend.Message = "model is inactive"
		return backend
	}

	switch deployment.Status.Phase {
	case v1alpha1.ModelDeploymentPhaseDeactivating, v1alpha1.ModelDeploymentPhaseCached:
		backend.State = routing.StateInactive
		backend.Message = "model is inactive"
		return backend
	case v1alpha1.ModelDeploymentPhasePending,
		v1alpha1.ModelDeploymentPhaseDownloading,
		v1alpha1.ModelDeploymentPhaseWaitingForCapacity,
		v1alpha1.ModelDeploymentPhaseWaitingForGPU,
		v1alpha1.ModelDeploymentPhaseActivating:
		backend.State = routing.StateActivating
		backend.Message = "model is activating"
		return backend
	case v1alpha1.ModelDeploymentPhaseFailed:
		backend.Message = "model deployment failed"
		return backend
	case v1alpha1.ModelDeploymentPhaseActive:
		// Continue through all readiness gates below.
	default:
		backend.State = routing.StateActivating
		backend.Message = "model state has not been observed yet"
		return backend
	}

	if !deployment.Spec.Routing.Enabled || !deployment.Spec.Routing.OpenAICompatible {
		backend.Message = "OpenAI-compatible gateway routing is disabled"
		return backend
	}
	if deployment.Status.ObservedGeneration < deployment.Generation {
		backend.Message = "model status is stale"
		return backend
	}
	if !conditionTrueForGeneration(deployment, v1alpha1.ConditionRuntimeReady) ||
		!conditionTrueForGeneration(deployment, v1alpha1.ConditionModelLoaded) ||
		!conditionTrueForGeneration(deployment, v1alpha1.ConditionRoutingReady) ||
		!conditionTrueForGeneration(deployment, v1alpha1.ConditionReady) ||
		!deployment.Status.Model.Loaded {
		backend.Message = "model runtime is not ready for routing"
		return backend
	}
	if deployment.Status.Replicas.Desired < 1 ||
		deployment.Status.Replicas.Ready < deployment.Status.Replicas.Desired {
		backend.Message = "model runtime has no ready replicas"
		return backend
	}

	serviceName := deployment.Status.ServiceName
	if serviceName == "" {
		serviceName = runtimecontract.ServiceName(deployment.Name)
	}
	service, found := services[serviceName]
	if !found {
		backend.Message = "model runtime Service was not found"
		return backend
	}
	if service.DeletionTimestamp != nil {
		backend.Message = "model runtime Service is terminating"
		return backend
	}
	if service.Labels[runtimecontract.ModelDeploymentLabel] != deployment.Name {
		backend.Message = "model runtime Service ownership does not match"
		return backend
	}
	if !ownedByDeployment(service, deployment) {
		backend.Message = "model runtime Service owner reference does not match"
		return backend
	}
	if service.Namespace != deployment.Namespace ||
		service.Spec.Selector[runtimecontract.ModelDeploymentLabel] != deployment.Name {
		backend.Message = "model runtime Service selector does not match"
		return backend
	}
	port, found := runtimeHTTPPort(service)
	if !found {
		backend.Message = "model runtime Service has no TCP port"
		return backend
	}
	if service.Spec.Type != corev1.ServiceTypeClusterIP {
		backend.Message = "model runtime Service must be a ClusterIP Service"
		return backend
	}
	if service.Spec.ClusterIP == "" || service.Spec.ClusterIP == corev1.ClusterIPNone {
		backend.Message = "model runtime Service is not allocated"
		return backend
	}
	if !readyServices[service.Name] {
		backend.Message = "model runtime Service has no ready endpoints"
		return backend
	}

	host := service.Name + "." + deployment.Namespace + ".svc"
	backend.Endpoint = &url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(host, strconv.Itoa(int(port))),
	}
	backend.State = routing.StateReady
	backend.Message = ""
	return backend
}

func routingPolicy(policy v1alpha1.RoutingPolicySpec) routing.TrafficPolicy {
	gatewayPolicy := routing.TrafficPolicy{}
	switch policy.RoutingStrategy {
	case v1alpha1.RoutingStrategyWeighted:
		gatewayPolicy.RoutingStrategy = routing.RoutingStrategyWeighted
	case v1alpha1.RoutingStrategyLeastLoaded:
		gatewayPolicy.RoutingStrategy = routing.RoutingStrategyLeastLoaded
	}
	if policy.Weight != nil {
		weight := int(*policy.Weight)
		gatewayPolicy.Weight = &weight
	}
	if policy.RateLimit != nil {
		gatewayPolicy.RateLimit = &routing.RateLimitPolicy{
			RequestsPerMinute: int(policy.RateLimit.RequestsPerMinute),
			Burst:             int(policy.RateLimit.Burst),
		}
	}
	if policy.RequestLogging.Enabled != nil {
		enabled := *policy.RequestLogging.Enabled
		gatewayPolicy.RequestLogging = &routing.RequestLoggingPolicy{Enabled: &enabled}
	}
	return gatewayPolicy
}

func readyServicesByName(endpointSlices []discoveryv1.EndpointSlice) map[string]bool {
	readyServices := make(map[string]bool)
	for sliceIndex := range endpointSlices {
		endpointSlice := &endpointSlices[sliceIndex]
		serviceName := endpointSlice.Labels[discoveryv1.LabelServiceName]
		if serviceName == "" ||
			readyServices[serviceName] ||
			endpointSlice.DeletionTimestamp != nil ||
			!endpointSliceHasHTTPPort(endpointSlice) {
			continue
		}
		for endpointIndex := range endpointSlice.Endpoints {
			endpoint := &endpointSlice.Endpoints[endpointIndex]
			terminating := endpoint.Conditions.Terminating != nil &&
				*endpoint.Conditions.Terminating
			ready := endpoint.Conditions.Ready == nil || *endpoint.Conditions.Ready
			if !terminating && ready && len(endpoint.Addresses) > 0 {
				readyServices[serviceName] = true
				break
			}
		}
	}
	return readyServices
}

func endpointSliceHasHTTPPort(endpointSlice *discoveryv1.EndpointSlice) bool {
	for _, endpointPort := range endpointSlice.Ports {
		if endpointPort.Name == nil ||
			*endpointPort.Name != runtimecontract.HTTPPortName ||
			endpointPort.Port == nil ||
			*endpointPort.Port < 1 {
			continue
		}
		if endpointPort.Protocol == nil || *endpointPort.Protocol == corev1.ProtocolTCP {
			return true
		}
	}
	return false
}

func ownedByDeployment(service *corev1.Service, deployment *v1alpha1.ModelDeployment) bool {
	for _, owner := range service.OwnerReferences {
		if owner.APIVersion != v1alpha1.GroupVersion.String() ||
			owner.Kind != "ModelDeployment" ||
			owner.Name != deployment.Name ||
			owner.Controller == nil ||
			!*owner.Controller {
			continue
		}
		return deployment.UID == "" || owner.UID == deployment.UID
	}
	return false
}

func conditionTrueForGeneration(deployment *v1alpha1.ModelDeployment, conditionType string) bool {
	for _, condition := range deployment.Status.Conditions {
		if condition.Type == conditionType {
			return condition.Status == metav1.ConditionTrue &&
				condition.ObservedGeneration >= deployment.Generation
		}
	}
	return false
}

func runtimeHTTPPort(service *corev1.Service) (int32, bool) {
	for _, servicePort := range service.Spec.Ports {
		if servicePort.Name == runtimecontract.HTTPPortName &&
			(servicePort.Protocol == "" || servicePort.Protocol == corev1.ProtocolTCP) &&
			servicePort.TargetPort.Type == intstr.String &&
			servicePort.TargetPort.StrVal == runtimecontract.HTTPPortName &&
			servicePort.Port > 0 {
			return servicePort.Port, true
		}
	}
	return 0, false
}
