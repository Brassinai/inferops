package controllers

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	"github.com/brassinai/inferops/operator/internal/status"
	"github.com/brassinai/inferops/operator/internal/templates"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const defaultModelDrainTimeout = 5 * time.Minute
const drainPollInterval = 2 * time.Second

func (r *ModelDeploymentController) reconcileDeactivation(
	ctx context.Context,
	deployment, original *v1alpha1.ModelDeployment,
	cache *v1alpha1.ModelCache,
	finalReason string,
) (ctrl.Result, error) {
	runtimeExists, err := r.runtimeWorkloadExists(ctx, deployment)
	if err != nil {
		return ctrl.Result{}, err
	}

	switch deployment.Status.Phase {
	case v1alpha1.ModelDeploymentPhaseDraining:
		timeout, err := effectiveDrainTimeout(deployment)
		if err != nil {
			return ctrl.Result{}, err
		}
		if deployment.Status.DrainStartedAt == nil {
			now := metav1.Now()
			deployment.Status.DrainStartedAt = &now
		}
		elapsed := time.Since(deployment.Status.DrainStartedAt.Time)
		if elapsed < 0 {
			elapsed = 0
		}
		remaining := timeout - elapsed
		if runtimeExists {
			if _, err := r.deleteRuntimeWorkload(ctx, deployment); err != nil {
				return ctrl.Result{}, err
			}
		}
		if remaining > 0 {
			drainComplete, err := r.gatewayDrainComplete(ctx, deployment)
			if err != nil {
				return ctrl.Result{}, err
			}
			if drainComplete {
				remaining = 0
			}
		}
		if remaining > 0 {
			setDrainingConditions(deployment)
			result, err := r.patchDeploymentStatus(ctx, deployment, original, v1alpha1.ReasonDrainStarted)
			if err == nil {
				if r.drainChecker != nil && remaining > drainPollInterval {
					remaining = drainPollInterval
				}
				result.RequeueAfter = remaining
			}
			return result, err
		}

		deployment.Status.Phase = v1alpha1.ModelDeploymentPhaseDeactivating
		deployment.Status.Model.Loaded = false
		setRuntimeWaitingConditions(
			deployment,
			v1alpha1.ReasonDeactivationStarted,
			"Drain grace period ended; the runtime workload is terminating",
		)
		result, err := r.patchDeploymentStatus(
			ctx,
			deployment,
			original,
			v1alpha1.ReasonDeactivationStarted,
		)
		if err == nil {
			result.RequeueAfter = deploymentRequeueAfter
		}
		return result, err

	case v1alpha1.ModelDeploymentPhaseDeactivating:
		if runtimeExists {
			if _, err := r.deleteRuntimeWorkload(ctx, deployment); err != nil {
				return ctrl.Result{}, err
			}
			setRuntimeWaitingConditions(
				deployment,
				v1alpha1.ReasonDeactivationStarted,
				"Runtime workload termination is still in progress",
			)
			result, err := r.patchDeploymentStatus(ctx, deployment, original, "")
			if err == nil {
				result.RequeueAfter = deploymentRequeueAfter
			}
			return result, err
		}
	}

	if runtimeExists {
		now := metav1.Now()
		deployment.Status.DrainStartedAt = &now
		deployment.Status.Phase = v1alpha1.ModelDeploymentPhaseDraining
		setDrainingConditions(deployment)
		if original.Status.Phase == v1alpha1.ModelDeploymentPhaseActive ||
			original.Status.Phase == v1alpha1.ModelDeploymentPhaseActivating {
			if err := r.touchCacheLastUsed(ctx, cache); err != nil {
				return ctrl.Result{}, err
			}
		}
		result, err := r.patchDeploymentStatus(ctx, deployment, original, v1alpha1.ReasonDrainStarted)
		if err == nil {
			timeout, timeoutErr := effectiveDrainTimeout(deployment)
			if timeoutErr != nil {
				return ctrl.Result{}, timeoutErr
			}
			if r.drainChecker != nil && timeout > drainPollInterval {
				timeout = drainPollInterval
			}
			result.RequeueAfter = timeout
		}
		return result, err
	}

	if _, err := r.deleteRuntimeWorkload(ctx, deployment); err != nil {
		return ctrl.Result{}, err
	}
	deployment.Status.DrainStartedAt = nil
	deployment.Status.Phase = v1alpha1.ModelDeploymentPhaseCached
	deployment.Status.AssignedNode = ""
	deployment.Status.AssignedGPUs = nil
	deployment.Status.Replicas = v1alpha1.ReplicaStatus{}
	deployment.Status.Scaling = v1alpha1.ScalingStatus{}
	deployment.Status.Model.Loaded = false
	stateMessage := "Model is cached and inactive"
	runtimeMessage := "Runtime is not created while the deployment is inactive"
	gpuMessage := "Inactive deployment does not reserve GPU capacity"
	if finalReason == v1alpha1.ReasonReplacementDisplaced {
		stateMessage = "Model is cached while the replacement transaction owns the GPU slot"
		runtimeMessage = "Runtime was removed for an explicitly requested replacement"
		gpuMessage = "Displaced deployment does not reserve GPU capacity"
	}
	if deployment.Spec.Resources.GPU != nil {
		setDeploymentCondition(
			deployment,
			v1alpha1.ConditionGPUAssigned,
			metav1.ConditionFalse,
			finalReason,
			gpuMessage,
		)
	} else {
		status.RemoveCondition(&deployment.Status.Conditions, v1alpha1.ConditionGPUAssigned)
	}
	setDeploymentCondition(
		deployment,
		v1alpha1.ConditionRuntimeReady,
		metav1.ConditionFalse,
		finalReason,
		runtimeMessage,
	)
	setDeploymentCondition(
		deployment,
		v1alpha1.ConditionModelLoaded,
		metav1.ConditionFalse,
		finalReason,
		stateMessage,
	)
	setDeploymentCondition(
		deployment,
		v1alpha1.ConditionRoutingReady,
		metav1.ConditionFalse,
		v1alpha1.ReasonRouteDisabled,
		"Gateway routing remains disabled while inactive",
	)
	setReadyFalse(&deployment.Status, finalReason, stateMessage)
	eventReason := ""
	if original.Status.Phase != v1alpha1.ModelDeploymentPhaseCached {
		eventReason = v1alpha1.ReasonDrainComplete
	}
	return r.patchDeploymentStatus(ctx, deployment, original, eventReason)
}

func (r *ModelDeploymentController) gatewayDrainComplete(
	ctx context.Context,
	deployment *v1alpha1.ModelDeployment,
) (bool, error) {
	if r.drainChecker == nil {
		return false, nil
	}
	complete, err := r.drainChecker.DrainComplete(ctx, deployment.Namespace, deployment.Name)
	if err != nil {
		return false, fmt.Errorf("check gateway drain status: %w", err)
	}
	return complete, nil
}

func setDrainingConditions(deployment *v1alpha1.ModelDeployment) {
	message := "Gateway routing is disabled while in-flight requests receive their drain grace period"
	setDeploymentCondition(
		deployment,
		v1alpha1.ConditionRoutingReady,
		metav1.ConditionFalse,
		v1alpha1.ReasonDrainStarted,
		message,
	)
	setDeploymentCondition(
		deployment,
		v1alpha1.ConditionReady,
		metav1.ConditionFalse,
		v1alpha1.ReasonDrainStarted,
		message,
	)
}

func effectiveDrainTimeout(deployment *v1alpha1.ModelDeployment) (time.Duration, error) {
	if deployment.Spec.Activation.DrainTimeout == "" {
		return defaultModelDrainTimeout, nil
	}
	timeout, err := time.ParseDuration(deployment.Spec.Activation.DrainTimeout)
	if err != nil {
		return 0, fmt.Errorf("parse drain timeout: %w", err)
	}
	if timeout <= 0 {
		return 0, errors.New("drain timeout must be greater than zero")
	}
	return timeout, nil
}

func (r *ModelDeploymentController) runtimeWorkloadExists(
	ctx context.Context,
	deployment *v1alpha1.ModelDeployment,
) (bool, error) {
	var runtimeDeployment appsv1.Deployment
	err := r.client.Get(ctx, types.NamespacedName{
		Namespace: deployment.Namespace,
		Name:      templates.RuntimeServiceName(deployment.Name),
	}, &runtimeDeployment)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get runtime Deployment during lifecycle transition: %w", err)
	}
	if err := assertControlledBy(&runtimeDeployment, deployment); err != nil {
		return false, err
	}
	return true, nil
}

func (r *ModelDeploymentController) startReplacement(
	ctx context.Context,
	deployment, original *v1alpha1.ModelDeployment,
	placementErr error,
) (ctrl.Result, error) {
	if effectiveGPUReservation(deployment) != 1 || effectiveRuntimeReplicas(deployment) != 1 {
		message := "single-GPU replacement requires exactly one replica requesting one GPU"
		setDeploymentCondition(
			deployment,
			v1alpha1.ConditionReplacement,
			metav1.ConditionFalse,
			v1alpha1.ReasonReplacementUnsupported,
			message,
		)
		return r.failDeployment(
			ctx,
			deployment,
			original,
			v1alpha1.ReasonReplacementUnsupported,
			message,
		)
	}

	target, err := r.selectReplacementTarget(ctx, deployment)
	if err != nil {
		return ctrl.Result{}, err
	}
	if target == nil {
		message := fmt.Sprintf("no eligible active single-GPU deployment can be replaced: %v", placementErr)
		setDeploymentCondition(
			deployment,
			v1alpha1.ConditionReplacement,
			metav1.ConditionFalse,
			v1alpha1.ReasonReplacementNoCandidate,
			message,
		)
		return r.failDeployment(
			ctx,
			deployment,
			original,
			v1alpha1.ReasonReplacementNoCandidate,
			message,
		)
	}

	now := metav1.Now()
	requester := referenceFor(deployment)
	targetRef := referenceFor(target)
	deployment.Status.Replacement = &v1alpha1.ReplacementStatus{
		Phase:             v1alpha1.ReplacementPhaseDraining,
		RequestGeneration: deployment.Generation,
		Target:            &targetRef,
		StartedAt:         &now,
		Message:           fmt.Sprintf("Draining %s/%s before activating the replacement", target.Namespace, target.Name),
	}
	deployment.Status.Phase = v1alpha1.ModelDeploymentPhaseWaitingForGPU
	deployment.Status.AssignedNode = target.Status.AssignedNode
	deployment.Status.AssignedGPUs = nil
	deployment.Status.Replicas = v1alpha1.ReplicaStatus{Desired: effectiveRuntimeReplicas(deployment)}
	deployment.Status.Model.Loaded = false
	setRuntimeWaitingConditions(
		deployment,
		v1alpha1.ReasonReplacementDraining,
		deployment.Status.Replacement.Message,
	)
	setDeploymentCondition(
		deployment,
		v1alpha1.ConditionGPUAssigned,
		metav1.ConditionUnknown,
		v1alpha1.ReasonReplacementDraining,
		fmt.Sprintf("GPU slot handoff from %s/%s is reserved but not yet available", target.Namespace, target.Name),
	)
	setDeploymentCondition(
		deployment,
		v1alpha1.ConditionReplacement,
		metav1.ConditionUnknown,
		v1alpha1.ReasonReplacementStarted,
		deployment.Status.Replacement.Message,
	)

	result, err := r.patchDeploymentStatus(
		ctx,
		deployment,
		original,
		v1alpha1.ReasonReplacementStarted,
	)
	if err != nil {
		return result, err
	}
	if result.Requeue {
		return result, nil
	}
	if err := r.markReplacementTarget(ctx, target, requester); err != nil {
		return ctrl.Result{}, err
	}
	result.RequeueAfter = deploymentRequeueAfter
	return result, nil
}

func (r *ModelDeploymentController) selectReplacementTarget(
	ctx context.Context,
	requester *v1alpha1.ModelDeployment,
) (*v1alpha1.ModelDeployment, error) {
	var deployments v1alpha1.ModelDeploymentList
	if err := r.client.List(ctx, &deployments); err != nil {
		return nil, fmt.Errorf("list ModelDeployments for replacement: %w", err)
	}

	candidates := make([]*v1alpha1.ModelDeployment, 0)
	for i := range deployments.Items {
		candidate := &deployments.Items[i]
		if candidate.Namespace != requester.Namespace ||
			candidate.Name == requester.Name ||
			!candidate.DeletionTimestamp.IsZero() ||
			effectiveDesiredState(candidate) != v1alpha1.ActivationDesiredStateActive ||
			candidate.Status.ObservedGeneration < candidate.Generation {
			continue
		}
		if candidate.Status.Phase != v1alpha1.ModelDeploymentPhaseActive ||
			candidate.Status.AssignedNode == "" ||
			candidate.Status.AssignedNode != requester.Status.Cache.NodeName ||
			candidate.Spec.Resources.GPU == nil ||
			gpuResourceName(candidate) != gpuResourceName(requester) ||
			effectiveGPUReservation(candidate) != 1 ||
			!replacementCandidateAvailable(candidate.Status.Replacement) {
			continue
		}
		if effectiveWhenFull(requester) == v1alpha1.ActivationWhenFullReplaceLowestPriority &&
			candidate.Spec.Activation.Priority >= requester.Spec.Activation.Priority {
			continue
		}
		candidates = append(candidates, candidate)
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	sort.Slice(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if effectiveWhenFull(requester) == v1alpha1.ActivationWhenFullReplaceLowestPriority &&
			left.Spec.Activation.Priority != right.Spec.Activation.Priority {
			return left.Spec.Activation.Priority < right.Spec.Activation.Priority
		}
		leftTime, rightTime := activeSince(left), activeSince(right)
		if !leftTime.Equal(rightTime) {
			return leftTime.Before(rightTime)
		}
		if left.Namespace != right.Namespace {
			return left.Namespace < right.Namespace
		}
		return left.Name < right.Name
	})
	return candidates[0].DeepCopy(), nil
}

func replacementCandidateAvailable(replacement *v1alpha1.ReplacementStatus) bool {
	return replacement == nil ||
		(replacement.RequestedBy == nil &&
			replacement.Target != nil &&
			replacement.Phase == v1alpha1.ReplacementPhaseSucceeded)
}

func activeSince(deployment *v1alpha1.ModelDeployment) time.Time {
	if ready, found := status.FindCondition(deployment.Status.Conditions, v1alpha1.ConditionReady); found &&
		ready.Status == metav1.ConditionTrue &&
		ready.ObservedGeneration >= deployment.Generation &&
		!ready.LastTransitionTime.IsZero() {
		return ready.LastTransitionTime.Time
	}
	return deployment.CreationTimestamp.Time
}

func referenceFor(deployment *v1alpha1.ModelDeployment) v1alpha1.ReplacementReference {
	return v1alpha1.ReplacementReference{
		Namespace: deployment.Namespace,
		Name:      deployment.Name,
		UID:       deployment.UID,
	}
}

func (r *ModelDeploymentController) markReplacementTarget(
	ctx context.Context,
	target *v1alpha1.ModelDeployment,
	requester v1alpha1.ReplacementReference,
) error {
	if target.Namespace != requester.Namespace {
		return fmt.Errorf(
			"replacement target %s/%s is outside requester namespace %s",
			target.Namespace,
			target.Name,
			requester.Namespace,
		)
	}
	if target.Status.Replacement != nil {
		if referencesEqual(target.Status.Replacement.RequestedBy, &requester) {
			return nil
		}
		if !replacementCandidateAvailable(target.Status.Replacement) {
			return fmt.Errorf(
				"replacement target %s/%s is already participating in another replacement",
				target.Namespace,
				target.Name,
			)
		}
		if err := r.releaseReplacementTarget(
			ctx,
			target.Status.Replacement.Target,
			target,
		); err != nil {
			return fmt.Errorf(
				"release previous target before replacing %s/%s: %w",
				target.Namespace,
				target.Name,
				err,
			)
		}
	}
	before := target.DeepCopy()
	now := metav1.Now()
	target.Status.Replacement = &v1alpha1.ReplacementStatus{
		Phase:       v1alpha1.ReplacementPhaseDraining,
		RequestedBy: &requester,
		StartedAt:   &now,
		Message:     fmt.Sprintf("Replacement requested by %s/%s", requester.Namespace, requester.Name),
	}
	setDeploymentCondition(
		target,
		v1alpha1.ConditionReplacement,
		metav1.ConditionUnknown,
		v1alpha1.ReasonReplacementDraining,
		target.Status.Replacement.Message,
	)
	if err := r.client.Status().Patch(ctx, target, client.MergeFrom(before)); err != nil {
		return fmt.Errorf("mark replacement target %s/%s: %w", target.Namespace, target.Name, err)
	}
	r.eventRecorder.Eventf(
		target,
		corev1.EventTypeNormal,
		v1alpha1.ReasonReplacementDraining,
		"Replacement requested by %s/%s",
		requester.Namespace,
		requester.Name,
	)
	return nil
}

func (r *ModelDeploymentController) reconcileReplacementRequester(
	ctx context.Context,
	deployment, original *v1alpha1.ModelDeployment,
) (bool, ctrl.Result, error) {
	replacement := deployment.Status.Replacement
	if replacement == nil || replacement.Target == nil {
		return false, ctrl.Result{}, nil
	}
	if replacement.Target.Namespace != deployment.Namespace {
		message := fmt.Sprintf(
			"replacement target %s/%s is outside requester namespace %s",
			replacement.Target.Namespace,
			replacement.Target.Name,
			deployment.Namespace,
		)
		deployment.Status.Replacement = nil
		deployment.Status.AssignedNode = ""
		deployment.Status.AssignedGPUs = nil
		status.RemoveCondition(&deployment.Status.Conditions, v1alpha1.ConditionReplacement)
		if effectiveDesiredState(deployment) == v1alpha1.ActivationDesiredStateInactive {
			return false, ctrl.Result{}, nil
		}
		setDeploymentCondition(
			deployment,
			v1alpha1.ConditionReplacement,
			metav1.ConditionFalse,
			v1alpha1.ReasonReplacementNoCandidate,
			message,
		)
		result, err := r.failDeployment(
			ctx,
			deployment,
			original,
			v1alpha1.ReasonReplacementNoCandidate,
			message,
		)
		return true, result, err
	}
	if effectiveDesiredState(deployment) == v1alpha1.ActivationDesiredStateInactive {
		if err := r.releaseReplacementTarget(ctx, replacement.Target, deployment); err != nil {
			return true, ctrl.Result{}, err
		}
		deployment.Status.Replacement = nil
		status.RemoveCondition(&deployment.Status.Conditions, v1alpha1.ConditionReplacement)
		return false, ctrl.Result{}, nil
	}
	if replacement.RequestGeneration > 0 &&
		replacement.RequestGeneration != deployment.Generation {
		switch replacement.Phase {
		case v1alpha1.ReplacementPhaseDraining:
			result, err := r.cancelPendingReplacement(
				ctx,
				deployment,
				original,
				"Deployment spec changed; replacement selection will be reevaluated",
			)
			return true, result, err
		case v1alpha1.ReplacementPhaseActivating:
			result, err := r.beginReplacementRollback(
				ctx,
				deployment,
				original,
				"deployment spec changed before replacement activation completed",
			)
			return true, result, err
		}
	}
	if !isReplacementPolicy(effectiveWhenFull(deployment)) {
		switch replacement.Phase {
		case v1alpha1.ReplacementPhaseDraining:
			result, err := r.cancelPendingReplacement(
				ctx,
				deployment,
				original,
				"Replacement policy was withdrawn; GPU capacity will be reevaluated",
			)
			return true, result, err
		case v1alpha1.ReplacementPhaseActivating:
			result, err := r.beginReplacementRollback(
				ctx,
				deployment,
				original,
				"explicit replacement policy was withdrawn before activation completed",
			)
			return true, result, err
		}
	}
	if replacement.Phase == v1alpha1.ReplacementPhaseRollingBack {
		result, err := r.reconcileReplacementRollback(ctx, deployment, original)
		return true, result, err
	}
	if replacement.Phase != v1alpha1.ReplacementPhaseDraining {
		return false, ctrl.Result{}, nil
	}

	target, err := r.getReplacementParticipant(ctx, replacement.Target)
	if err != nil {
		if apierrors.IsNotFound(err) {
			message := "replacement target disappeared before its GPU slot was released"
			replacement.Phase = v1alpha1.ReplacementPhaseRollbackFailed
			replacement.Message = message
			deployment.Status.AssignedNode = ""
			deployment.Status.AssignedGPUs = nil
			setDeploymentCondition(
				deployment,
				v1alpha1.ConditionReplacement,
				metav1.ConditionFalse,
				v1alpha1.ReasonReplacementRollbackFailed,
				message,
			)
			result, patchErr := r.failDeployment(
				ctx,
				deployment,
				original,
				v1alpha1.ReasonReplacementRollbackFailed,
				message,
			)
			return true, result, patchErr
		}
		return true, ctrl.Result{}, err
	}
	requester := referenceFor(deployment)
	if replacementCandidateAvailable(target.Status.Replacement) &&
		effectiveDesiredState(target) == v1alpha1.ActivationDesiredStateActive &&
		target.Status.Phase == v1alpha1.ModelDeploymentPhaseActive {
		if err := r.markReplacementTarget(ctx, target, requester); err != nil {
			return true, ctrl.Result{}, err
		}
	} else if target.Status.Replacement != nil &&
		!referencesEqual(target.Status.Replacement.RequestedBy, &requester) {
		message := fmt.Sprintf(
			"replacement target %s/%s is owned by another replacement transaction",
			target.Namespace,
			target.Name,
		)
		replacement.Phase = v1alpha1.ReplacementPhaseRollbackFailed
		replacement.Message = message
		deployment.Status.AssignedNode = ""
		deployment.Status.AssignedGPUs = nil
		setDeploymentCondition(
			deployment,
			v1alpha1.ConditionReplacement,
			metav1.ConditionFalse,
			v1alpha1.ReasonReplacementNoCandidate,
			message,
		)
		result, err := r.failDeployment(
			ctx,
			deployment,
			original,
			v1alpha1.ReasonReplacementNoCandidate,
			message,
		)
		return true, result, err
	}
	runtimeExists, err := r.runtimeWorkloadExists(ctx, target)
	if err != nil {
		return true, ctrl.Result{}, err
	}
	if target.Status.Phase == v1alpha1.ModelDeploymentPhaseFailed {
		message := fmt.Sprintf(
			"replacement target %s/%s failed while releasing its GPU slot",
			target.Namespace,
			target.Name,
		)
		if err := r.releaseReplacementTarget(ctx, replacement.Target, deployment); err != nil {
			return true, ctrl.Result{}, err
		}
		replacement.Phase = v1alpha1.ReplacementPhaseRollbackFailed
		replacement.Message = message
		deployment.Status.AssignedNode = ""
		deployment.Status.AssignedGPUs = nil
		setDeploymentCondition(
			deployment,
			v1alpha1.ConditionReplacement,
			metav1.ConditionFalse,
			v1alpha1.ReasonReplacementRollbackFailed,
			message,
		)
		result, err := r.failDeployment(
			ctx,
			deployment,
			original,
			v1alpha1.ReasonReplacementRollbackFailed,
			message,
		)
		return true, result, err
	}
	if runtimeExists ||
		target.Status.AssignedNode != "" ||
		(target.Status.Phase != v1alpha1.ModelDeploymentPhaseCached &&
			target.Status.Phase != v1alpha1.ModelDeploymentPhaseWaitingForGPU) {
		deployment.Status.Phase = v1alpha1.ModelDeploymentPhaseWaitingForGPU
		setRuntimeWaitingConditions(
			deployment,
			v1alpha1.ReasonReplacementDraining,
			fmt.Sprintf("Waiting for %s/%s to finish draining", target.Namespace, target.Name),
		)
		setDeploymentCondition(
			deployment,
			v1alpha1.ConditionGPUAssigned,
			metav1.ConditionUnknown,
			v1alpha1.ReasonReplacementDraining,
			fmt.Sprintf("GPU slot handoff from %s/%s is reserved but not yet available", target.Namespace, target.Name),
		)
		result, err := r.patchDeploymentStatus(
			ctx,
			deployment,
			original,
			v1alpha1.ReasonReplacementDraining,
		)
		if err == nil {
			result.RequeueAfter = deploymentRequeueAfter
		}
		return true, result, err
	}

	replacement.Phase = v1alpha1.ReplacementPhaseActivating
	replacement.Message = fmt.Sprintf("GPU slot from %s/%s was released; replacement runtime is activating", target.Namespace, target.Name)
	setDeploymentCondition(
		deployment,
		v1alpha1.ConditionReplacement,
		metav1.ConditionUnknown,
		v1alpha1.ReasonReplacementActivating,
		replacement.Message,
	)
	return false, ctrl.Result{}, nil
}

func (r *ModelDeploymentController) cancelPendingReplacement(
	ctx context.Context,
	deployment, original *v1alpha1.ModelDeployment,
	message string,
) (ctrl.Result, error) {
	replacement := deployment.Status.Replacement
	if err := r.releaseReplacementTarget(ctx, replacement.Target, deployment); err != nil {
		return ctrl.Result{}, err
	}
	deployment.Status.Replacement = nil
	deployment.Status.AssignedNode = ""
	deployment.Status.AssignedGPUs = nil
	status.RemoveCondition(&deployment.Status.Conditions, v1alpha1.ConditionReplacement)
	deployment.Status.Phase = v1alpha1.ModelDeploymentPhaseWaitingForGPU
	setRuntimeWaitingConditions(
		deployment,
		v1alpha1.ReasonWaitingForGPU,
		message,
	)
	result, err := r.patchDeploymentStatus(
		ctx,
		deployment,
		original,
		v1alpha1.ReasonReplacementCanceled,
	)
	if err == nil {
		result = ctrl.Result{Requeue: true}
	}
	return result, err
}

func isReplacementPolicy(policy v1alpha1.ActivationWhenFull) bool {
	return policy == v1alpha1.ActivationWhenFullReplaceOldest ||
		policy == v1alpha1.ActivationWhenFullReplaceLowestPriority
}

func (r *ModelDeploymentController) reconcileReplacementParticipant(
	ctx context.Context,
	deployment, original *v1alpha1.ModelDeployment,
	cache *v1alpha1.ModelCache,
) (bool, ctrl.Result, error) {
	replacement := deployment.Status.Replacement
	if replacement == nil || replacement.RequestedBy == nil {
		return false, ctrl.Result{}, nil
	}
	if replacement.RequestedBy.Namespace != deployment.Namespace {
		deployment.Status.Replacement = nil
		status.RemoveCondition(&deployment.Status.Conditions, v1alpha1.ConditionReplacement)
		return false, ctrl.Result{}, nil
	}
	requester, err := r.getReplacementParticipant(ctx, replacement.RequestedBy)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return true, ctrl.Result{}, err
		}
		deployment.Status.Replacement = nil
		status.RemoveCondition(&deployment.Status.Conditions, v1alpha1.ConditionReplacement)
		return false, ctrl.Result{}, nil
	}
	requesterReplacement := requester.Status.Replacement
	targetRef := referenceFor(deployment)
	if requesterReplacement == nil ||
		!referencesEqual(requesterReplacement.Target, &targetRef) {
		deployment.Status.Replacement = nil
		status.RemoveCondition(&deployment.Status.Conditions, v1alpha1.ConditionReplacement)
		return false, ctrl.Result{}, nil
	}
	if effectiveDesiredState(deployment) == v1alpha1.ActivationDesiredStateInactive {
		deployment.Status.Replacement = nil
		status.RemoveCondition(&deployment.Status.Conditions, v1alpha1.ConditionReplacement)
		return false, ctrl.Result{}, nil
	}

	switch requesterReplacement.Phase {
	case v1alpha1.ReplacementPhaseDraining:
		replacement.Phase = v1alpha1.ReplacementPhaseDraining
		result, err := r.reconcileDeactivation(
			ctx,
			deployment,
			original,
			cache,
			v1alpha1.ReasonReplacementDisplaced,
		)
		return true, result, err

	case v1alpha1.ReplacementPhaseActivating:
		runtimeExists, runtimeErr := r.runtimeWorkloadExists(ctx, deployment)
		if runtimeErr != nil {
			return true, ctrl.Result{}, runtimeErr
		}
		if runtimeExists || deployment.Status.AssignedNode != "" {
			result, err := r.reconcileDeactivation(
				ctx,
				deployment,
				original,
				cache,
				v1alpha1.ReasonReplacementDisplaced,
			)
			return true, result, err
		}
		replacement.Phase = v1alpha1.ReplacementPhaseDisplaced
		deployment.Status.Phase = v1alpha1.ModelDeploymentPhaseCached
		setDeploymentCondition(
			deployment,
			v1alpha1.ConditionReplacement,
			metav1.ConditionUnknown,
			v1alpha1.ReasonReplacementActivating,
			fmt.Sprintf("Waiting for replacement %s/%s to become ready", requester.Namespace, requester.Name),
		)
		result, err := r.patchDeploymentStatus(ctx, deployment, original, "")
		if err == nil {
			result.RequeueAfter = deploymentRequeueAfter
		}
		return true, result, err

	case v1alpha1.ReplacementPhaseSucceeded:
		if requester.Status.Phase != v1alpha1.ModelDeploymentPhaseActive {
			deployment.Status.Replacement = nil
			status.RemoveCondition(&deployment.Status.Conditions, v1alpha1.ConditionReplacement)
			return false, ctrl.Result{}, nil
		}
		replacement.Phase = v1alpha1.ReplacementPhaseDisplaced
		deployment.Status.Phase = v1alpha1.ModelDeploymentPhaseWaitingForGPU
		deployment.Status.AssignedNode = ""
		deployment.Status.AssignedGPUs = nil
		deployment.Status.Replicas = v1alpha1.ReplicaStatus{Desired: effectiveRuntimeReplicas(deployment)}
		setRuntimeWaitingConditions(
			deployment,
			v1alpha1.ReasonReplacementDisplaced,
			fmt.Sprintf("GPU slot is held by replacement %s/%s", requester.Namespace, requester.Name),
		)
		setDeploymentCondition(
			deployment,
			v1alpha1.ConditionReplacement,
			metav1.ConditionFalse,
			v1alpha1.ReasonReplacementDisplaced,
			fmt.Sprintf("Runtime was replaced by %s/%s", requester.Namespace, requester.Name),
		)
		result, err := r.patchDeploymentStatus(
			ctx,
			deployment,
			original,
			v1alpha1.ReasonReplacementDisplaced,
		)
		if err == nil {
			result.RequeueAfter = waitingRequeueAfter
		}
		return true, result, err

	case v1alpha1.ReplacementPhaseRollingBack:
		replacement.Phase = v1alpha1.ReplacementPhaseRollingBack
		deployment.Status.DrainStartedAt = nil
		setDeploymentCondition(
			deployment,
			v1alpha1.ConditionReplacement,
			metav1.ConditionUnknown,
			v1alpha1.ReasonReplacementRollbackStarted,
			fmt.Sprintf("Restoring runtime after %s/%s failed to activate", requester.Namespace, requester.Name),
		)
		return false, ctrl.Result{}, nil

	default:
		deployment.Status.Replacement = nil
		status.RemoveCondition(&deployment.Status.Conditions, v1alpha1.ConditionReplacement)
		return false, ctrl.Result{}, nil
	}
}

func (r *ModelDeploymentController) reconcileReplacementRollback(
	ctx context.Context,
	deployment, original *v1alpha1.ModelDeployment,
) (ctrl.Result, error) {
	replacement := deployment.Status.Replacement
	target, err := r.getReplacementParticipant(ctx, replacement.Target)
	if err != nil {
		message := fmt.Sprintf("replacement rollback target is unavailable: %v", err)
		replacement.Phase = v1alpha1.ReplacementPhaseRollbackFailed
		replacement.Message = message
		deployment.Status.AssignedNode = ""
		deployment.Status.AssignedGPUs = nil
		setDeploymentCondition(
			deployment,
			v1alpha1.ConditionReplacement,
			metav1.ConditionFalse,
			v1alpha1.ReasonReplacementRollbackFailed,
			message,
		)
		return r.failDeployment(
			ctx,
			deployment,
			original,
			v1alpha1.ReasonReplacementRollbackFailed,
			message,
		)
	}
	if effectiveDesiredState(target) != v1alpha1.ActivationDesiredStateActive {
		message := fmt.Sprintf(
			"replacement failed and %s/%s no longer requests an active runtime",
			target.Namespace,
			target.Name,
		)
		if err := r.releaseReplacementTarget(ctx, replacement.Target, deployment); err != nil {
			return ctrl.Result{}, err
		}
		replacement.Phase = v1alpha1.ReplacementPhaseRollbackFailed
		replacement.Message = message
		deployment.Status.AssignedNode = ""
		deployment.Status.AssignedGPUs = nil
		setDeploymentCondition(
			deployment,
			v1alpha1.ConditionReplacement,
			metav1.ConditionFalse,
			v1alpha1.ReasonReplacementRollbackFailed,
			message,
		)
		return r.failDeployment(
			ctx,
			deployment,
			original,
			v1alpha1.ReasonReplacementRollbackFailed,
			message,
		)
	}

	runtimeExists, err := r.runtimeWorkloadExists(ctx, deployment)
	if err != nil {
		return ctrl.Result{}, err
	}
	if runtimeExists {
		if _, err := r.deleteRuntimeWorkload(ctx, deployment); err != nil {
			return ctrl.Result{}, err
		}
		deployment.Status.AssignedGPUs = nil
		deployment.Status.Replicas = v1alpha1.ReplicaStatus{}
		deployment.Status.Model.Loaded = false
		result, err := r.patchDeploymentStatus(
			ctx,
			deployment,
			original,
			v1alpha1.ReasonReplacementRollbackStarted,
		)
		if err == nil {
			result.RequeueAfter = deploymentRequeueAfter
		}
		return result, err
	}

	requesterRef := referenceFor(deployment)
	if target.Status.Replacement != nil &&
		!referencesEqual(target.Status.Replacement.RequestedBy, &requesterRef) {
		replacement.Phase = v1alpha1.ReplacementPhaseRollbackFailed
		replacement.Message = fmt.Sprintf(
			"cannot restore %s/%s because another replacement transaction owns it",
			target.Namespace,
			target.Name,
		)
		deployment.Status.AssignedNode = ""
		deployment.Status.AssignedGPUs = nil
		setDeploymentCondition(
			deployment,
			v1alpha1.ConditionReplacement,
			metav1.ConditionFalse,
			v1alpha1.ReasonReplacementRollbackFailed,
			replacement.Message,
		)
		return r.failDeployment(
			ctx,
			deployment,
			original,
			v1alpha1.ReasonReplacementRollbackFailed,
			replacement.Message,
		)
	}
	if target.Status.Replacement == nil ||
		target.Status.Replacement.Phase != v1alpha1.ReplacementPhaseRollingBack {
		before := target.DeepCopy()
		if target.Status.Replacement == nil {
			target.Status.Replacement = &v1alpha1.ReplacementStatus{RequestedBy: &requesterRef}
		}
		target.Status.Replacement.Phase = v1alpha1.ReplacementPhaseRollingBack
		target.Status.Replacement.Message = fmt.Sprintf("Restoring runtime after %s/%s failed", deployment.Namespace, deployment.Name)
		if err := r.client.Status().Patch(ctx, target, client.MergeFrom(before)); err != nil {
			return ctrl.Result{}, fmt.Errorf("request replacement rollback on %s/%s: %w", target.Namespace, target.Name, err)
		}
	}

	if target.Status.Phase == v1alpha1.ModelDeploymentPhaseActive {
		if err := r.releaseReplacementTarget(ctx, replacement.Target, deployment); err != nil {
			return ctrl.Result{}, err
		}
		replacement.Phase = v1alpha1.ReplacementPhaseRolledBack
		replacement.Message = fmt.Sprintf("Replacement failed; restored %s/%s", target.Namespace, target.Name)
		deployment.Status.AssignedNode = ""
		deployment.Status.AssignedGPUs = nil
		setDeploymentCondition(
			deployment,
			v1alpha1.ConditionReplacement,
			metav1.ConditionFalse,
			v1alpha1.ReasonReplacementRolledBack,
			replacement.Message,
		)
		return r.failDeployment(
			ctx,
			deployment,
			original,
			v1alpha1.ReasonReplacementRolledBack,
			replacement.Message,
		)
	}
	if target.Status.Phase == v1alpha1.ModelDeploymentPhaseFailed {
		if err := r.releaseReplacementTarget(ctx, replacement.Target, deployment); err != nil {
			return ctrl.Result{}, err
		}
		replacement.Phase = v1alpha1.ReplacementPhaseRollbackFailed
		replacement.Message = fmt.Sprintf("Replacement failed and %s/%s could not be restored", target.Namespace, target.Name)
		deployment.Status.AssignedNode = ""
		deployment.Status.AssignedGPUs = nil
		setDeploymentCondition(
			deployment,
			v1alpha1.ConditionReplacement,
			metav1.ConditionFalse,
			v1alpha1.ReasonReplacementRollbackFailed,
			replacement.Message,
		)
		return r.failDeployment(
			ctx,
			deployment,
			original,
			v1alpha1.ReasonReplacementRollbackFailed,
			replacement.Message,
		)
	}

	deployment.Status.Phase = v1alpha1.ModelDeploymentPhaseDeactivating
	setRuntimeWaitingConditions(
		deployment,
		v1alpha1.ReasonReplacementRollbackStarted,
		fmt.Sprintf("Waiting for %s/%s to become ready during rollback", target.Namespace, target.Name),
	)
	setDeploymentCondition(
		deployment,
		v1alpha1.ConditionGPUAssigned,
		metav1.ConditionUnknown,
		v1alpha1.ReasonReplacementRollbackStarted,
		fmt.Sprintf("GPU slot is reserved for rollback to %s/%s", target.Namespace, target.Name),
	)
	result, err := r.patchDeploymentStatus(
		ctx,
		deployment,
		original,
		v1alpha1.ReasonReplacementRollbackStarted,
	)
	if err == nil {
		result.RequeueAfter = deploymentRequeueAfter
	}
	return result, err
}

func (r *ModelDeploymentController) beginReplacementRollback(
	ctx context.Context,
	deployment, original *v1alpha1.ModelDeployment,
	message string,
) (ctrl.Result, error) {
	if _, err := r.deleteRuntimeWorkload(ctx, deployment); err != nil {
		return ctrl.Result{}, err
	}
	deployment.Status.Replacement.Phase = v1alpha1.ReplacementPhaseRollingBack
	deployment.Status.Replacement.Message = message
	deployment.Status.Phase = v1alpha1.ModelDeploymentPhaseDeactivating
	deployment.Status.AssignedGPUs = nil
	deployment.Status.Replicas = v1alpha1.ReplicaStatus{}
	deployment.Status.Model.Loaded = false
	conditionMessage := "Replacement activation failed; rollback is starting: " + message
	setRuntimeWaitingConditions(
		deployment,
		v1alpha1.ReasonReplacementActivationFailed,
		conditionMessage,
	)
	setDeploymentCondition(
		deployment,
		v1alpha1.ConditionGPUAssigned,
		metav1.ConditionUnknown,
		v1alpha1.ReasonReplacementRollbackStarted,
		"GPU slot is reserved for restoration of the displaced runtime",
	)
	setDeploymentCondition(
		deployment,
		v1alpha1.ConditionReplacement,
		metav1.ConditionFalse,
		v1alpha1.ReasonReplacementActivationFailed,
		conditionMessage,
	)
	result, err := r.patchDeploymentStatus(
		ctx,
		deployment,
		original,
		v1alpha1.ReasonReplacementActivationFailed,
	)
	if err == nil {
		result.RequeueAfter = deploymentRequeueAfter
	}
	return result, err
}

func (r *ModelDeploymentController) releaseReplacementTarget(
	ctx context.Context,
	targetRef *v1alpha1.ReplacementReference,
	requester *v1alpha1.ModelDeployment,
) error {
	if targetRef == nil || targetRef.Namespace != requester.Namespace {
		return nil
	}
	target, err := r.getReplacementParticipant(ctx, targetRef)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	requesterRef := referenceFor(requester)
	if target.Status.Replacement == nil ||
		!referencesEqual(target.Status.Replacement.RequestedBy, &requesterRef) {
		return nil
	}
	before := target.DeepCopy()
	target.Status.Replacement = nil
	if effectiveDesiredState(target) == v1alpha1.ActivationDesiredStateActive {
		target.Status.DrainStartedAt = nil
	}
	status.RemoveCondition(&target.Status.Conditions, v1alpha1.ConditionReplacement)
	if err := r.client.Status().Patch(ctx, target, client.MergeFrom(before)); err != nil {
		return fmt.Errorf("release replacement target %s/%s: %w", target.Namespace, target.Name, err)
	}
	return nil
}

func (r *ModelDeploymentController) getReplacementParticipant(
	ctx context.Context,
	ref *v1alpha1.ReplacementReference,
) (*v1alpha1.ModelDeployment, error) {
	if ref == nil {
		return nil, errors.New("replacement participant reference is required")
	}
	var deployment v1alpha1.ModelDeployment
	if err := r.client.Get(ctx, types.NamespacedName{
		Namespace: ref.Namespace,
		Name:      ref.Name,
	}, &deployment); err != nil {
		return nil, err
	}
	if ref.UID != "" && deployment.UID != ref.UID {
		return nil, apierrors.NewNotFound(v1alpha1.GroupVersion.WithResource("modeldeployments").GroupResource(), ref.Name)
	}
	return &deployment, nil
}

func referencesEqual(left, right *v1alpha1.ReplacementReference) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Namespace == right.Namespace &&
		left.Name == right.Name &&
		left.UID == right.UID
}

func runtimeActivationFailure(runtimeDeployment *appsv1.Deployment) (string, bool) {
	if runtimeDeployment.Status.ObservedGeneration < runtimeDeployment.Generation {
		return "", false
	}
	for _, condition := range runtimeDeployment.Status.Conditions {
		if condition.Type == appsv1.DeploymentProgressing &&
			condition.Status == corev1.ConditionFalse &&
			condition.Reason == "ProgressDeadlineExceeded" {
			return condition.Message, true
		}
		if condition.Type == appsv1.DeploymentReplicaFailure &&
			condition.Status == corev1.ConditionTrue {
			return condition.Message, true
		}
	}
	return "", false
}
