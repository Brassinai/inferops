package admission

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	cradmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func TestModelDeploymentAdmission(t *testing.T) {
	t.Parallel()

	valid := &v1alpha1.ModelDeployment{
		Spec: v1alpha1.ModelDeploymentSpec{
			Model:     v1alpha1.ModelSpec{Repo: "Qwen/Qwen2.5-7B-Instruct"},
			Runtime:   v1alpha1.RuntimeSpec{Ref: "nano-vllm"},
			Resources: v1alpha1.ResourceRequirements{CPU: "4", Memory: "16Gi"},
			Scaling:   v1alpha1.ScalingSpec{MaxReplicas: 1},
		},
	}
	valid.Name = "qwen"
	invalid := valid.DeepCopy()
	invalid.Spec.Runtime.TensorParallelSize = 2

	handler := newModelDeploymentHandler(testDecoder(t))
	tests := []struct {
		name        string
		object      runtime.Object
		operation   admissionv1.Operation
		allowed     bool
		messagePart string
	}{
		{
			name:      "valid create",
			object:    valid,
			operation: admissionv1.Create,
			allowed:   true,
		},
		{
			name:        "invalid update",
			object:      invalid,
			operation:   admissionv1.Update,
			allowed:     false,
			messagePart: "tensorParallelSize requires spec.resources.gpu",
		},
		{
			name:      "delete",
			operation: admissionv1.Delete,
			allowed:   true,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := admissionRequest(t, test.operation, test.object)
			response := handler.Handle(context.Background(), request)
			if response.Allowed != test.allowed {
				t.Fatalf("Allowed = %t, want %t; result=%#v", response.Allowed, test.allowed, response.Result)
			}
			if test.messagePart != "" &&
				(response.Result == nil || !strings.Contains(response.Result.Message, test.messagePart)) {
				t.Fatalf("message = %#v, want containing %q", response.Result, test.messagePart)
			}
		})
	}
}

func TestModelRuntimeAndCacheAdmission(t *testing.T) {
	t.Parallel()

	modelRuntime := &v1alpha1.ModelRuntime{
		Spec: v1alpha1.ModelRuntimeSpec{
			Engine:       "nano-vllm",
			Protocol:     "openai",
			DefaultImage: "ghcr.io/inferops/nano-vllm:0.0.0",
			Port:         8000,
			HealthPath:   "/health",
		},
	}
	modelRuntime.Name = "nano-vllm"
	cache := &v1alpha1.ModelCache{
		Spec: v1alpha1.ModelCacheSpec{
			ModelRepo: "Qwen/Qwen2.5-7B-Instruct",
			Storage: v1alpha1.ModelCacheStorage{
				Type: "nodeLocal",
				Size: "100Gi",
				Path: "/var/lib/inferops/models/qwen",
			},
		},
	}
	cache.Name = "qwen-cache"

	tests := []struct {
		name    string
		handler cradmission.Handler
		object  runtime.Object
	}{
		{name: "runtime", handler: newModelRuntimeHandler(testDecoder(t)), object: modelRuntime},
		{name: "cache", handler: newModelCacheHandler(testDecoder(t)), object: cache},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response := test.handler.Handle(
				context.Background(),
				admissionRequest(t, admissionv1.Create, test.object),
			)
			if !response.Allowed {
				t.Fatalf("admission denied valid %s: %#v", test.name, response.Result)
			}
		})
	}
}

func TestAdmissionRejectsMalformedObject(t *testing.T) {
	t.Parallel()

	handler := newModelDeploymentHandler(testDecoder(t))
	response := handler.Handle(context.Background(), cradmission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			UID:       types.UID("request"),
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: []byte("{")},
		},
	})
	if response.Allowed {
		t.Fatal("malformed object was allowed")
	}
	if response.Result == nil || response.Result.Code != 400 {
		t.Fatalf("result = %#v, want HTTP 400", response.Result)
	}
}

func TestValidationWebhookServesAdmissionReviewOverTLS(t *testing.T) {
	t.Parallel()

	deployment := &v1alpha1.ModelDeployment{
		Spec: v1alpha1.ModelDeploymentSpec{
			Model:     v1alpha1.ModelSpec{Repo: "Qwen/Qwen2.5-7B-Instruct"},
			Runtime:   v1alpha1.RuntimeSpec{Ref: "llama-cpp"},
			Resources: v1alpha1.ResourceRequirements{CPU: "2", Memory: "4Gi"},
			Scaling:   v1alpha1.ScalingSpec{MaxReplicas: 1},
		},
	}
	deployment.Name = "cpu-model"
	request := admissionRequest(t, admissionv1.Create, deployment)
	review := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: admissionv1.SchemeGroupVersion.String(),
			Kind:       "AdmissionReview",
		},
		Request: &request.AdmissionRequest,
	}
	payload, err := json.Marshal(review)
	if err != nil {
		t.Fatalf("marshal AdmissionReview: %v", err)
	}

	server := httptest.NewTLSServer(
		validatingWebhook(newModelDeploymentHandler(testDecoder(t))),
	)
	defer server.Close()
	response, err := server.Client().Post(
		server.URL,
		"application/json",
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatalf("POST AdmissionReview: %v", err)
	}
	defer response.Body.Close()

	var result admissionv1.AdmissionReview
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode AdmissionReview response: %v", err)
	}
	if result.Response == nil || !result.Response.Allowed {
		t.Fatalf("AdmissionReview response = %#v, want allowed", result.Response)
	}
	if result.Response.UID != request.UID {
		t.Fatalf("response UID = %q, want %q", result.Response.UID, request.UID)
	}
}

func testDecoder(t *testing.T) cradmission.Decoder {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}
	return cradmission.NewDecoder(scheme)
}

func admissionRequest(
	t *testing.T,
	operation admissionv1.Operation,
	object runtime.Object,
) cradmission.Request {
	t.Helper()
	var raw []byte
	if object != nil {
		var err error
		raw, err = json.Marshal(object)
		if err != nil {
			t.Fatalf("marshal admission object: %v", err)
		}
	}
	return cradmission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			UID:       types.UID("request"),
			Operation: operation,
			Object:    runtime.RawExtension{Raw: raw},
		},
	}
}
