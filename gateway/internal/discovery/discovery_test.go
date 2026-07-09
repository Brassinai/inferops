package discovery

import (
	"context"
	"errors"
	"io"
	"log"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brassinai/inferops/gateway/internal/routing"
	"github.com/brassinai/inferops/internal/runtimecontract"
	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestSyncPublishesOnlyReadyActiveOwnedService(t *testing.T) {
	t.Parallel()
	deployment := readyDeployment()
	service := runtimeService()
	kubernetesClient := fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithObjects(deployment, service, runtimeEndpointSlice()).
		Build()
	registry := routing.NewMemoryRegistry()
	modelDiscovery := newTestDiscovery(t, kubernetesClient, registry)

	if err := modelDiscovery.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if !modelDiscovery.Ready() {
		t.Fatal("Ready() = false after complete snapshot")
	}
	backend, upstreamPath, found := registry.Lookup("/models/qwen-chat/v1/chat/completions")
	if !found {
		t.Fatal("active model route was not published")
	}
	if backend.State != routing.StateReady {
		t.Fatalf("backend state = %q, want ready; message=%q", backend.State, backend.Message)
	}
	if backend.Endpoint.String() != "http://qwen-chat-runtime.inferops.svc:8000" {
		t.Errorf("backend endpoint = %q", backend.Endpoint)
	}
	if upstreamPath != "/v1/chat/completions" {
		t.Errorf("upstream path = %q", upstreamPath)
	}
}

func TestSyncPublishesRoutingPolicy(t *testing.T) {
	t.Parallel()
	deployment := readyDeployment()
	weight := int32(25)
	loggingEnabled := true
	deployment.Spec.Routing.Policy = v1alpha1.RoutingPolicySpec{
		RoutingStrategy: v1alpha1.RoutingStrategyWeighted,
		Weight:          &weight,
		RateLimit: &v1alpha1.RateLimitSpec{
			RequestsPerMinute: 60,
			Burst:             10,
		},
		RequestLogging: v1alpha1.RequestLoggingPolicy{Enabled: &loggingEnabled},
	}
	kubernetesClient := fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithObjects(deployment, runtimeService(), runtimeEndpointSlice()).
		Build()
	registry := routing.NewMemoryRegistry()
	modelDiscovery := newTestDiscovery(t, kubernetesClient, registry)

	if err := modelDiscovery.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	backend, _, found := registry.Lookup("/models/qwen-chat/v1/chat/completions")
	if !found {
		t.Fatal("active model route was not published")
	}
	if backend.Policy.RoutingStrategy != routing.RoutingStrategyWeighted ||
		backend.Policy.Weight == nil ||
		*backend.Policy.Weight != 25 ||
		backend.Policy.RateLimit == nil ||
		backend.Policy.RateLimit.RequestsPerMinute != 60 ||
		backend.Policy.RateLimit.Burst != 10 ||
		backend.Policy.RequestLogging == nil ||
		backend.Policy.RequestLogging.Enabled == nil ||
		!*backend.Policy.RequestLogging.Enabled {
		t.Fatalf("backend policy = %#v, want routing policy from ModelDeployment", backend.Policy)
	}
}

func TestSyncReactsToDrainBeforeAcceptingAnotherRequest(t *testing.T) {
	t.Parallel()
	scheme := testScheme(t)
	kubernetesClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.ModelDeployment{}).
		WithObjects(readyDeployment(), runtimeService(), runtimeEndpointSlice()).
		Build()
	registry := routing.NewMemoryRegistry()
	modelDiscovery := newTestDiscovery(t, kubernetesClient, registry)
	ctx := context.Background()

	if err := modelDiscovery.Sync(ctx); err != nil {
		t.Fatalf("initial Sync() error = %v", err)
	}
	backend, _, found := registry.Lookup("/models/qwen-chat/v1/models")
	if !found || backend.State != routing.StateReady {
		t.Fatalf("initial backend = (%+v, %t), want ready", backend, found)
	}

	var deployment v1alpha1.ModelDeployment
	key := types.NamespacedName{Namespace: "inferops", Name: "qwen-chat"}
	if err := kubernetesClient.Get(ctx, key, &deployment); err != nil {
		t.Fatalf("get ModelDeployment: %v", err)
	}
	deployment.Status.Phase = v1alpha1.ModelDeploymentPhaseDraining
	if err := kubernetesClient.Status().Update(ctx, &deployment); err != nil {
		t.Fatalf("update ModelDeployment status: %v", err)
	}
	if err := modelDiscovery.Sync(ctx); err != nil {
		t.Fatalf("draining Sync() error = %v", err)
	}
	backend, _, found = registry.Lookup("/models/qwen-chat/v1/models")
	if !found || backend.State != routing.StateDraining {
		t.Fatalf("draining backend = (%+v, %t), want draining", backend, found)
	}
}

func TestSyncKeepsStableRouteWhenServiceDisappears(t *testing.T) {
	t.Parallel()
	kubernetesClient := fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithObjects(readyDeployment(), runtimeService(), runtimeEndpointSlice()).
		Build()
	registry := routing.NewMemoryRegistry()
	modelDiscovery := newTestDiscovery(t, kubernetesClient, registry)
	ctx := context.Background()

	if err := modelDiscovery.Sync(ctx); err != nil {
		t.Fatalf("initial Sync() error = %v", err)
	}
	if err := kubernetesClient.Delete(ctx, runtimeService()); err != nil {
		t.Fatalf("delete Service: %v", err)
	}
	if err := modelDiscovery.Sync(ctx); err != nil {
		t.Fatalf("Sync() after Service deletion error = %v", err)
	}
	backend, _, found := registry.Lookup("/models/qwen-chat/v1/models")
	if !found {
		t.Fatal("stable model route disappeared with runtime Service")
	}
	if backend.State != routing.StateUnavailable || backend.Endpoint != nil {
		t.Fatalf("backend after Service deletion = %+v, want unavailable without endpoint", backend)
	}
}

func TestSyncKeepsRouteUnavailableUntilEndpointIsReady(t *testing.T) {
	t.Parallel()
	kubernetesClient := fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithObjects(readyDeployment(), runtimeService()).
		Build()
	registry := routing.NewMemoryRegistry()
	modelDiscovery := newTestDiscovery(t, kubernetesClient, registry)

	if err := modelDiscovery.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	backend, _, found := registry.Lookup("/models/qwen-chat/v1/models")
	if !found || backend.State != routing.StateUnavailable || backend.Endpoint != nil {
		t.Fatalf("backend without ready EndpointSlice = (%+v, %t), want unavailable", backend, found)
	}
}

func TestSyncRejectsStaleStatusAndUnownedService(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*v1alpha1.ModelDeployment, *corev1.Service)
	}{
		{
			name: "stale observed generation",
			mutate: func(deployment *v1alpha1.ModelDeployment, _ *corev1.Service) {
				deployment.Status.ObservedGeneration = deployment.Generation - 1
			},
		},
		{
			name: "stale readiness condition",
			mutate: func(deployment *v1alpha1.ModelDeployment, _ *corev1.Service) {
				deployment.Status.Conditions[0].ObservedGeneration = deployment.Generation - 1
			},
		},
		{
			name: "overall readiness false",
			mutate: func(deployment *v1alpha1.ModelDeployment, _ *corev1.Service) {
				setTestCondition(deployment, v1alpha1.ConditionReady, metav1.ConditionFalse)
			},
		},
		{
			name: "model not loaded",
			mutate: func(deployment *v1alpha1.ModelDeployment, _ *corev1.Service) {
				deployment.Status.Model.Loaded = false
			},
		},
		{
			name: "unowned service",
			mutate: func(_ *v1alpha1.ModelDeployment, service *corev1.Service) {
				service.Labels[runtimecontract.ModelDeploymentLabel] = "some-other-model"
			},
		},
		{
			name: "wrong owner reference",
			mutate: func(_ *v1alpha1.ModelDeployment, service *corev1.Service) {
				service.OwnerReferences[0].UID = "some-other-uid"
			},
		},
		{
			name: "non-controller owner reference",
			mutate: func(_ *v1alpha1.ModelDeployment, service *corev1.Service) {
				controller := false
				service.OwnerReferences[0].Controller = &controller
			},
		},
		{
			name: "wrong service selector",
			mutate: func(_ *v1alpha1.ModelDeployment, service *corev1.Service) {
				service.Spec.Selector[runtimecontract.ModelDeploymentLabel] = "some-other-model"
			},
		},
		{
			name: "unnamed service port",
			mutate: func(_ *v1alpha1.ModelDeployment, service *corev1.Service) {
				service.Spec.Ports[0].Name = ""
			},
		},
		{
			name: "wrong service target port",
			mutate: func(_ *v1alpha1.ModelDeployment, service *corev1.Service) {
				service.Spec.Ports[0].TargetPort = intstr.FromString("metrics")
			},
		},
		{
			name: "headless service",
			mutate: func(_ *v1alpha1.ModelDeployment, service *corev1.Service) {
				service.Spec.ClusterIP = corev1.ClusterIPNone
			},
		},
		{
			name: "no ready replicas",
			mutate: func(deployment *v1alpha1.ModelDeployment, _ *corev1.Service) {
				deployment.Status.Replicas.Ready = 0
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			deployment := readyDeployment()
			service := runtimeService()
			test.mutate(deployment, service)
			kubernetesClient := fake.NewClientBuilder().
				WithScheme(testScheme(t)).
				WithObjects(deployment, service, runtimeEndpointSlice()).
				Build()
			registry := routing.NewMemoryRegistry()
			modelDiscovery := newTestDiscovery(t, kubernetesClient, registry)
			if err := modelDiscovery.Sync(context.Background()); err != nil {
				t.Fatalf("Sync() error = %v", err)
			}
			backend, _, found := registry.Lookup("/models/qwen-chat/v1/models")
			if !found || backend.State != routing.StateUnavailable || backend.Endpoint != nil {
				t.Fatalf("backend = (%+v, %t), want unavailable", backend, found)
			}
		})
	}
}

func TestBackendLifecycleStates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		phase        v1alpha1.ModelDeploymentPhase
		desiredState v1alpha1.ActivationDesiredState
		wantState    routing.State
	}{
		{name: "inactive", phase: v1alpha1.ModelDeploymentPhaseCached, desiredState: v1alpha1.ActivationDesiredStateInactive, wantState: routing.StateInactive},
		{name: "pending activation", phase: v1alpha1.ModelDeploymentPhasePending, desiredState: v1alpha1.ActivationDesiredStateActive, wantState: routing.StateActivating},
		{name: "waiting for GPU", phase: v1alpha1.ModelDeploymentPhaseWaitingForGPU, desiredState: v1alpha1.ActivationDesiredStateActive, wantState: routing.StateActivating},
		{name: "activating", phase: v1alpha1.ModelDeploymentPhaseActivating, desiredState: v1alpha1.ActivationDesiredStateActive, wantState: routing.StateActivating},
		{name: "active", phase: v1alpha1.ModelDeploymentPhaseActive, desiredState: v1alpha1.ActivationDesiredStateActive, wantState: routing.StateReady},
		{name: "draining after deactivation request", phase: v1alpha1.ModelDeploymentPhaseDraining, desiredState: v1alpha1.ActivationDesiredStateInactive, wantState: routing.StateDraining},
		{name: "deactivating", phase: v1alpha1.ModelDeploymentPhaseDeactivating, desiredState: v1alpha1.ActivationDesiredStateInactive, wantState: routing.StateInactive},
		{name: "failed", phase: v1alpha1.ModelDeploymentPhaseFailed, desiredState: v1alpha1.ActivationDesiredStateActive, wantState: routing.StateUnavailable},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			deployment := readyDeployment()
			deployment.Status.Phase = test.phase
			deployment.Spec.Activation.DesiredState = test.desiredState
			services := map[string]*corev1.Service{
				"qwen-chat-runtime": runtimeService(),
			}
			backend := backendFor(deployment, services, map[string]bool{"qwen-chat-runtime": true})
			if backend.State != test.wantState {
				t.Fatalf("state = %q, want %q; message=%q", backend.State, test.wantState, backend.Message)
			}
		})
	}
}

func TestReadyServicesByName(t *testing.T) {
	t.Parallel()
	ready := true
	notReady := false
	terminating := true
	tests := []struct {
		name      string
		endpoint  discoveryv1.Endpoint
		wantReady bool
	}{
		{
			name:      "explicitly ready",
			endpoint:  discoveryv1.Endpoint{Addresses: []string{"10.0.0.1"}, Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
			wantReady: true,
		},
		{
			name:      "unknown readiness follows Kubernetes semantics",
			endpoint:  discoveryv1.Endpoint{Addresses: []string{"10.0.0.1"}},
			wantReady: true,
		},
		{
			name:     "not ready",
			endpoint: discoveryv1.Endpoint{Addresses: []string{"10.0.0.1"}, Conditions: discoveryv1.EndpointConditions{Ready: &notReady}},
		},
		{
			name:     "terminating",
			endpoint: discoveryv1.Endpoint{Addresses: []string{"10.0.0.1"}, Conditions: discoveryv1.EndpointConditions{Ready: &ready, Terminating: &terminating}},
		},
		{
			name:     "missing address",
			endpoint: discoveryv1.Endpoint{Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			slices := []discoveryv1.EndpointSlice{
				{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							discoveryv1.LabelServiceName: "qwen-chat-runtime",
						},
					},
					Ports:     runtimeEndpointSlice().Ports,
					Endpoints: []discoveryv1.Endpoint{test.endpoint},
				},
			}
			got := readyServicesByName(slices)["qwen-chat-runtime"]
			if got != test.wantReady {
				t.Fatalf("readyServicesByName() = %t, want %t", got, test.wantReady)
			}
		})
	}

	sliceWithoutPort := runtimeEndpointSlice()
	sliceWithoutPort.Ports = nil
	if readyServicesByName([]discoveryv1.EndpointSlice{*sliceWithoutPort})["qwen-chat-runtime"] {
		t.Fatal("EndpointSlice without the runtime HTTP port was considered ready")
	}
	deletingSlice := runtimeEndpointSlice()
	now := metav1.Now()
	deletingSlice.DeletionTimestamp = &now
	if readyServicesByName([]discoveryv1.EndpointSlice{*deletingSlice})["qwen-chat-runtime"] {
		t.Fatal("terminating EndpointSlice was considered ready")
	}
}

func TestDiscoveryRetriesFailedListAndEventuallyPublishes(t *testing.T) {
	t.Parallel()
	delegate := fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithObjects(readyDeployment(), runtimeService(), runtimeEndpointSlice()).
		Build()
	flakyClient := &failFirstLister{delegate: delegate}
	registry := routing.NewMemoryRegistry()
	modelDiscovery, err := New(
		flakyClient,
		registry,
		"inferops",
		minimumSyncInterval,
		log.New(io.Discard, "", 0),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go modelDiscovery.Run(ctx)

	for !modelDiscovery.Ready() {
		select {
		case <-ctx.Done():
			t.Fatalf("discovery did not recover: %v", ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	backend, _, found := registry.Lookup("/models/qwen-chat/v1/models")
	if !found || backend.State != routing.StateReady {
		t.Fatalf("backend = (%+v, %t), want ready after retry", backend, found)
	}
	if calls := flakyClient.calls.Load(); calls < 4 {
		t.Fatalf("List() calls = %d, want failed call plus complete retry", calls)
	}
}

func TestFailedSnapshotPreservesLastCompleteRegistry(t *testing.T) {
	t.Parallel()
	registry := routing.NewMemoryRegistry()
	if err := registry.Upsert(routing.Backend{Name: "existing", State: routing.StateInactive}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	modelDiscovery := newTestDiscovery(t, alwaysFailLister{}, registry)

	if err := modelDiscovery.Sync(context.Background()); err == nil {
		t.Fatal("Sync() error = nil, want list failure")
	}
	backend, _, found := registry.Lookup("/models/existing/v1/models")
	if !found || backend.State != routing.StateInactive {
		t.Fatalf("last complete route = (%+v, %t), want preserved inactive route", backend, found)
	}
	if modelDiscovery.Ready() {
		t.Fatal("Ready() = true without a complete Kubernetes snapshot")
	}
}

func TestDiscoveryFailsClosedWhenSnapshotStaysStaleAndRecovers(t *testing.T) {
	t.Parallel()
	delegate := fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithObjects(readyDeployment(), runtimeService(), runtimeEndpointSlice()).
		Build()
	switchable := &switchableLister{delegate: delegate}
	registry := routing.NewMemoryRegistry()
	modelDiscovery := newTestDiscovery(t, switchable, registry)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := modelDiscovery.Sync(ctx); err != nil {
		t.Fatalf("initial Sync() error = %v", err)
	}
	switchable.fail.Store(true)
	go modelDiscovery.Run(ctx)

	waitFor(t, ctx, func() bool {
		backend, _, found := registry.Lookup("/models/qwen-chat/v1/models")
		return found &&
			backend.State == routing.StateUnavailable &&
			!modelDiscovery.Ready()
	}, "discovery did not fail closed after its snapshot became stale")

	switchable.fail.Store(false)
	waitFor(t, ctx, func() bool {
		backend, _, found := registry.Lookup("/models/qwen-chat/v1/models")
		return found &&
			backend.State == routing.StateReady &&
			modelDiscovery.Ready()
	}, "discovery did not restore routing after Kubernetes recovered")
}

func TestDiscoveryFailsClosedWhenKubernetesReadHangs(t *testing.T) {
	t.Parallel()
	delegate := fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithObjects(readyDeployment(), runtimeService(), runtimeEndpointSlice()).
		Build()
	blocking := &blockingLister{delegate: delegate}
	registry := routing.NewMemoryRegistry()
	modelDiscovery := newTestDiscovery(t, blocking, registry)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := modelDiscovery.Sync(ctx); err != nil {
		t.Fatalf("initial Sync() error = %v", err)
	}
	blocking.block.Store(true)
	go modelDiscovery.Run(ctx)

	waitFor(t, ctx, func() bool {
		backend, _, found := registry.Lookup("/models/qwen-chat/v1/models")
		return found &&
			backend.State == routing.StateUnavailable &&
			!modelDiscovery.Ready()
	}, "discovery did not fail closed after a hung Kubernetes read")
}

type failFirstLister struct {
	delegate Lister
	calls    atomic.Int32
}

func (l *failFirstLister) List(
	ctx context.Context,
	list client.ObjectList,
	options ...client.ListOption,
) error {
	if l.calls.Add(1) == 1 {
		return errors.New("temporary Kubernetes API failure")
	}
	return l.delegate.List(ctx, list, options...)
}

type alwaysFailLister struct{}

func (alwaysFailLister) List(
	context.Context,
	client.ObjectList,
	...client.ListOption,
) error {
	return errors.New("Kubernetes API unavailable")
}

type switchableLister struct {
	delegate Lister
	fail     atomic.Bool
}

type blockingLister struct {
	delegate Lister
	block    atomic.Bool
}

func (l *blockingLister) List(
	ctx context.Context,
	list client.ObjectList,
	options ...client.ListOption,
) error {
	if l.block.Load() {
		<-ctx.Done()
		return ctx.Err()
	}
	return l.delegate.List(ctx, list, options...)
}

func (l *switchableLister) List(
	ctx context.Context,
	list client.ObjectList,
	options ...client.ListOption,
) error {
	if l.fail.Load() {
		return errors.New("Kubernetes API unavailable")
	}
	return l.delegate.List(ctx, list, options...)
}

func newTestDiscovery(
	t *testing.T,
	kubernetesClient Lister,
	registry routing.ReplacingRegistry,
) *Discovery {
	t.Helper()
	modelDiscovery, err := New(
		kubernetesClient,
		registry,
		"inferops",
		minimumSyncInterval,
		log.New(io.Discard, "", 0),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	modelDiscovery.syncInterval = 10 * time.Millisecond
	return modelDiscovery
}

func TestNewRejectsTightSyncLoop(t *testing.T) {
	t.Parallel()
	_, err := New(
		alwaysFailLister{},
		routing.NewMemoryRegistry(),
		"inferops",
		minimumSyncInterval-time.Millisecond,
		log.New(io.Discard, "", 0),
	)
	if err == nil {
		t.Fatal("New() error = nil, want minimum sync interval error")
	}
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("corev1.AddToScheme(): %v", err)
	}
	if err := discoveryv1.AddToScheme(scheme); err != nil {
		t.Fatalf("discoveryv1.AddToScheme(): %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("v1alpha1.AddToScheme(): %v", err)
	}
	return scheme
}

func readyDeployment() *v1alpha1.ModelDeployment {
	const generation int64 = 3
	return &v1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "qwen-chat",
			Namespace:  "inferops",
			Generation: generation,
			UID:        "qwen-chat-uid",
		},
		Spec: v1alpha1.ModelDeploymentSpec{
			Activation: v1alpha1.ActivationSpec{
				DesiredState: v1alpha1.ActivationDesiredStateActive,
			},
			Routing: v1alpha1.RoutingSpec{
				Enabled:          true,
				OpenAICompatible: true,
			},
		},
		Status: v1alpha1.ModelDeploymentStatus{
			ObservedGeneration: generation,
			Phase:              v1alpha1.ModelDeploymentPhaseActive,
			ServiceName:        "qwen-chat-runtime",
			Replicas:           v1alpha1.ReplicaStatus{Desired: 1, Ready: 1},
			Model:              v1alpha1.ModelStatus{Loaded: true},
			Conditions: []v1alpha1.Condition{
				{
					Type:               v1alpha1.ConditionRuntimeReady,
					Status:             metav1.ConditionTrue,
					ObservedGeneration: generation,
				},
				{
					Type:               v1alpha1.ConditionModelLoaded,
					Status:             metav1.ConditionTrue,
					ObservedGeneration: generation,
				},
				{
					Type:               v1alpha1.ConditionRoutingReady,
					Status:             metav1.ConditionTrue,
					ObservedGeneration: generation,
				},
				{
					Type:               v1alpha1.ConditionReady,
					Status:             metav1.ConditionTrue,
					ObservedGeneration: generation,
				},
			},
		},
	}
}

func runtimeService() *corev1.Service {
	controller := true
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "qwen-chat-runtime",
			Namespace: "inferops",
			Labels: map[string]string{
				runtimecontract.ModelDeploymentLabel: "qwen-chat",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: v1alpha1.GroupVersion.String(),
					Kind:       "ModelDeployment",
					Name:       "qwen-chat",
					UID:        "qwen-chat-uid",
					Controller: &controller,
				},
			},
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "10.96.0.42",
			Selector: map[string]string{
				runtimecontract.ModelDeploymentLabel: "qwen-chat",
			},
			Ports: []corev1.ServicePort{
				{
					Name:       runtimecontract.HTTPPortName,
					Protocol:   corev1.ProtocolTCP,
					Port:       8000,
					TargetPort: intstr.FromString(runtimecontract.HTTPPortName),
				},
			},
		},
	}
}

func runtimeEndpointSlice() *discoveryv1.EndpointSlice {
	ready := true
	portName := runtimecontract.HTTPPortName
	port := int32(8000)
	protocol := corev1.ProtocolTCP
	return &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "qwen-chat-runtime-abc12",
			Namespace: "inferops",
			Labels: map[string]string{
				discoveryv1.LabelServiceName: "qwen-chat-runtime",
			},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Ports: []discoveryv1.EndpointPort{
			{
				Name:     &portName,
				Protocol: &protocol,
				Port:     &port,
			},
		},
		Endpoints: []discoveryv1.Endpoint{
			{
				Addresses: []string{"10.244.0.42"},
				Conditions: discoveryv1.EndpointConditions{
					Ready: &ready,
				},
			},
		},
	}
}

func setTestCondition(
	deployment *v1alpha1.ModelDeployment,
	conditionType string,
	conditionStatus metav1.ConditionStatus,
) {
	for index := range deployment.Status.Conditions {
		if deployment.Status.Conditions[index].Type == conditionType {
			deployment.Status.Conditions[index].Status = conditionStatus
			return
		}
	}
}

func waitFor(t *testing.T, ctx context.Context, condition func() bool, message string) {
	t.Helper()
	for !condition() {
		select {
		case <-ctx.Done():
			t.Fatalf("%s: %v", message, ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
}
