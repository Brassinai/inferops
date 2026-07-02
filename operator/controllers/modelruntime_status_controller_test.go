package controllers

import (
	"context"
	"testing"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	"github.com/brassinai/inferops/operator/internal/status"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestModelRuntimeControllerStatus(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		image      string
		wantPhase  v1alpha1.ModelRuntimePhase
		wantStatus metav1.ConditionStatus
		wantReason string
	}{
		{
			name: "valid", image: "ghcr.io/inferops/runtime:v1.0.0",
			wantPhase:  v1alpha1.ModelRuntimePhaseReady,
			wantStatus: metav1.ConditionTrue, wantReason: v1alpha1.RuntimeReasonValidated,
		},
		{
			name: "mutable image", image: "ghcr.io/inferops/runtime:latest",
			wantPhase:  v1alpha1.ModelRuntimePhaseFailed,
			wantStatus: metav1.ConditionFalse, wantReason: v1alpha1.RuntimeReasonInvalid,
		},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			scheme := runtime.NewScheme()
			if err := v1alpha1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			modelRuntime := &v1alpha1.ModelRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name: "runtime", Namespace: "default", UID: "runtime-uid", Generation: 2,
				},
				Spec: v1alpha1.ModelRuntimeSpec{
					Engine: "test", Protocol: "openai", DefaultImage: tt.image,
					Port: 8000, HealthPath: "/health",
				},
			}
			c := fake.NewClientBuilder().WithScheme(scheme).
				WithStatusSubresource(&v1alpha1.ModelRuntime{}).
				WithObjects(modelRuntime).Build()
			reconciler, err := NewModelRuntimeController(c, record.NewFakeRecorder(10), nil)
			if err != nil {
				t.Fatal(err)
			}
			request := ctrl.Request{NamespacedName: types.NamespacedName{
				Namespace: modelRuntime.Namespace, Name: modelRuntime.Name,
			}}
			if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
				t.Fatalf("Reconcile() error = %v", err)
			}
			var updated v1alpha1.ModelRuntime
			if err := c.Get(context.Background(), request.NamespacedName, &updated); err != nil {
				t.Fatal(err)
			}
			if updated.Status.Phase != tt.wantPhase {
				t.Errorf("phase = %q, want %q", updated.Status.Phase, tt.wantPhase)
			}
			condition, found := status.FindCondition(updated.Status.Conditions, v1alpha1.RuntimeConditionReady)
			if !found || condition.Status != tt.wantStatus || condition.Reason != tt.wantReason {
				t.Errorf("Ready condition = %#v, found=%t", condition, found)
			}

			before := updated.ResourceVersion
			if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
				t.Fatalf("second Reconcile() error = %v", err)
			}
			if err := c.Get(context.Background(), request.NamespacedName, &updated); err != nil {
				t.Fatal(err)
			}
			if updated.ResourceVersion != before {
				t.Errorf("unchanged reconcile wrote status: resourceVersion %q -> %q", before, updated.ResourceVersion)
			}
		})
	}
}
