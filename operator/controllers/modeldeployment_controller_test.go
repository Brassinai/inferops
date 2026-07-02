package controllers

import (
	"context"
	"errors"
	"testing"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	"github.com/brassinai/inferops/operator/internal/events"
	"github.com/brassinai/inferops/operator/internal/runtime"
	"github.com/brassinai/inferops/operator/internal/status"
	"github.com/brassinai/inferops/operator/internal/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileValidInactiveDeployment(t *testing.T) {
	t.Parallel()

	md := &v1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-chat", Namespace: "default", Generation: 2},
		Spec: v1alpha1.ModelDeploymentSpec{
			Model: v1alpha1.ModelSpec{
				Repo:   "Qwen/Qwen2.5-7B-Instruct",
				Source: "huggingface",
			},
			Runtime: v1alpha1.RuntimeSpec{Ref: "nano-vllm"},
			Resources: v1alpha1.ResourceRequirements{
				CPU:    "4",
				Memory: "16Gi",
				GPU:    &v1alpha1.GPUResourceRequest{Count: 1},
			},
			Activation: v1alpha1.ActivationSpec{
				DesiredState: v1alpha1.ActivationDesiredStateInactive,
				DrainTimeout: "5m",
			},
			Cache: v1alpha1.CacheSpec{Path: "/var/lib/inferops/models"},
			Secrets: v1alpha1.SecretReferences{
				HuggingFaceTokenSecretName: "hf-token",
			},
		},
	}

	rt := &v1alpha1.ModelRuntime{
		ObjectMeta: metav1.ObjectMeta{Name: "nano-vllm", Namespace: "default"},
		Spec: v1alpha1.ModelRuntimeSpec{
			Engine:       "nano-vllm",
			Protocol:     "openai",
			DefaultImage: "ghcr.io/inferops/nano-vllm:v0.1.0",
			Port:         8000,
			HealthPath:   "/health",
		},
	}

	recorder := &events.FakeRecorder{}
	reconciler := newTestReconciler(rt, recorder)

	result, err := reconciler.Reconcile(context.Background(), md)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	if result.Status.ObservedGeneration != 2 {
		t.Errorf("observedGeneration = %d, want 2", result.Status.ObservedGeneration)
	}
	if result.Status.Phase != v1alpha1.ModelDeploymentPhasePending {
		t.Errorf("phase = %q, want Pending", result.Status.Phase)
	}

	assertCondition(t, result.Status.Conditions, v1alpha1.ConditionSpecValid, metav1.ConditionTrue, events.ReasonSpecValidated, 2)
	assertCondition(t, result.Status.Conditions, v1alpha1.ConditionRuntimeResolved, metav1.ConditionTrue, events.ReasonRuntimeResolved, 2)
	assertCondition(t, result.Status.Conditions, v1alpha1.ConditionSecretsReady, metav1.ConditionTrue, events.ReasonSecretsAvailable, 2)

	if len(recorder.Events) == 0 {
		t.Error("expected events to be recorded")
	}
	if result.Runtime.Image() != "ghcr.io/inferops/nano-vllm:v0.1.0" {
		t.Errorf("resolved runtime image = %q, want %q", result.Runtime.Image(), "ghcr.io/inferops/nano-vllm:v0.1.0")
	}
}

func TestReconcileBlocksOnInvalidSpec(t *testing.T) {
	t.Parallel()

	md := &v1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-chat", Namespace: "default", Generation: 1},
		Spec: v1alpha1.ModelDeploymentSpec{
			Model:   v1alpha1.ModelSpec{Repo: ""},
			Runtime: v1alpha1.RuntimeSpec{Ref: "nano-vllm"},
			Resources: v1alpha1.ResourceRequirements{
				CPU:    "4",
				Memory: "16Gi",
			},
		},
	}

	recorder := &events.FakeRecorder{}
	reconciler := newTestReconciler(nil, recorder)

	result, err := reconciler.Reconcile(context.Background(), md)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	if result.Status.Phase != v1alpha1.ModelDeploymentPhaseFailed {
		t.Errorf("phase = %q, want Failed", result.Status.Phase)
	}
	assertCondition(t, result.Status.Conditions, v1alpha1.ConditionSpecValid, metav1.ConditionFalse, v1alpha1.ReasonSpecInvalid, 1)

	foundWarning := false
	for _, event := range recorder.Events {
		if event.EventType == "Warning" && event.Reason == v1alpha1.ReasonSpecInvalid {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Error("expected a SpecInvalid warning event")
	}
}

func TestReconcileBlocksOnMissingRuntime(t *testing.T) {
	t.Parallel()

	md := &v1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-chat", Namespace: "default", Generation: 1},
		Spec: v1alpha1.ModelDeploymentSpec{
			Model:   v1alpha1.ModelSpec{Repo: "Qwen/Qwen2.5-7B-Instruct"},
			Runtime: v1alpha1.RuntimeSpec{Ref: "missing-runtime"},
			Resources: v1alpha1.ResourceRequirements{
				CPU:    "4",
				Memory: "16Gi",
				GPU:    &v1alpha1.GPUResourceRequest{Count: 1},
			},
		},
	}

	recorder := &events.FakeRecorder{}
	reconciler := newTestReconciler(nil, recorder)

	result, err := reconciler.Reconcile(context.Background(), md)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	if result.Status.Phase != v1alpha1.ModelDeploymentPhaseFailed {
		t.Errorf("phase = %q, want Failed", result.Status.Phase)
	}
	assertCondition(t, result.Status.Conditions, v1alpha1.ConditionRuntimeResolved, metav1.ConditionFalse, v1alpha1.ReasonRuntimeNotFound, 1)
	assertCondition(t, result.Status.Conditions, v1alpha1.ConditionSpecValid, metav1.ConditionUnknown, v1alpha1.ReasonRuntimeNotFound, 1)
	assertCondition(t, result.Status.Conditions, v1alpha1.ConditionSecretsReady, metav1.ConditionUnknown, v1alpha1.ReasonRuntimeNotFound, 1)
}

func TestReconcileBlocksOnMissingSecret(t *testing.T) {
	t.Parallel()

	md := &v1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-chat", Namespace: "default", Generation: 1},
		Spec: v1alpha1.ModelDeploymentSpec{
			Model: v1alpha1.ModelSpec{
				Repo:   "Qwen/Qwen2.5-7B-Instruct",
				Source: "huggingface",
			},
			Runtime: v1alpha1.RuntimeSpec{Ref: "nano-vllm"},
			Resources: v1alpha1.ResourceRequirements{
				CPU:    "4",
				Memory: "16Gi",
				GPU:    &v1alpha1.GPUResourceRequest{Count: 1},
			},
			Cache: v1alpha1.CacheSpec{Path: "/var/lib/inferops/models"},
		},
	}

	rt := &v1alpha1.ModelRuntime{
		ObjectMeta: metav1.ObjectMeta{Name: "nano-vllm", Namespace: "default"},
		Spec: v1alpha1.ModelRuntimeSpec{
			Engine:       "nano-vllm",
			Protocol:     "openai",
			DefaultImage: "ghcr.io/inferops/nano-vllm:v0.1.0",
		},
	}

	recorder := &events.FakeRecorder{}
	reconciler := newTestReconciler(rt, recorder)

	result, err := reconciler.Reconcile(context.Background(), md)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	if result.Status.Phase != v1alpha1.ModelDeploymentPhaseFailed {
		t.Errorf("phase = %q, want Failed", result.Status.Phase)
	}
	assertCondition(t, result.Status.Conditions, v1alpha1.ConditionSpecValid, metav1.ConditionFalse, v1alpha1.ReasonSecretRequired, 1)
	assertCondition(t, result.Status.Conditions, v1alpha1.ConditionSecretsReady, metav1.ConditionFalse, v1alpha1.ReasonSecretRequired, 1)

	foundWarning := false
	for _, event := range recorder.Events {
		if event.EventType == "Warning" && event.Reason == v1alpha1.ReasonSecretRequired {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Error("expected a SecretRequired warning event")
	}
}

func TestReconcileBlocksOnCachePathOutsideRoot(t *testing.T) {
	t.Parallel()

	md := &v1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-chat", Namespace: "default", Generation: 1},
		Spec: v1alpha1.ModelDeploymentSpec{
			Model: v1alpha1.ModelSpec{
				Repo:   "Qwen/Qwen2.5-7B-Instruct",
				Source: "huggingface",
			},
			Runtime: v1alpha1.RuntimeSpec{Ref: "nano-vllm"},
			Resources: v1alpha1.ResourceRequirements{
				CPU:    "4",
				Memory: "16Gi",
				GPU:    &v1alpha1.GPUResourceRequest{Count: 1},
			},
			Cache: v1alpha1.CacheSpec{Path: "/tmp/models"},
			Secrets: v1alpha1.SecretReferences{
				HuggingFaceTokenSecretName: "hf-token",
			},
		},
	}

	rt := &v1alpha1.ModelRuntime{
		ObjectMeta: metav1.ObjectMeta{Name: "nano-vllm", Namespace: "default"},
		Spec: v1alpha1.ModelRuntimeSpec{
			Engine:       "nano-vllm",
			Protocol:     "openai",
			DefaultImage: "ghcr.io/inferops/nano-vllm:v0.1.0",
		},
	}

	recorder := &events.FakeRecorder{}
	reconciler := newTestReconciler(rt, recorder)

	result, err := reconciler.Reconcile(context.Background(), md)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	if result.Status.Phase != v1alpha1.ModelDeploymentPhaseFailed {
		t.Errorf("phase = %q, want Failed", result.Status.Phase)
	}
	assertCondition(t, result.Status.Conditions, v1alpha1.ConditionSpecValid, metav1.ConditionFalse, v1alpha1.ReasonInvalidCachePath, 1)
	assertCondition(t, result.Status.Conditions, v1alpha1.ConditionSecretsReady, metav1.ConditionTrue, v1alpha1.ReasonSecretsAvailable, 1)

	foundWarning := false
	for _, event := range recorder.Events {
		if event.EventType == "Warning" && event.Reason == v1alpha1.ReasonInvalidCachePath {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Error("expected an InvalidCachePath warning event")
	}
}

func TestReconcileReturnsTransientRuntimeLookupError(t *testing.T) {
	t.Parallel()

	getErr := errors.New("Kubernetes API unavailable")
	recorder := &events.FakeRecorder{}
	reconciler := newTestReconcilerWithGetter(fakeRuntimeGetter{err: getErr}, recorder)

	_, err := reconciler.Reconcile(context.Background(), validTestDeployment())
	if !errors.Is(err, getErr) {
		t.Fatalf("Reconcile() error = %v, want wrapped getter error", err)
	}
	if len(recorder.Events) != 0 {
		t.Errorf("recorded %d events for a transient lookup error, want 0", len(recorder.Events))
	}
}

func TestReconcileRecoversValidationOwnedFailedPhase(t *testing.T) {
	t.Parallel()

	md := validTestDeployment()
	md.Spec.Secrets.HuggingFaceTokenSecretName = ""
	reconciler := newTestReconciler(validTestRuntime(), &events.FakeRecorder{})

	failed, err := reconciler.Reconcile(context.Background(), md)
	if err != nil {
		t.Fatalf("first Reconcile() error = %v", err)
	}
	if failed.Status.Phase != v1alpha1.ModelDeploymentPhaseFailed {
		t.Fatalf("first phase = %q, want Failed", failed.Status.Phase)
	}

	md.Status = failed.Status
	md.Generation++
	md.Spec.Secrets.HuggingFaceTokenSecretName = "hf-token"
	recovered, err := reconciler.Reconcile(context.Background(), md)
	if err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}
	if recovered.Status.Phase != v1alpha1.ModelDeploymentPhasePending {
		t.Errorf("recovered phase = %q, want Pending", recovered.Status.Phase)
	}
	assertCondition(t, recovered.Status.Conditions, v1alpha1.ConditionSpecValid, metav1.ConditionTrue, v1alpha1.ReasonSpecValidated, md.Generation)
}

func TestReconcileDoesNotRepeatEventsForUnchangedStatus(t *testing.T) {
	t.Parallel()

	md := validTestDeployment()
	recorder := &events.FakeRecorder{}
	reconciler := newTestReconciler(validTestRuntime(), recorder)

	first, err := reconciler.Reconcile(context.Background(), md)
	if err != nil {
		t.Fatalf("first Reconcile() error = %v", err)
	}
	if len(recorder.Events) == 0 {
		t.Fatal("first Reconcile() recorded no events")
	}

	md.Status = first.Status
	recorder.Events = nil
	_, err = reconciler.Reconcile(context.Background(), md)
	if err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}
	if len(recorder.Events) != 0 {
		t.Errorf("second Reconcile() recorded %d duplicate events, want 0", len(recorder.Events))
	}
}

func newTestReconciler(rt *v1alpha1.ModelRuntime, recorder events.Recorder) *ModelDeploymentReconciler {
	return newTestReconcilerWithGetter(fakeRuntimeGetter{runtime: rt}, recorder)
}

func newTestReconcilerWithGetter(getter runtime.RuntimeGetter, recorder events.Recorder) *ModelDeploymentReconciler {
	validator, err := validation.NewReconciliationValidator("/var/lib/inferops/models")
	if err != nil {
		panic(err)
	}
	return NewModelDeploymentReconciler(
		runtime.NewResolver(getter),
		validator,
		recorder,
	)
}

func validTestDeployment() *v1alpha1.ModelDeployment {
	return &v1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-chat", Namespace: "default", Generation: 1},
		Spec: v1alpha1.ModelDeploymentSpec{
			Model: v1alpha1.ModelSpec{
				Repo:   "Qwen/Qwen2.5-7B-Instruct",
				Source: "huggingface",
			},
			Runtime: v1alpha1.RuntimeSpec{Ref: "nano-vllm"},
			Resources: v1alpha1.ResourceRequirements{
				CPU:    "4",
				Memory: "16Gi",
				GPU:    &v1alpha1.GPUResourceRequest{Count: 1},
			},
			Activation: v1alpha1.ActivationSpec{DrainTimeout: "5m"},
			Cache:      v1alpha1.CacheSpec{Path: "/var/lib/inferops/models"},
			Secrets: v1alpha1.SecretReferences{
				HuggingFaceTokenSecretName: "hf-token",
			},
		},
	}
}

func validTestRuntime() *v1alpha1.ModelRuntime {
	return &v1alpha1.ModelRuntime{
		ObjectMeta: metav1.ObjectMeta{Name: "nano-vllm", Namespace: "default"},
		Spec: v1alpha1.ModelRuntimeSpec{
			Engine:       "nano-vllm",
			Protocol:     "openai",
			DefaultImage: "ghcr.io/inferops/nano-vllm:v0.1.0",
			Port:         8000,
			HealthPath:   "/health",
		},
	}
}

func assertCondition(t *testing.T, conditions []v1alpha1.Condition, conditionType string, wantStatus metav1.ConditionStatus, wantReason string, wantGeneration int64) {
	t.Helper()
	cond, found := status.FindCondition(conditions, conditionType)
	if !found {
		t.Fatalf("condition %q not found", conditionType)
	}
	if cond.Status != wantStatus {
		t.Errorf("condition %q status = %q, want %q", conditionType, cond.Status, wantStatus)
	}
	if cond.Reason != wantReason {
		t.Errorf("condition %q reason = %q, want %q", conditionType, cond.Reason, wantReason)
	}
	if cond.ObservedGeneration != wantGeneration {
		t.Errorf("condition %q observedGeneration = %d, want %d", conditionType, cond.ObservedGeneration, wantGeneration)
	}
}

type fakeRuntimeGetter struct {
	runtime *v1alpha1.ModelRuntime
	err     error
}

func (f fakeRuntimeGetter) GetRuntime(_ context.Context, _, _ string) (*v1alpha1.ModelRuntime, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.runtime == nil {
		return nil, runtime.ErrRuntimeNotFound
	}
	return f.runtime, nil
}

// Compile-time check that fakeRuntimeGetter implements the interface.
var _ runtime.RuntimeGetter = fakeRuntimeGetter{}
