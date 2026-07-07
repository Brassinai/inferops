package proxy

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brassinai/inferops/gateway/internal/auth"
	"github.com/brassinai/inferops/gateway/internal/discovery"
	"github.com/brassinai/inferops/gateway/internal/routing"
	"github.com/brassinai/inferops/internal/runtimecontract"
	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGatewayIntegratesKubernetesDiscoveryAuthRuntimeAndDrain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	deployment := integrationDeployment(v1alpha1.ModelDeploymentPhaseActivating)
	kubernetesClient := fake.NewClientBuilder().
		WithScheme(integrationScheme(t)).
		WithStatusSubresource(&v1alpha1.ModelDeployment{}).
		WithObjects(deployment, integrationService(), integrationEndpointSlice()).
		Build()
	registry := routing.NewMemoryRegistry()
	modelDiscovery, err := discovery.New(
		kubernetesClient,
		registry,
		"inferops",
		time.Second,
		log.New(io.Discard, "", 0),
	)
	if err != nil {
		t.Fatalf("discovery.New(): %v", err)
	}

	var runtimeRequests atomic.Int32
	runtimeServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		runtimeRequests.Add(1)
		if got := request.Header.Get("Authorization"); got != "" {
			t.Errorf("runtime received gateway credential %q", got)
		}
		if request.URL.Path != "/v1/chat/completions" {
			t.Errorf("runtime path = %q, want /v1/chat/completions", request.URL.Path)
		}
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(response, "data: complete\n\n")
	}))
	defer runtimeServer.Close()

	gatewayProxy, err := New(registry)
	if err != nil {
		t.Fatalf("proxy.New(): %v", err)
	}
	gatewayProxy.transport = &integrationTransport{
		target: parseURL(t, runtimeServer.URL),
		base:   runtimeServer.Client().Transport,
	}
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("integration-token\n"), 0o600); err != nil {
		t.Fatalf("write auth token: %v", err)
	}
	tokenSource, err := auth.NewFileTokenSource(tokenPath)
	if err != nil {
		t.Fatalf("auth.NewFileTokenSource(): %v", err)
	}
	authMiddleware, err := auth.New(tokenSource)
	if err != nil {
		t.Fatalf("auth.New(): %v", err)
	}
	handler := authMiddleware.Wrap(gatewayProxy)

	if err := modelDiscovery.Sync(ctx); err != nil {
		t.Fatalf("cold-start discovery sync: %v", err)
	}
	coldStart := integrationRequest(handler, "integration-token")
	assertAPIError(t, coldStart, http.StatusServiceUnavailable, "model_activating")
	if coldStart.Header().Get("Retry-After") == "" {
		t.Fatal("cold-start response omitted Retry-After")
	}

	unauthorized := integrationRequest(handler, "wrong-token")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", unauthorized.Code, http.StatusUnauthorized)
	}

	var current v1alpha1.ModelDeployment
	key := types.NamespacedName{Namespace: "inferops", Name: "qwen-chat"}
	if err := kubernetesClient.Get(ctx, key, &current); err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	current.Status = integrationDeployment(v1alpha1.ModelDeploymentPhaseActive).Status
	if err := kubernetesClient.Status().Update(ctx, &current); err != nil {
		t.Fatalf("publish ready status: %v", err)
	}
	if err := modelDiscovery.Sync(ctx); err != nil {
		t.Fatalf("ready discovery sync: %v", err)
	}

	ready := integrationRequest(handler, "integration-token")
	if ready.Code != http.StatusOK {
		t.Fatalf("ready status = %d, want %d; body=%s", ready.Code, http.StatusOK, ready.Body.String())
	}
	if ready.Body.String() != "data: complete\n\n" {
		t.Errorf("streamed body = %q", ready.Body.String())
	}
	if ready.Header().Get("X-Accel-Buffering") != "no" {
		t.Error("ready response did not disable proxy buffering")
	}

	endpointSlice := integrationEndpointSlice()
	if err := kubernetesClient.Delete(ctx, endpointSlice); err != nil {
		t.Fatalf("delete ready EndpointSlice: %v", err)
	}
	if err := modelDiscovery.Sync(ctx); err != nil {
		t.Fatalf("unavailable discovery sync: %v", err)
	}
	unavailable := integrationRequest(handler, "integration-token")
	assertAPIError(t, unavailable, http.StatusServiceUnavailable, "model_unavailable")
	if got := runtimeRequests.Load(); got != 1 {
		t.Fatalf("unavailable model reached runtime; requests = %d", got)
	}
	if err := kubernetesClient.Create(ctx, endpointSlice); err != nil {
		t.Fatalf("restore ready EndpointSlice: %v", err)
	}

	if err := kubernetesClient.Get(ctx, key, &current); err != nil {
		t.Fatalf("get ready deployment: %v", err)
	}
	current.Status.Phase = v1alpha1.ModelDeploymentPhaseDraining
	if err := kubernetesClient.Status().Update(ctx, &current); err != nil {
		t.Fatalf("publish draining status: %v", err)
	}
	if err := modelDiscovery.Sync(ctx); err != nil {
		t.Fatalf("draining discovery sync: %v", err)
	}
	draining := integrationRequest(handler, "integration-token")
	assertAPIError(t, draining, http.StatusServiceUnavailable, "model_draining")
	if got := runtimeRequests.Load(); got != 1 {
		t.Fatalf("runtime requests = %d, want only the ready request", got)
	}
}

type integrationTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (t *integrationTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.URL.Scheme = t.target.Scheme
	clone.URL.Host = t.target.Host
	clone.Host = t.target.Host
	return t.base.RoundTrip(clone)
}

func integrationRequest(handler http.Handler, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(
		http.MethodPost,
		"/models/qwen-chat/v1/chat/completions",
		nil,
	)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func integrationScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	for name, add := range map[string]func(*runtime.Scheme) error{
		"core":      corev1.AddToScheme,
		"discovery": discoveryv1.AddToScheme,
		"inferops":  v1alpha1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatalf("register %s scheme: %v", name, err)
		}
	}
	return scheme
}

func integrationDeployment(phase v1alpha1.ModelDeploymentPhase) *v1alpha1.ModelDeployment {
	const generation int64 = 2
	deployment := &v1alpha1.ModelDeployment{
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
			Phase:              phase,
			ServiceName:        "qwen-chat-runtime",
			Replicas:           v1alpha1.ReplicaStatus{Desired: 1, Ready: 1},
			Model:              v1alpha1.ModelStatus{Loaded: true},
		},
	}
	for _, conditionType := range []string{
		v1alpha1.ConditionRuntimeReady,
		v1alpha1.ConditionModelLoaded,
		v1alpha1.ConditionRoutingReady,
		v1alpha1.ConditionReady,
	} {
		deployment.Status.Conditions = append(
			deployment.Status.Conditions,
			v1alpha1.Condition{
				Type:               conditionType,
				Status:             metav1.ConditionTrue,
				ObservedGeneration: generation,
			},
		)
	}
	return deployment
}

func integrationService() *corev1.Service {
	controller := true
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "qwen-chat-runtime",
			Namespace: "inferops",
			Labels: map[string]string{
				runtimecontract.ModelDeploymentLabel: "qwen-chat",
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: v1alpha1.GroupVersion.String(),
				Kind:       "ModelDeployment",
				Name:       "qwen-chat",
				UID:        "qwen-chat-uid",
				Controller: &controller,
			}},
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "10.96.0.42",
			Selector: map[string]string{
				runtimecontract.ModelDeploymentLabel: "qwen-chat",
			},
			Ports: []corev1.ServicePort{{
				Name:       runtimecontract.HTTPPortName,
				Protocol:   corev1.ProtocolTCP,
				Port:       8000,
				TargetPort: intstr.FromString(runtimecontract.HTTPPortName),
			}},
		},
	}
}

func integrationEndpointSlice() *discoveryv1.EndpointSlice {
	ready := true
	portName := runtimecontract.HTTPPortName
	port := int32(8000)
	protocol := corev1.ProtocolTCP
	return &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "qwen-chat-runtime-test",
			Namespace: "inferops",
			Labels: map[string]string{
				discoveryv1.LabelServiceName: "qwen-chat-runtime",
			},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Ports: []discoveryv1.EndpointPort{{
			Name:     &portName,
			Protocol: &protocol,
			Port:     &port,
		}},
		Endpoints: []discoveryv1.Endpoint{{
			Addresses: []string{"10.244.0.42"},
			Conditions: discoveryv1.EndpointConditions{
				Ready: &ready,
			},
		}},
	}
}
