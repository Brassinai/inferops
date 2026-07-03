package controllers

import (
	"context"
	"fmt"
	"reflect"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	controllermetrics "github.com/brassinai/inferops/operator/internal/metrics"
	"github.com/brassinai/inferops/operator/internal/resources"
	"github.com/brassinai/inferops/operator/internal/status"
	"github.com/brassinai/inferops/operator/internal/validation"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ModelRuntimeController validates runtime definitions and publishes their
// availability without probing arbitrary user images.
type ModelRuntimeController struct {
	client        client.Client
	eventRecorder record.EventRecorder
	metrics       controllermetrics.Recorder
}

// NewModelRuntimeController creates a ModelRuntime status controller.
func NewModelRuntimeController(
	c client.Client,
	eventRecorder record.EventRecorder,
	metricsRecorder controllermetrics.Recorder,
) (*ModelRuntimeController, error) {
	if c == nil {
		return nil, fmt.Errorf("client is required")
	}
	if eventRecorder == nil {
		return nil, fmt.Errorf("event recorder is required")
	}
	if metricsRecorder == nil {
		metricsRecorder = controllermetrics.NoOpRecorder{}
	}
	return &ModelRuntimeController{
		client: c, eventRecorder: eventRecorder, metrics: metricsRecorder,
	}, nil
}

// Reconcile implements controller-runtime reconciliation.
func (r *ModelRuntimeController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var modelRuntime v1alpha1.ModelRuntime
	if err := r.client.Get(ctx, req.NamespacedName, &modelRuntime); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get ModelRuntime: %w", err)
	}
	if !modelRuntime.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	original := modelRuntime.DeepCopy()
	modelRuntime.Status.ObservedGeneration = modelRuntime.Generation

	validationErr := validation.ValidateModelRuntime(modelRuntime)
	if validationErr == nil {
		validationErr = resources.ValidatePinnedImage(modelRuntime.Spec.DefaultImage)
	}
	if validationErr != nil {
		modelRuntime.Status.Phase = v1alpha1.ModelRuntimePhaseFailed
		status.SetCondition(&modelRuntime.Status.Conditions, modelRuntime.Generation, v1alpha1.Condition{
			Type: v1alpha1.RuntimeConditionReady, Status: metav1.ConditionFalse,
			Reason: v1alpha1.RuntimeReasonInvalid, Message: validationErr.Error(),
		})
		return r.patchStatus(ctx, &modelRuntime, original, v1alpha1.RuntimeReasonInvalid)
	}

	modelRuntime.Status.Phase = v1alpha1.ModelRuntimePhaseReady
	status.SetCondition(&modelRuntime.Status.Conditions, modelRuntime.Generation, v1alpha1.Condition{
		Type: v1alpha1.RuntimeConditionReady, Status: metav1.ConditionTrue,
		Reason: v1alpha1.RuntimeReasonValidated, Message: "Runtime definition is valid",
	})
	return r.patchStatus(ctx, &modelRuntime, original, v1alpha1.RuntimeReasonValidated)
}

func (r *ModelRuntimeController) patchStatus(
	ctx context.Context,
	modelRuntime, original *v1alpha1.ModelRuntime,
	reason string,
) (ctrl.Result, error) {
	if reflect.DeepEqual(modelRuntime.Status, original.Status) {
		return ctrl.Result{}, nil
	}
	if err := r.client.Status().Patch(ctx, modelRuntime, client.MergeFrom(original)); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, fmt.Errorf("patch ModelRuntime status: %w", err)
	}
	oldCondition, oldFound := status.FindCondition(original.Status.Conditions, v1alpha1.RuntimeConditionReady)
	newCondition, _ := status.FindCondition(modelRuntime.Status.Conditions, v1alpha1.RuntimeConditionReady)
	if !oldFound || oldCondition.Status != newCondition.Status || oldCondition.Reason != newCondition.Reason {
		eventType := corev1.EventTypeNormal
		if modelRuntime.Status.Phase == v1alpha1.ModelRuntimePhaseFailed {
			eventType = corev1.EventTypeWarning
			r.metrics.IncFailure("modelruntime", reason)
		}
		r.eventRecorder.Event(modelRuntime, eventType, reason, newCondition.Message)
	}
	return ctrl.Result{}, nil
}

// SetupWithManager registers the ModelRuntime controller.
func (r *ModelRuntimeController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ModelRuntime{}).
		Complete(r)
}
