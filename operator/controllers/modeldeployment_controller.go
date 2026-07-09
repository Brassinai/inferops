package controllers

import (
	"context"
	"errors"
	"fmt"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	"github.com/brassinai/inferops/operator/internal/events"
	"github.com/brassinai/inferops/operator/internal/runtime"
	"github.com/brassinai/inferops/operator/internal/status"
	"github.com/brassinai/inferops/operator/internal/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ModelDeploymentReconciler owns reconciliation for ModelDeployment resources.
type ModelDeploymentReconciler struct {
	resolver  *runtime.Resolver
	validator *validation.ReconciliationValidator
	recorder  events.Recorder
}

// NewModelDeploymentReconciler creates a reconciler with the required dependencies.
func NewModelDeploymentReconciler(
	resolver *runtime.Resolver,
	validator *validation.ReconciliationValidator,
	recorder events.Recorder,
) *ModelDeploymentReconciler {
	if recorder == nil {
		recorder = events.NoOpRecorder{}
	}
	return &ModelDeploymentReconciler{
		resolver:  resolver,
		validator: validator,
		recorder:  recorder,
	}
}

// ReconcileResult contains the observed status and resolved runtime after
// reconciliation.  Downstream reconcilers can use ResolvedRuntime without
// resolving the runtime a second time.
type ReconcileResult struct {
	Status  v1alpha1.ModelDeploymentStatus
	Runtime runtime.ResolvedRuntime
}

// Reconcile validates the ModelDeployment, resolves its runtime, and returns the
// observed status.  It does not create or update managed workloads; that is the
// responsibility of downstream reconcilers.
func (r *ModelDeploymentReconciler) Reconcile(ctx context.Context, deployment *v1alpha1.ModelDeployment) (ReconcileResult, error) {
	if err := ctx.Err(); err != nil {
		return ReconcileResult{}, err
	}
	if deployment == nil {
		return ReconcileResult{}, errors.New("model deployment is required")
	}
	if r == nil || r.resolver == nil {
		return ReconcileResult{}, errors.New("runtime resolver is required")
	}
	if r.validator == nil {
		return ReconcileResult{}, errors.New("reconciliation validator is required")
	}
	if r.recorder == nil {
		return ReconcileResult{}, errors.New("event recorder is required")
	}

	specChangedAfterFailure := deployment.Status.Phase == v1alpha1.ModelDeploymentPhaseFailed &&
		deployment.Generation > deployment.Status.ObservedGeneration
	observed := deployment.Status.DeepCopy()
	observed.ObservedGeneration = deployment.Generation
	wasValidationBlocked := validationBlocked(*observed)

	// Static validation covers fields and enums expressible without cluster state.
	if err := validation.ValidateModelDeployment(*deployment); err != nil {
		if setValidationFailed(observed, err.Error()) {
			r.recorder.Warningf(deployment, v1alpha1.ReasonSpecInvalid, "ModelDeployment spec is invalid: %v", err)
		}
		setRuntimeUnknown(observed, v1alpha1.ReasonSpecInvalid, "Static validation failed before runtime resolution")
		setSecretsUnknown(observed, v1alpha1.ReasonSpecInvalid, "Static validation failed before secret checks")
		setReadyFalse(observed, v1alpha1.ReasonSpecInvalid, err.Error())
		return ReconcileResult{Status: *observed}, nil
	}

	// Resolve the referenced runtime and apply defaults.
	resolved, err := r.resolver.Resolve(ctx, deployment)
	if err != nil {
		switch {
		case errors.Is(err, runtime.ErrRuntimeNotFound):
			if setRuntimeUnresolved(observed, v1alpha1.ReasonRuntimeNotFound, err.Error()) {
				r.recorder.Warningf(
					deployment,
					v1alpha1.ReasonRuntimeNotFound,
					"Could not resolve runtime %q: %v",
					deployment.Spec.Runtime.Ref,
					err,
				)
			}
			setValidationUnknown(observed, v1alpha1.ReasonRuntimeNotFound, "Referenced runtime must exist before validation can complete")
			setSecretsUnknown(observed, v1alpha1.ReasonRuntimeNotFound, "Runtime resolution failed before secret checks")
			setReadyFalse(observed, v1alpha1.ReasonRuntimeNotFound, err.Error())
		case errors.Is(err, runtime.ErrInvalidRuntimeConfiguration):
			if setRuntimeUnresolved(observed, v1alpha1.ReasonSpecInvalid, err.Error()) {
				r.recorder.Warningf(
					deployment,
					v1alpha1.ReasonSpecInvalid,
					"Runtime %q is incompatible with ModelDeployment: %v",
					deployment.Spec.Runtime.Ref,
					err,
				)
			}
			setValidationFailed(observed, err.Error())
			setSecretsUnknown(observed, v1alpha1.ReasonSpecInvalid, "Runtime validation failed before secret checks")
			setReadyFalse(observed, v1alpha1.ReasonSpecInvalid, err.Error())
		default:
			return ReconcileResult{}, fmt.Errorf("resolve ModelRuntime: %w", err)
		}
		observed.Phase = v1alpha1.ModelDeploymentPhaseFailed
		return ReconcileResult{Status: *observed}, nil
	}

	if setRuntimeResolved(observed, resolved.Image()) {
		r.recorder.Eventf(deployment, "Normal", events.ReasonRuntimeResolved,
			"Resolved runtime %q with image %q", deployment.Spec.Runtime.Ref, resolved.Image())
	}

	// Reconciliation-time validation uses operator configuration and cross-object checks.
	if err := r.validator.ValidateForReconciliation(deployment); err != nil {
		reason := v1alpha1.ReasonSpecInvalid
		var validationErr *validation.ValidationError
		if errors.As(err, &validationErr) {
			reason = validationErr.Reason
		}
		if setValidationFailedWithReason(observed, reason, err.Error()) {
			r.recorder.Warningf(deployment, reason, "ModelDeployment failed reconciliation validation: %v", err)
		}
		if reason == v1alpha1.ReasonSecretRequired {
			setSecretsNotReady(observed, reason, err.Error())
		} else {
			setSecretsReady(observed)
		}
		setReadyFalse(observed, reason, err.Error())
		return ReconcileResult{Status: *observed}, nil
	}

	if setValidationPassed(observed) {
		r.recorder.Event(deployment, "Normal", events.ReasonSpecValidated, "ModelDeployment spec is valid")
	}

	// Secret checks.
	if setSecretsReady(observed) &&
		deployment.Spec.Model.Source == "huggingface" &&
		deployment.Spec.Secrets.HuggingFaceTokenSecretName != "" {
		r.recorder.Eventf(deployment, "Normal", events.ReasonSecretsAvailable,
			"HuggingFace token secret %q is referenced", deployment.Spec.Secrets.HuggingFaceTokenSecretName)
	}

	// Validation passed.  Leave the phase to downstream reconcilers unless it
	// has not been initialized or this validation layer previously blocked it.
	if observed.Phase == "" || wasValidationBlocked || specChangedAfterFailure {
		observed.Phase = v1alpha1.ModelDeploymentPhasePending
		setReadyFalse(observed, events.ReasonSpecValidated, "Spec and runtime are valid; awaiting cache and activation")
	}

	return ReconcileResult{Status: *observed, Runtime: resolved}, nil
}

func validationBlocked(s v1alpha1.ModelDeploymentStatus) bool {
	if s.Phase != v1alpha1.ModelDeploymentPhaseFailed {
		return false
	}
	for _, conditionType := range []string{
		v1alpha1.ConditionSpecValid,
		v1alpha1.ConditionRuntimeResolved,
		v1alpha1.ConditionSecretsReady,
	} {
		condition, found := status.FindCondition(s.Conditions, conditionType)
		if found && condition.Status != metav1.ConditionTrue {
			return true
		}
	}
	return false
}

func setValidationFailed(s *v1alpha1.ModelDeploymentStatus, message string) bool {
	return setValidationFailedWithReason(s, v1alpha1.ReasonSpecInvalid, message)
}

func setValidationFailedWithReason(s *v1alpha1.ModelDeploymentStatus, reason, message string) bool {
	s.Phase = v1alpha1.ModelDeploymentPhaseFailed
	return status.SetCondition(&s.Conditions, s.ObservedGeneration, v1alpha1.Condition{
		Type:    v1alpha1.ConditionSpecValid,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

func setValidationUnknown(s *v1alpha1.ModelDeploymentStatus, reason, message string) bool {
	return status.SetCondition(&s.Conditions, s.ObservedGeneration, v1alpha1.Condition{
		Type:    v1alpha1.ConditionSpecValid,
		Status:  metav1.ConditionUnknown,
		Reason:  reason,
		Message: message,
	})
}

func setValidationPassed(s *v1alpha1.ModelDeploymentStatus) bool {
	return status.SetCondition(&s.Conditions, s.ObservedGeneration, v1alpha1.Condition{
		Type:    v1alpha1.ConditionSpecValid,
		Status:  metav1.ConditionTrue,
		Reason:  v1alpha1.ReasonSpecValidated,
		Message: "ModelDeployment spec is valid",
	})
}

func setRuntimeResolved(s *v1alpha1.ModelDeploymentStatus, image string) bool {
	return status.SetCondition(&s.Conditions, s.ObservedGeneration, v1alpha1.Condition{
		Type:    v1alpha1.ConditionRuntimeResolved,
		Status:  metav1.ConditionTrue,
		Reason:  v1alpha1.ReasonRuntimeResolved,
		Message: fmt.Sprintf("Runtime resolved with image %q", image),
	})
}

func setRuntimeUnresolved(s *v1alpha1.ModelDeploymentStatus, reason, message string) bool {
	return status.SetCondition(&s.Conditions, s.ObservedGeneration, v1alpha1.Condition{
		Type:    v1alpha1.ConditionRuntimeResolved,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

func setRuntimeUnknown(s *v1alpha1.ModelDeploymentStatus, reason, message string) bool {
	return status.SetCondition(&s.Conditions, s.ObservedGeneration, v1alpha1.Condition{
		Type:    v1alpha1.ConditionRuntimeResolved,
		Status:  metav1.ConditionUnknown,
		Reason:  reason,
		Message: message,
	})
}

func setSecretsReady(s *v1alpha1.ModelDeploymentStatus) bool {
	return status.SetCondition(&s.Conditions, s.ObservedGeneration, v1alpha1.Condition{
		Type:    v1alpha1.ConditionSecretsReady,
		Status:  metav1.ConditionTrue,
		Reason:  v1alpha1.ReasonSecretsAvailable,
		Message: "Credential references are valid",
	})
}

func setSecretsNotReady(s *v1alpha1.ModelDeploymentStatus, reason, message string) bool {
	return status.SetCondition(&s.Conditions, s.ObservedGeneration, v1alpha1.Condition{
		Type:    v1alpha1.ConditionSecretsReady,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

func setSecretsUnknown(s *v1alpha1.ModelDeploymentStatus, reason, message string) bool {
	return status.SetCondition(&s.Conditions, s.ObservedGeneration, v1alpha1.Condition{
		Type:    v1alpha1.ConditionSecretsReady,
		Status:  metav1.ConditionUnknown,
		Reason:  reason,
		Message: message,
	})
}

func setReadyFalse(s *v1alpha1.ModelDeploymentStatus, reason, message string) bool {
	return status.SetCondition(&s.Conditions, s.ObservedGeneration, v1alpha1.Condition{
		Type:    v1alpha1.ConditionReady,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}
