package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestSnapshotSanitizesAndAggregatesClusterState(t *testing.T) {
	scheme := testScheme(t)
	weight := int32(20)
	kubernetesClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			&v1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "inferops-system"},
				Spec: v1alpha1.ModelDeploymentSpec{
					Model:   v1alpha1.ModelSpec{Repo: "Qwen/Qwen2.5-0.5B-Instruct"},
					Runtime: v1alpha1.RuntimeSpec{Ref: "vllm"},
					Resources: v1alpha1.ResourceRequirements{
						GPU: &v1alpha1.GPUResourceRequest{Count: 1, Vendor: "nvidia", Type: "l4"},
					},
					Activation: v1alpha1.ActivationSpec{
						DesiredState: v1alpha1.ActivationDesiredStateActive,
						WhenFull:     v1alpha1.ActivationWhenFullQueue,
						DrainTimeout: "30s",
					},
					Scaling: v1alpha1.ScalingSpec{MinReplicas: 1, MaxReplicas: 2, TargetPendingRequests: 8},
					Routing: v1alpha1.RoutingSpec{
						Enabled:          true,
						OpenAICompatible: true,
						Path:             "/models/qwen",
						Policy:           v1alpha1.RoutingPolicySpec{RoutingStrategy: v1alpha1.RoutingStrategyWeighted, Weight: &weight},
					},
					Secrets: v1alpha1.SecretReferences{HuggingFaceTokenSecretName: "hf-token"},
				},
				Status: v1alpha1.ModelDeploymentStatus{
					Phase:        v1alpha1.ModelDeploymentPhaseActive,
					Endpoint:     "http://inferops-gateway/models/qwen",
					ServiceName:  "qwen-runtime",
					AssignedNode: "gpu-a",
					AssignedGPUs: []string{"0"},
					Replicas:     v1alpha1.ReplicaStatus{Desired: 1, Ready: 1},
					Scaling:      v1alpha1.ScalingStatus{DesiredReplicas: 1, PendingRequests: 3, Reason: "MetricsObserved"},
					Cache:        v1alpha1.ModelCacheSummary{State: "Ready", NodeName: "gpu-a", Path: "/var/lib/inferops/models/qwen"},
					Conditions: []v1alpha1.Condition{{
						Type:               v1alpha1.ConditionReady,
						Status:             metav1.ConditionTrue,
						Reason:             v1alpha1.ReasonRuntimeReady,
						LastTransitionTime: metav1.NewTime(time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)),
					}},
				},
			},
			&v1alpha1.ModelCache{
				ObjectMeta: metav1.ObjectMeta{Name: "qwen-cache", Namespace: "inferops-system"},
				Spec: v1alpha1.ModelCacheSpec{
					ModelRepo: "Qwen/Qwen2.5-0.5B-Instruct",
					Revision:  "main",
					Storage:   v1alpha1.ModelCacheStorage{Type: "local", Size: "20Gi", Path: "/var/lib/inferops/models/qwen"},
					SecretRef: "hf-token",
				},
				Status: v1alpha1.ModelCacheStatus{
					Phase:        v1alpha1.ModelCachePhaseReady,
					Revision:     "main",
					NodeName:     "gpu-a",
					Path:         "/var/lib/inferops/models/qwen",
					Size:         "4Gi",
					ReservedSize: "20Gi",
				},
			},
			&v1alpha1.ModelRuntime{
				ObjectMeta: metav1.ObjectMeta{Name: "vllm", Namespace: "inferops-system"},
				Spec: v1alpha1.ModelRuntimeSpec{
					Engine: "vllm", Protocol: "openai", DefaultImage: "ghcr.io/inferops/vllm:0.0.0",
					Port: 8000, HealthPath: "/health", MetricsPath: "/metrics",
				},
				Status: v1alpha1.ModelRuntimeStatus{Phase: v1alpha1.ModelRuntimePhaseReady},
			},
			&corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "qwen-runtime", Namespace: "inferops-system"},
				Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.0.0.12"},
			},
			&corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "gpu-a"},
				Status: corev1.NodeStatus{
					Capacity:    corev1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
					Allocatable: corev1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
				},
			},
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "qwen-runtime-0", Namespace: "inferops-system"},
				Spec: corev1.PodSpec{
					NodeName: "gpu-a",
					Containers: []corev1.Container{{
						Name:      "runtime",
						Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")}},
					}},
				},
				Status: corev1.PodStatus{Phase: corev1.PodRunning},
			},
			&corev1.Event{
				ObjectMeta:     metav1.ObjectMeta{Name: "qwen-ready", Namespace: "inferops-system"},
				Type:           corev1.EventTypeNormal,
				Reason:         "RuntimeReady",
				Message:        "runtime is ready",
				Count:          2,
				LastTimestamp:  metav1.NewTime(time.Date(2026, 7, 9, 12, 5, 0, 0, time.UTC)),
				InvolvedObject: corev1.ObjectReference{Kind: "ModelDeployment", Name: "qwen"},
			},
		).
		Build()
	server, err := New(kubernetesClient, Options{
		Namespace:      "inferops-system",
		GatewayBaseURL: "https://models.example.com",
		PrometheusURL:  "https://prom.example.com",
		Now:            func() time.Time { return time.Date(2026, 7, 9, 13, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}

	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/snapshot", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	var snapshot Snapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}

	if snapshot.Summary.Deployments != 1 || snapshot.Summary.Caches != 1 || snapshot.Summary.Runtimes != 1 {
		t.Fatalf("summary = %#v", snapshot.Summary)
	}
	deployment := snapshot.Deployments[0]
	if deployment.Endpoint.GatewayURL != "https://models.example.com/models/qwen" {
		t.Fatalf("gateway URL = %q", deployment.Endpoint.GatewayURL)
	}
	if deployment.GPU.AssignedNode != "gpu-a" || deployment.GPU.RequestedCount != 1 {
		t.Fatalf("GPU assignment = %#v", deployment.GPU)
	}
	if deployment.Scaling.PendingRequests != 3 || deployment.Routing.Weight != 20 {
		t.Fatalf("deployment summary = %#v", deployment)
	}
	if snapshot.GPUs[0].Requested != "1" {
		t.Fatalf("GPU requested = %q", snapshot.GPUs[0].Requested)
	}
	if snapshot.Events[0].Reason != "RuntimeReady" {
		t.Fatalf("events = %#v", snapshot.Events)
	}
}

func TestGeneratedYAMLDoesNotExposeSecretReferencesOrStatus(t *testing.T) {
	scheme := testScheme(t)
	kubernetesClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&v1alpha1.ModelDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "qwen",
				Namespace: "inferops-system",
				Annotations: map[string]string{
					"kubectl.kubernetes.io/last-applied-configuration": `{"spec":{"secrets":{"huggingFaceTokenSecretName":"hf-token"}}}`,
					"inferops.dev/operator-note":                       "do-not-copy-live-annotations",
				},
			},
			Spec: v1alpha1.ModelDeploymentSpec{
				Model:   v1alpha1.ModelSpec{Repo: "Qwen/Qwen2.5-0.5B-Instruct"},
				Runtime: v1alpha1.RuntimeSpec{Ref: "vllm"},
				Secrets: v1alpha1.SecretReferences{
					HuggingFaceTokenSecretName: "hf-token",
				},
			},
			Status: v1alpha1.ModelDeploymentStatus{Phase: v1alpha1.ModelDeploymentPhaseActive},
		}).
		Build()
	server, err := New(kubernetesClient, Options{Namespace: "inferops-system"})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}

	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/generated-yaml", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	var response GeneratedYAMLResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	rendered := response.Deployments[0].YAML
	for _, forbidden := range []string{
		"hf-token",
		"status:",
		"resourceVersion:",
		"uid:",
		"annotations:",
		"last-applied-configuration",
		"do-not-copy-live-annotations",
	} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("generated YAML contains %q:\n%s", forbidden, rendered)
		}
	}
	if !strings.Contains(rendered, "kind: ModelDeployment") {
		t.Fatalf("generated YAML missing kind:\n%s", rendered)
	}
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add InferOps scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	return scheme
}
