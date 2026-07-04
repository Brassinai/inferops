package controllers

import (
	"context"
	"strings"
	"testing"
	"time"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	"github.com/brassinai/inferops/operator/internal/cachecontract"
	controllermetrics "github.com/brassinai/inferops/operator/internal/metrics"
	"github.com/brassinai/inferops/operator/internal/resources"
	"github.com/brassinai/inferops/operator/internal/status"
	"github.com/brassinai/inferops/operator/internal/templates"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestLifecycleInactiveCreatesStableResourcesWithoutRuntime(t *testing.T) {
	t.Parallel()

	deployment := lifecycleDeployment(v1alpha1.ActivationDesiredStateInactive, v1alpha1.ActivationWhenFullQueue)
	c, reconciler := lifecycleTestController(t, deployment, lifecycleRuntime())
	reconcileLifecycle(t, reconciler, deployment)

	assertObjectExists(t, c, types.NamespacedName{Namespace: deployment.Namespace, Name: templates.RuntimeServiceName(deployment.Name)}, &corev1.Service{})
	assertObjectExists(t, c, types.NamespacedName{Namespace: deployment.Namespace, Name: deployment.Name + "-runtime-config"}, &corev1.ConfigMap{})
	assertObjectMissing(t, c, types.NamespacedName{Namespace: deployment.Namespace, Name: templates.RuntimeServiceName(deployment.Name)}, &appsv1.Deployment{})
	assertObjectMissing(t, c, types.NamespacedName{Namespace: deployment.Namespace, Name: templates.RuntimeServiceName(deployment.Name)}, &policyv1.PodDisruptionBudget{})

	cache := getLifecycleCache(t, c, deployment)
	if len(cache.OwnerReferences) != 0 {
		t.Fatalf("ModelCache ownerReferences = %#v, want independently retained cache", cache.OwnerReferences)
	}
	markLifecycleCacheReady(t, c, cache, "gpu-node-1")
	reconcileLifecycle(t, reconciler, deployment)

	var updated v1alpha1.ModelDeployment
	getObject(t, c, client.ObjectKeyFromObject(deployment), &updated)
	if updated.Status.Phase != v1alpha1.ModelDeploymentPhaseCached {
		t.Fatalf("phase = %q, want Cached", updated.Status.Phase)
	}
	if updated.Status.ServiceName != "qwen-runtime" {
		t.Errorf("serviceName = %q, want qwen-runtime", updated.Status.ServiceName)
	}
	assertLifecycleCondition(t, updated.Status.Conditions, v1alpha1.ConditionCacheReady, metav1.ConditionTrue, v1alpha1.ReasonCacheVerified)
	assertLifecycleCondition(t, updated.Status.Conditions, v1alpha1.ConditionReady, metav1.ConditionFalse, v1alpha1.ReasonInactive)
	assertObjectMissing(t, c, types.NamespacedName{Namespace: deployment.Namespace, Name: templates.RuntimeServiceName(deployment.Name)}, &appsv1.Deployment{})
}

func TestLifecycleActiveReservesGPUAndProjectsReadiness(t *testing.T) {
	t.Parallel()

	deployment := lifecycleDeployment(v1alpha1.ActivationDesiredStateActive, v1alpha1.ActivationWhenFullQueue)
	cache := lifecycleReadyCache(t, deployment, "gpu-node-1")
	node := lifecycleGPUNode("gpu-node-1", 1)
	c, reconciler := lifecycleTestController(t, deployment, lifecycleRuntime(), cache, node)

	reconcileLifecycle(t, reconciler, deployment)
	var runtimeDeployment appsv1.Deployment
	getObject(t, c, types.NamespacedName{Namespace: deployment.Namespace, Name: "qwen-runtime"}, &runtimeDeployment)
	var pdb policyv1.PodDisruptionBudget
	getObject(t, c, types.NamespacedName{Namespace: deployment.Namespace, Name: "qwen-runtime"}, &pdb)
	if pdb.Spec.MinAvailable == nil || pdb.Spec.MinAvailable.IntValue() != 1 {
		t.Fatalf("runtime PodDisruptionBudget minAvailable = %#v, want 1", pdb.Spec.MinAvailable)
	}
	if runtimeDeployment.Spec.Template.Spec.Affinity == nil {
		t.Fatal("runtime Deployment has no cache-local node affinity")
	}
	if got := runtimeDeployment.Spec.Template.Spec.Containers[0].Resources.Requests["nvidia.com/gpu"]; got.Value() != 1 {
		t.Errorf("GPU request = %s, want 1", got.String())
	}
	var activating v1alpha1.ModelDeployment
	getObject(t, c, client.ObjectKeyFromObject(deployment), &activating)
	if activating.Status.Phase != v1alpha1.ModelDeploymentPhaseActivating {
		t.Fatalf("phase = %q, want Activating", activating.Status.Phase)
	}
	if activating.Status.AssignedNode != "gpu-node-1" {
		t.Errorf("assignedNode = %q, want gpu-node-1", activating.Status.AssignedNode)
	}
	if len(activating.Status.AssignedGPUs) != 0 {
		t.Errorf("assignedGPUs = %#v, must not invent physical UUIDs", activating.Status.AssignedGPUs)
	}

	runtimeDeployment.Status.ObservedGeneration = runtimeDeployment.Generation
	runtimeDeployment.Status.ReadyReplicas = 1
	runtimeDeployment.Status.AvailableReplicas = 1
	if err := c.Status().Update(context.Background(), &runtimeDeployment); err != nil {
		t.Fatalf("update runtime Deployment status: %v", err)
	}
	reconcileLifecycle(t, reconciler, deployment)

	var active v1alpha1.ModelDeployment
	getObject(t, c, client.ObjectKeyFromObject(deployment), &active)
	if active.Status.Phase != v1alpha1.ModelDeploymentPhaseActive {
		t.Fatalf("phase = %q, want Active", active.Status.Phase)
	}
	assertLifecycleCondition(t, active.Status.Conditions, v1alpha1.ConditionGPUAssigned, metav1.ConditionTrue, v1alpha1.ReasonGPUCapacityReserved)
	assertLifecycleCondition(t, active.Status.Conditions, v1alpha1.ConditionReady, metav1.ConditionTrue, v1alpha1.ReasonRuntimeReady)
}

func TestLifecycleReadyRuntimeWinsOverStaleFailureCondition(t *testing.T) {
	t.Parallel()

	deployment := lifecycleDeployment(
		v1alpha1.ActivationDesiredStateActive,
		v1alpha1.ActivationWhenFullQueue,
	)
	cache := lifecycleReadyCache(t, deployment, "gpu-node-1")
	c, reconciler := lifecycleTestController(
		t,
		deployment,
		lifecycleRuntime(),
		cache,
		lifecycleGPUNode("gpu-node-1", 1),
	)
	reconcileLifecycle(t, reconciler, deployment)

	var runtimeDeployment appsv1.Deployment
	getObject(
		t,
		c,
		types.NamespacedName{Namespace: deployment.Namespace, Name: "qwen-runtime"},
		&runtimeDeployment,
	)
	runtimeDeployment.Status.ObservedGeneration = runtimeDeployment.Generation
	runtimeDeployment.Status.ReadyReplicas = 1
	runtimeDeployment.Status.AvailableReplicas = 1
	runtimeDeployment.Status.Conditions = []appsv1.DeploymentCondition{{
		Type:    appsv1.DeploymentProgressing,
		Status:  corev1.ConditionFalse,
		Reason:  "ProgressDeadlineExceeded",
		Message: "condition was not cleared after readiness",
	}}
	if err := c.Status().Update(context.Background(), &runtimeDeployment); err != nil {
		t.Fatalf("update runtime status: %v", err)
	}
	reconcileLifecycle(t, reconciler, deployment)

	var updated v1alpha1.ModelDeployment
	getObject(t, c, client.ObjectKeyFromObject(deployment), &updated)
	if updated.Status.Phase != v1alpha1.ModelDeploymentPhaseActive {
		t.Fatalf("phase = %q, want Active", updated.Status.Phase)
	}
}

func TestLifecycleRecoversAfterCacheRetryWithoutSpecChange(t *testing.T) {
	t.Parallel()

	deployment := lifecycleDeployment(
		v1alpha1.ActivationDesiredStateActive,
		v1alpha1.ActivationWhenFullQueue,
	)
	deployment.Status.ObservedGeneration = deployment.Generation
	deployment.Status.Phase = v1alpha1.ModelDeploymentPhaseFailed
	deployment.Status.Conditions = []v1alpha1.Condition{{
		Type:               v1alpha1.ConditionReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: deployment.Generation,
		Reason:             v1alpha1.ReasonCacheFailed,
		Message:            "previous cache download failed",
	}}
	cache := lifecycleReadyCache(t, deployment, "gpu-node-1")
	c, reconciler := lifecycleTestController(
		t,
		deployment,
		lifecycleRuntime(),
		cache,
		lifecycleGPUNode("gpu-node-1", 1),
	)

	reconcileLifecycle(t, reconciler, deployment)

	var updated v1alpha1.ModelDeployment
	getObject(t, c, client.ObjectKeyFromObject(deployment), &updated)
	if updated.Status.Phase != v1alpha1.ModelDeploymentPhaseActivating {
		t.Fatalf("phase = %q, want Activating after cache retry", updated.Status.Phase)
	}
	assertObjectExists(
		t,
		c,
		types.NamespacedName{Namespace: deployment.Namespace, Name: "qwen-runtime"},
		&appsv1.Deployment{},
	)
}

func TestLifecycleRuntimeNodePreflight(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mutate     func(*v1alpha1.ModelDeployment, *corev1.Node)
		wantPhase  v1alpha1.ModelDeploymentPhase
		wantReason string
	}{
		{
			name: "insufficient aggregate CPU waits",
			mutate: func(deployment *v1alpha1.ModelDeployment, node *corev1.Node) {
				deployment.Spec.Resources.CPU = "64"
				node.Status.Allocatable[corev1.ResourceCPU] = resource.MustParse("32")
			},
			wantPhase:  v1alpha1.ModelDeploymentPhaseWaitingForCapacity,
			wantReason: v1alpha1.ReasonInsufficientCompute,
		},
		{
			name: "node selector mismatch fails visibly",
			mutate: func(deployment *v1alpha1.ModelDeployment, node *corev1.Node) {
				deployment.Spec.Scheduling.NodeSelector = map[string]string{
					"inferops.dev/pool": "inference",
				}
				node.Labels = map[string]string{"inferops.dev/pool": "general"}
			},
			wantPhase:  v1alpha1.ModelDeploymentPhaseFailed,
			wantReason: v1alpha1.ReasonSchedulingBlocked,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			deployment := lifecycleDeployment(
				v1alpha1.ActivationDesiredStateActive,
				v1alpha1.ActivationWhenFullQueue,
			)
			node := lifecycleGPUNode("gpu-node-1", 1)
			test.mutate(deployment, node)
			cache := lifecycleReadyCache(t, deployment, "gpu-node-1")
			c, reconciler := lifecycleTestController(t, deployment, lifecycleRuntime(), cache, node)

			reconcileLifecycle(t, reconciler, deployment)
			var updated v1alpha1.ModelDeployment
			getObject(t, c, client.ObjectKeyFromObject(deployment), &updated)
			if updated.Status.Phase != test.wantPhase {
				t.Fatalf("phase = %q, want %q", updated.Status.Phase, test.wantPhase)
			}
			assertLifecycleCondition(
				t,
				updated.Status.Conditions,
				v1alpha1.ConditionReady,
				metav1.ConditionFalse,
				test.wantReason,
			)
			key := types.NamespacedName{Namespace: deployment.Namespace, Name: "qwen-runtime"}
			assertObjectMissing(t, c, key, &appsv1.Deployment{})
			assertObjectMissing(t, c, key, &policyv1.PodDisruptionBudget{})
		})
	}
}

func TestLifecycleGPUWhenFullPolicies(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		whenFull   v1alpha1.ActivationWhenFull
		wantPhase  v1alpha1.ModelDeploymentPhase
		wantReason string
	}{
		{
			name: "queue", whenFull: v1alpha1.ActivationWhenFullQueue,
			wantPhase:  v1alpha1.ModelDeploymentPhaseWaitingForGPU,
			wantReason: v1alpha1.ReasonWaitingForGPU,
		},
		{
			name: "reject", whenFull: v1alpha1.ActivationWhenFullReject,
			wantPhase:  v1alpha1.ModelDeploymentPhaseFailed,
			wantReason: v1alpha1.ReasonInsufficientGPU,
		},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			deployment := lifecycleDeployment(v1alpha1.ActivationDesiredStateActive, tt.whenFull)
			cache := lifecycleReadyCache(t, deployment, "gpu-node-1")
			other := lifecycleDeployment(v1alpha1.ActivationDesiredStateActive, v1alpha1.ActivationWhenFullQueue)
			other.Name = "occupied"
			other.UID = "occupied-uid"
			other.Status.Phase = v1alpha1.ModelDeploymentPhaseActive
			other.Status.AssignedNode = "gpu-node-1"
			node := lifecycleGPUNode("gpu-node-1", 1)
			c, reconciler := lifecycleTestController(t, deployment, other, lifecycleRuntime(), cache, node)

			reconcileLifecycle(t, reconciler, deployment)
			var updated v1alpha1.ModelDeployment
			getObject(t, c, client.ObjectKeyFromObject(deployment), &updated)
			if updated.Status.Phase != tt.wantPhase {
				t.Fatalf("phase = %q, want %q", updated.Status.Phase, tt.wantPhase)
			}
			assertLifecycleCondition(t, updated.Status.Conditions, v1alpha1.ConditionReady, metav1.ConditionFalse, tt.wantReason)
			assertObjectMissing(t, c, types.NamespacedName{Namespace: deployment.Namespace, Name: "qwen-runtime"}, &appsv1.Deployment{})
			getObject(t, c, client.ObjectKeyFromObject(other), &updated)
			if updated.Status.Replacement != nil {
				t.Errorf("%s policy modified active deployment replacement status: %#v", tt.whenFull, updated.Status.Replacement)
			}
		})
	}
}

func TestLifecycleRepeatedReconcileDoesNotChurnRuntime(t *testing.T) {
	t.Parallel()

	deployment := lifecycleDeployment(v1alpha1.ActivationDesiredStateActive, v1alpha1.ActivationWhenFullQueue)
	cache := lifecycleReadyCache(t, deployment, "gpu-node-1")
	c, reconciler := lifecycleTestController(t, deployment, lifecycleRuntime(), cache, lifecycleGPUNode("gpu-node-1", 2))
	reconcileLifecycle(t, reconciler, deployment)

	var first appsv1.Deployment
	getObject(t, c, types.NamespacedName{Namespace: deployment.Namespace, Name: "qwen-runtime"}, &first)
	patchesBefore := c.(*patchCountingClient).deploymentPatches
	statusPatchesBefore := c.(*patchCountingClient).modelDeploymentStatusPatches
	reconcileLifecycle(t, reconciler, deployment)
	var second appsv1.Deployment
	getObject(t, c, types.NamespacedName{Namespace: deployment.Namespace, Name: "qwen-runtime"}, &second)
	if first.UID != second.UID {
		t.Errorf("runtime Deployment UID changed: %q != %q", first.UID, second.UID)
	}
	if got := c.(*patchCountingClient).deploymentPatches; got != patchesBefore {
		t.Errorf("runtime Deployment patch count changed: %d -> %d: %v",
			patchesBefore, got, c.(*patchCountingClient).deploymentPatchData)
	}
	if got := c.(*patchCountingClient).modelDeploymentStatusPatches; got != statusPatchesBefore {
		t.Errorf("ModelDeployment status patch count changed: %d -> %d", statusPatchesBefore, got)
	}
}

func TestLifecycleDeactivationTouchesCacheOnce(t *testing.T) {
	t.Parallel()

	deployment := lifecycleDeployment(v1alpha1.ActivationDesiredStateActive, v1alpha1.ActivationWhenFullQueue)
	cache := lifecycleReadyCache(t, deployment, "gpu-node-1")
	c, reconciler := lifecycleTestController(
		t, deployment, lifecycleRuntime(), cache, lifecycleGPUNode("gpu-node-1", 1),
	)
	reconcileLifecycle(t, reconciler, deployment)

	var activeCache v1alpha1.ModelCache
	getObject(t, c, client.ObjectKeyFromObject(cache), &activeCache)
	activeCache.Status.LastUsedTime = metav1.NewTime(time.Now().Add(-time.Hour))
	if err := c.Status().Update(context.Background(), &activeCache); err != nil {
		t.Fatalf("set prior cache use time: %v", err)
	}
	firstUse := activeCache.Status.LastUsedTime
	var latest v1alpha1.ModelDeployment
	getObject(t, c, client.ObjectKeyFromObject(deployment), &latest)
	latest.Spec.Activation.DesiredState = v1alpha1.ActivationDesiredStateInactive
	latest.Generation++
	if err := c.Update(context.Background(), &latest); err != nil {
		t.Fatalf("update desired state: %v", err)
	}
	reconcileLifecycle(t, reconciler, deployment)

	getObject(t, c, client.ObjectKeyFromObject(cache), &activeCache)
	if !activeCache.Status.LastUsedTime.After(firstUse.Time) {
		t.Errorf("lastUsedTime = %s, want after activation time %s",
			activeCache.Status.LastUsedTime, firstUse)
	}
	deactivatedUse := activeCache.Status.LastUsedTime
	reconcileLifecycle(t, reconciler, deployment)
	getObject(t, c, client.ObjectKeyFromObject(cache), &activeCache)
	if !activeCache.Status.LastUsedTime.Equal(&deactivatedUse) {
		t.Errorf("ordinary inactive reconcile changed lastUsedTime: %s -> %s",
			deactivatedUse, activeCache.Status.LastUsedTime)
	}
}

func TestLifecycleDeactivationDrainsBeforeDeletingRuntime(t *testing.T) {
	t.Parallel()

	deployment := lifecycleDeployment(v1alpha1.ActivationDesiredStateActive, v1alpha1.ActivationWhenFullQueue)
	deployment.Spec.Activation.DrainTimeout = "1s"
	cache := lifecycleReadyCache(t, deployment, "gpu-node-1")
	c, reconciler := lifecycleTestController(
		t,
		deployment,
		lifecycleRuntime(),
		cache,
		lifecycleGPUNode("gpu-node-1", 1),
	)
	reconcileLifecycle(t, reconciler, deployment)
	markLifecycleRuntimeReady(t, c, deployment)
	reconcileLifecycle(t, reconciler, deployment)

	var latest v1alpha1.ModelDeployment
	getObject(t, c, client.ObjectKeyFromObject(deployment), &latest)
	latest.Spec.Activation.DesiredState = v1alpha1.ActivationDesiredStateInactive
	latest.Generation++
	if err := c.Update(context.Background(), &latest); err != nil {
		t.Fatalf("request deactivation: %v", err)
	}
	reconcileLifecycle(t, reconciler, deployment)

	getObject(t, c, client.ObjectKeyFromObject(deployment), &latest)
	if latest.Status.Phase != v1alpha1.ModelDeploymentPhaseDraining {
		t.Fatalf("phase = %q, want Draining", latest.Status.Phase)
	}
	if latest.Status.DrainStartedAt == nil {
		t.Fatal("drainStartedAt is nil")
	}
	if !latest.Status.Model.Loaded {
		t.Error("draining runtime was reported unloaded before termination")
	}
	assertLifecycleCondition(
		t,
		latest.Status.Conditions,
		v1alpha1.ConditionRoutingReady,
		metav1.ConditionFalse,
		v1alpha1.ReasonDrainStarted,
	)
	assertObjectExists(
		t,
		c,
		types.NamespacedName{Namespace: deployment.Namespace, Name: "qwen-runtime"},
		&appsv1.Deployment{},
	)
	var runtimeService corev1.Service
	getObject(
		t,
		c,
		types.NamespacedName{Namespace: deployment.Namespace, Name: "qwen-runtime"},
		&runtimeService,
	)
	if len(runtimeService.Spec.Selector) != 0 {
		t.Errorf("draining Service selector = %#v, want no selected endpoints", runtimeService.Spec.Selector)
	}
	reconcileLifecycle(t, reconciler, deployment)
	getObject(t, c, client.ObjectKeyFromObject(deployment), &latest)
	if latest.Status.Phase != v1alpha1.ModelDeploymentPhaseDraining {
		t.Fatalf("phase during graceful shutdown = %q, want Draining", latest.Status.Phase)
	}
	assertObjectMissing(
		t,
		c,
		types.NamespacedName{Namespace: deployment.Namespace, Name: "qwen-runtime"},
		&appsv1.Deployment{},
	)

	expired := metav1.NewTime(time.Now().Add(-2 * time.Second))
	latest.Status.DrainStartedAt = &expired
	if err := c.Status().Update(context.Background(), &latest); err != nil {
		t.Fatalf("expire drain timeout: %v", err)
	}
	reconcileLifecycle(t, reconciler, deployment)
	getObject(t, c, client.ObjectKeyFromObject(deployment), &latest)
	if latest.Status.Phase != v1alpha1.ModelDeploymentPhaseDeactivating {
		t.Fatalf("phase = %q, want Deactivating", latest.Status.Phase)
	}
	assertObjectMissing(
		t,
		c,
		types.NamespacedName{Namespace: deployment.Namespace, Name: "qwen-runtime"},
		&appsv1.Deployment{},
	)

	reconcileLifecycle(t, reconciler, deployment)
	getObject(t, c, client.ObjectKeyFromObject(deployment), &latest)
	if latest.Status.Phase != v1alpha1.ModelDeploymentPhaseCached {
		t.Fatalf("phase = %q, want Cached", latest.Status.Phase)
	}
	if latest.Status.DrainStartedAt != nil {
		t.Errorf("drainStartedAt = %s, want nil", latest.Status.DrainStartedAt)
	}
	assertObjectExists(
		t,
		c,
		types.NamespacedName{Namespace: deployment.Namespace, Name: "qwen-runtime"},
		&corev1.Service{},
	)
	var retainedCache v1alpha1.ModelCache
	getObject(t, c, client.ObjectKeyFromObject(cache), &retainedCache)
	if retainedCache.UID != cache.UID {
		t.Fatalf("retained cache UID = %q, want %q", retainedCache.UID, cache.UID)
	}

	latest.Spec.Activation.DesiredState = v1alpha1.ActivationDesiredStateActive
	latest.Generation++
	if err := c.Update(context.Background(), &latest); err != nil {
		t.Fatalf("request reactivation: %v", err)
	}
	reconcileLifecycle(t, reconciler, deployment)
	assertObjectExists(
		t,
		c,
		types.NamespacedName{Namespace: deployment.Namespace, Name: "qwen-runtime"},
		&appsv1.Deployment{},
	)
	getObject(
		t,
		c,
		types.NamespacedName{Namespace: deployment.Namespace, Name: "qwen-runtime"},
		&runtimeService,
	)
	if runtimeService.Spec.Selector[resources.LabelModelDeployment] != deployment.Name {
		t.Errorf("reactivated Service selector = %#v, want runtime selector", runtimeService.Spec.Selector)
	}
	getObject(t, c, client.ObjectKeyFromObject(cache), &retainedCache)
	if retainedCache.UID != cache.UID {
		t.Fatalf("reactivation replaced cache UID %q with %q", cache.UID, retainedCache.UID)
	}
}

func TestLifecycleSingleGPUReplacementSucceeds(t *testing.T) {
	t.Parallel()

	c, reconciler, incoming, victim := lifecycleReplacementFixture(
		t,
		v1alpha1.ActivationWhenFullReplaceOldest,
	)
	startAndDrainReplacement(t, c, reconciler, incoming, victim)

	reconcileLifecycle(t, reconciler, incoming)
	var replacementRuntime appsv1.Deployment
	getObject(
		t,
		c,
		types.NamespacedName{Namespace: incoming.Namespace, Name: "qwen-runtime"},
		&replacementRuntime,
	)
	markLifecycleRuntimeReady(t, c, incoming)
	reconcileLifecycle(t, reconciler, incoming)

	var updated v1alpha1.ModelDeployment
	getObject(t, c, client.ObjectKeyFromObject(incoming), &updated)
	if updated.Status.Phase != v1alpha1.ModelDeploymentPhaseActive {
		t.Fatalf("replacement phase = %q, want Active", updated.Status.Phase)
	}
	if updated.Status.Replacement == nil ||
		updated.Status.Replacement.Phase != v1alpha1.ReplacementPhaseSucceeded {
		t.Fatalf("replacement status = %#v, want Succeeded", updated.Status.Replacement)
	}
	assertLifecycleCondition(
		t,
		updated.Status.Conditions,
		v1alpha1.ConditionReplacement,
		metav1.ConditionTrue,
		v1alpha1.ReasonReplacementSucceeded,
	)
	assertRecordedEventReasons(t, reconciler, v1alpha1.ReasonReplacementSucceeded)

	reconcileLifecycle(t, reconciler, victim)
	getObject(t, c, client.ObjectKeyFromObject(victim), &updated)
	if updated.Status.Phase != v1alpha1.ModelDeploymentPhaseWaitingForGPU {
		t.Fatalf("displaced phase = %q, want WaitingForGPU", updated.Status.Phase)
	}
	assertLifecycleCondition(
		t,
		updated.Status.Conditions,
		v1alpha1.ConditionReplacement,
		metav1.ConditionFalse,
		v1alpha1.ReasonReplacementDisplaced,
	)
}

func TestLifecycleSingleGPUReplacementFailureRollsBack(t *testing.T) {
	t.Parallel()

	c, reconciler, incoming, victim := lifecycleReplacementFixture(
		t,
		v1alpha1.ActivationWhenFullReplaceOldest,
	)
	startAndDrainReplacement(t, c, reconciler, incoming, victim)
	reconcileLifecycle(t, reconciler, incoming)
	markLifecycleRuntimeFailed(t, c, incoming, "replacement image could not start")
	reconcileLifecycle(t, reconciler, incoming)

	var updated v1alpha1.ModelDeployment
	getObject(t, c, client.ObjectKeyFromObject(incoming), &updated)
	if updated.Status.Replacement == nil ||
		updated.Status.Replacement.Phase != v1alpha1.ReplacementPhaseRollingBack {
		t.Fatalf("replacement status = %#v, want RollingBack", updated.Status.Replacement)
	}
	if updated.Status.AssignedNode != "gpu-node-1" {
		t.Fatalf("rollback handoff node = %q, want gpu-node-1", updated.Status.AssignedNode)
	}
	observer := lifecycleDeployment(
		v1alpha1.ActivationDesiredStateActive,
		v1alpha1.ActivationWhenFullQueue,
	)
	observer.Name = "rollback-observer"
	allocations, err := reconciler.gpuAllocations(context.Background(), observer)
	if err != nil {
		t.Fatalf("rollback gpuAllocations() error = %v", err)
	}
	if len(allocations) != 1 || allocations[0].NodeName != "gpu-node-1" {
		t.Fatalf("rollback allocations = %#v, want protected handoff", allocations)
	}
	assertLifecycleCondition(
		t,
		updated.Status.Conditions,
		v1alpha1.ConditionReplacement,
		metav1.ConditionFalse,
		v1alpha1.ReasonReplacementActivationFailed,
	)
	var replacementService corev1.Service
	getObject(
		t,
		c,
		types.NamespacedName{Namespace: incoming.Namespace, Name: templates.RuntimeServiceName(incoming.Name)},
		&replacementService,
	)
	if len(replacementService.Spec.Selector) != 0 {
		t.Errorf("failed replacement Service selector = %#v, want no selected endpoints", replacementService.Spec.Selector)
	}

	reconcileLifecycle(t, reconciler, incoming)
	reconcileLifecycle(t, reconciler, victim)
	markLifecycleRuntimeReady(t, c, victim)
	reconcileLifecycle(t, reconciler, victim)
	reconcileLifecycle(t, reconciler, incoming)

	getObject(t, c, client.ObjectKeyFromObject(incoming), &updated)
	if updated.Status.Replacement == nil ||
		updated.Status.Replacement.Phase != v1alpha1.ReplacementPhaseRolledBack {
		t.Fatalf("replacement status = %#v, want RolledBack", updated.Status.Replacement)
	}
	assertLifecycleCondition(
		t,
		updated.Status.Conditions,
		v1alpha1.ConditionReady,
		metav1.ConditionFalse,
		v1alpha1.ReasonReplacementRolledBack,
	)
	getObject(t, c, client.ObjectKeyFromObject(victim), &updated)
	if updated.Status.Phase != v1alpha1.ModelDeploymentPhaseActive {
		t.Fatalf("restored victim phase = %q, want Active", updated.Status.Phase)
	}
	reconcileLifecycle(t, reconciler, incoming)
	getObject(t, c, client.ObjectKeyFromObject(incoming), &updated)
	if updated.Status.Replacement == nil ||
		updated.Status.Replacement.Phase != v1alpha1.ReplacementPhaseRolledBack {
		t.Fatalf("terminal rollback outcome was not durable: %#v", updated.Status.Replacement)
	}
	assertLifecycleCondition(
		t,
		updated.Status.Conditions,
		v1alpha1.ConditionCacheReady,
		metav1.ConditionTrue,
		v1alpha1.ReasonCacheVerified,
	)
	assertRecordedEventReasons(
		t,
		reconciler,
		v1alpha1.ReasonReplacementActivationFailed,
		v1alpha1.ReasonReplacementRolledBack,
	)
	updated.Spec.Activation.WhenFull = v1alpha1.ActivationWhenFullQueue
	updated.Generation++
	if err := c.Update(context.Background(), &updated); err != nil {
		t.Fatalf("retry rolled-back deployment: %v", err)
	}
	reconcileLifecycle(t, reconciler, incoming)
	getObject(t, c, client.ObjectKeyFromObject(incoming), &updated)
	if updated.Status.Phase != v1alpha1.ModelDeploymentPhaseWaitingForGPU {
		t.Fatalf("retried phase = %q, want WaitingForGPU", updated.Status.Phase)
	}
	if updated.Status.Replacement != nil {
		t.Fatalf("retry retained terminal replacement state: %#v", updated.Status.Replacement)
	}
}

func TestLifecycleSingleGPUReplacementReportsFailedRollback(t *testing.T) {
	t.Parallel()

	c, reconciler, incoming, victim := lifecycleReplacementFixture(
		t,
		v1alpha1.ActivationWhenFullReplaceOldest,
	)
	startAndDrainReplacement(t, c, reconciler, incoming, victim)
	reconcileLifecycle(t, reconciler, incoming)
	markLifecycleRuntimeFailed(t, c, incoming, "replacement failed")
	reconcileLifecycle(t, reconciler, incoming)
	reconcileLifecycle(t, reconciler, incoming)
	reconcileLifecycle(t, reconciler, victim)
	markLifecycleRuntimeFailed(t, c, victim, "rollback failed")
	reconcileLifecycle(t, reconciler, victim)
	reconcileLifecycle(t, reconciler, incoming)

	var updated v1alpha1.ModelDeployment
	getObject(t, c, client.ObjectKeyFromObject(incoming), &updated)
	if updated.Status.Replacement == nil ||
		updated.Status.Replacement.Phase != v1alpha1.ReplacementPhaseRollbackFailed {
		t.Fatalf("replacement status = %#v, want RollbackFailed", updated.Status.Replacement)
	}
	assertLifecycleCondition(
		t,
		updated.Status.Conditions,
		v1alpha1.ConditionReady,
		metav1.ConditionFalse,
		v1alpha1.ReasonReplacementRollbackFailed,
	)
	assertRecordedEventReasons(
		t,
		reconciler,
		v1alpha1.ReasonReplacementActivationFailed,
		v1alpha1.ReasonReplacementRollbackFailed,
	)
	getObject(t, c, client.ObjectKeyFromObject(victim), &updated)
	if updated.Status.Replacement != nil {
		t.Errorf("failed rollback left target transaction state behind: %#v", updated.Status.Replacement)
	}
}

func TestLifecycleReplacementHonorsTargetDeactivation(t *testing.T) {
	t.Parallel()

	c, reconciler, incoming, victim := lifecycleReplacementFixture(
		t,
		v1alpha1.ActivationWhenFullReplaceOldest,
	)
	reconcileLifecycle(t, reconciler, incoming)
	reconcileLifecycle(t, reconciler, victim)

	var updated v1alpha1.ModelDeployment
	getObject(t, c, client.ObjectKeyFromObject(victim), &updated)
	if updated.Status.DrainStartedAt == nil {
		t.Fatal("replacement target has no drain start")
	}
	drainStartedAt := updated.Status.DrainStartedAt.DeepCopy()
	updated.Spec.Activation.DesiredState = v1alpha1.ActivationDesiredStateInactive
	updated.Generation++
	if err := c.Update(context.Background(), &updated); err != nil {
		t.Fatalf("deactivate replacement target: %v", err)
	}
	reconcileLifecycle(t, reconciler, victim)

	getObject(t, c, client.ObjectKeyFromObject(victim), &updated)
	if updated.Status.Replacement != nil {
		t.Fatalf("inactive target remained replacement-locked: %#v", updated.Status.Replacement)
	}
	if updated.Status.DrainStartedAt == nil ||
		!updated.Status.DrainStartedAt.Equal(drainStartedAt) {
		t.Fatalf(
			"inactive target drain start = %v, want preserved %v",
			updated.Status.DrainStartedAt,
			drainStartedAt,
		)
	}
	reconcileLifecycle(t, reconciler, incoming)
	getObject(t, c, client.ObjectKeyFromObject(victim), &updated)
	if updated.Status.Replacement != nil {
		t.Fatalf("requester re-locked inactive target: %#v", updated.Status.Replacement)
	}
}

func TestLifecycleReplacementPolicyRevocationCancelsDrain(t *testing.T) {
	t.Parallel()

	c, reconciler, incoming, victim := lifecycleReplacementFixture(
		t,
		v1alpha1.ActivationWhenFullReplaceOldest,
	)
	reconcileLifecycle(t, reconciler, incoming)
	reconcileLifecycle(t, reconciler, victim)

	var updated v1alpha1.ModelDeployment
	getObject(t, c, client.ObjectKeyFromObject(incoming), &updated)
	updated.Spec.Activation.WhenFull = v1alpha1.ActivationWhenFullQueue
	updated.Generation++
	if err := c.Update(context.Background(), &updated); err != nil {
		t.Fatalf("revoke replacement policy: %v", err)
	}
	reconcileLifecycle(t, reconciler, incoming)

	getObject(t, c, client.ObjectKeyFromObject(incoming), &updated)
	if updated.Status.Replacement != nil {
		t.Fatalf("revoked requester retained replacement state: %#v", updated.Status.Replacement)
	}
	if updated.Status.Phase != v1alpha1.ModelDeploymentPhaseWaitingForGPU {
		t.Fatalf("revoked requester phase = %q, want WaitingForGPU", updated.Status.Phase)
	}
	getObject(t, c, client.ObjectKeyFromObject(victim), &updated)
	if updated.Status.Replacement != nil {
		t.Fatalf("revoked target retained replacement state: %#v", updated.Status.Replacement)
	}

	reconcileLifecycle(t, reconciler, victim)
	getObject(t, c, client.ObjectKeyFromObject(victim), &updated)
	if updated.Status.Phase != v1alpha1.ModelDeploymentPhaseActive {
		t.Fatalf("victim phase after revocation = %q, want Active", updated.Status.Phase)
	}
}

func TestLifecycleReplacementTargetSelection(t *testing.T) {
	t.Parallel()

	requester := lifecycleDeployment(
		v1alpha1.ActivationDesiredStateActive,
		v1alpha1.ActivationWhenFullReplaceLowestPriority,
	)
	requester.Spec.Activation.Priority = 50
	requester.Status.Cache.NodeName = "gpu-node-1"

	lowPriority := lifecycleDeployment(
		v1alpha1.ActivationDesiredStateActive,
		v1alpha1.ActivationWhenFullQueue,
	)
	lowPriority.Name = "low-priority"
	lowPriority.UID = "low-priority-uid"
	lowPriority.Spec.Activation.Priority = 10
	lowPriority.Status.Phase = v1alpha1.ModelDeploymentPhaseActive
	lowPriority.Status.ObservedGeneration = lowPriority.Generation
	lowPriority.Status.AssignedNode = "gpu-node-1"

	higherPriority := lowPriority.DeepCopy()
	higherPriority.Name = "higher-priority"
	higherPriority.UID = "higher-priority-uid"
	higherPriority.Spec.Activation.Priority = 20

	equalPriority := lowPriority.DeepCopy()
	equalPriority.Name = "equal-priority"
	equalPriority.UID = "equal-priority-uid"
	equalPriority.Spec.Activation.Priority = 50

	otherNamespace := lowPriority.DeepCopy()
	otherNamespace.Namespace = "other-team"
	otherNamespace.Name = "cross-namespace-lowest"
	otherNamespace.UID = "cross-namespace-lowest-uid"
	otherNamespace.Spec.Activation.Priority = -100

	_, reconciler := lifecycleTestController(
		t,
		requester,
		lowPriority,
		higherPriority,
		equalPriority,
		otherNamespace,
		lifecycleRuntime(),
	)
	selected, err := reconciler.selectReplacementTarget(context.Background(), requester)
	if err != nil {
		t.Fatalf("selectReplacementTarget() error = %v", err)
	}
	if selected == nil || selected.Name != lowPriority.Name {
		t.Fatalf("selected target = %#v, want %q", selected, lowPriority.Name)
	}
}

func TestLifecycleReplacementSelectsOldestReadyDeployment(t *testing.T) {
	t.Parallel()

	requester := lifecycleDeployment(
		v1alpha1.ActivationDesiredStateActive,
		v1alpha1.ActivationWhenFullReplaceOldest,
	)
	requester.Status.Cache.NodeName = "gpu-node-1"

	oldest := lifecycleDeployment(
		v1alpha1.ActivationDesiredStateActive,
		v1alpha1.ActivationWhenFullQueue,
	)
	oldest.Name = "oldest"
	oldest.UID = "oldest-uid"
	oldest.Status.Phase = v1alpha1.ModelDeploymentPhaseActive
	oldest.Status.ObservedGeneration = oldest.Generation
	oldest.Status.AssignedNode = "gpu-node-1"
	oldest.Status.Conditions = []v1alpha1.Condition{{
		Type:               v1alpha1.ConditionReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: oldest.Generation,
		LastTransitionTime: metav1.NewTime(time.Now().Add(-time.Hour)),
	}}

	newer := oldest.DeepCopy()
	newer.Name = "newer"
	newer.UID = "newer-uid"
	newer.Status.Conditions[0].LastTransitionTime = metav1.NewTime(time.Now().Add(-time.Minute))

	_, reconciler := lifecycleTestController(
		t,
		requester,
		oldest,
		newer,
		lifecycleRuntime(),
	)
	selected, err := reconciler.selectReplacementTarget(context.Background(), requester)
	if err != nil {
		t.Fatalf("selectReplacementTarget() error = %v", err)
	}
	if selected == nil || selected.Name != oldest.Name {
		t.Fatalf("selected target = %#v, want %q", selected, oldest.Name)
	}
}

func TestLifecycleSuccessfulReplacementCanLaterBeReplaced(t *testing.T) {
	t.Parallel()

	previousTarget := lifecycleDeployment(
		v1alpha1.ActivationDesiredStateActive,
		v1alpha1.ActivationWhenFullQueue,
	)
	previousTarget.Name = "previous-target"
	previousTarget.UID = "previous-target-uid"

	active := lifecycleDeployment(
		v1alpha1.ActivationDesiredStateActive,
		v1alpha1.ActivationWhenFullQueue,
	)
	active.Name = "active"
	active.UID = "active-uid"
	active.Status.Phase = v1alpha1.ModelDeploymentPhaseActive
	active.Status.ObservedGeneration = active.Generation
	active.Status.AssignedNode = "gpu-node-1"
	activeRef := referenceFor(active)
	previousTargetRef := referenceFor(previousTarget)
	active.Status.Replacement = &v1alpha1.ReplacementStatus{
		Phase:  v1alpha1.ReplacementPhaseSucceeded,
		Target: &previousTargetRef,
	}
	previousTarget.Status.Replacement = &v1alpha1.ReplacementStatus{
		Phase:       v1alpha1.ReplacementPhaseDisplaced,
		RequestedBy: &activeRef,
	}

	requester := lifecycleDeployment(
		v1alpha1.ActivationDesiredStateActive,
		v1alpha1.ActivationWhenFullReplaceOldest,
	)
	requester.Status.Cache.NodeName = "gpu-node-1"
	c, reconciler := lifecycleTestController(
		t,
		requester,
		active,
		previousTarget,
		lifecycleRuntime(),
	)

	selected, err := reconciler.selectReplacementTarget(context.Background(), requester)
	if err != nil {
		t.Fatalf("selectReplacementTarget() error = %v", err)
	}
	if selected == nil || selected.Name != active.Name {
		t.Fatalf("selected target = %#v, want prior replacement %q", selected, active.Name)
	}
	requesterRef := referenceFor(requester)
	if err := reconciler.markReplacementTarget(context.Background(), selected, requesterRef); err != nil {
		t.Fatalf("markReplacementTarget() error = %v", err)
	}

	var updated v1alpha1.ModelDeployment
	getObject(t, c, client.ObjectKeyFromObject(active), &updated)
	if updated.Status.Replacement == nil ||
		!referencesEqual(updated.Status.Replacement.RequestedBy, &requesterRef) {
		t.Fatalf("active replacement state = %#v, want new requester", updated.Status.Replacement)
	}
	getObject(t, c, client.ObjectKeyFromObject(previousTarget), &updated)
	if updated.Status.Replacement != nil {
		t.Fatalf("previous target remained locked: %#v", updated.Status.Replacement)
	}
}

func TestLifecycleStaleHandoffDoesNotHideRecreatedTargetAllocation(t *testing.T) {
	t.Parallel()

	requester := lifecycleDeployment(
		v1alpha1.ActivationDesiredStateActive,
		v1alpha1.ActivationWhenFullReplaceOldest,
	)
	requester.Status.Phase = v1alpha1.ModelDeploymentPhaseWaitingForGPU
	requester.Status.AssignedNode = "gpu-node-1"
	requester.Status.Replacement = &v1alpha1.ReplacementStatus{
		Phase: v1alpha1.ReplacementPhaseDraining,
		Target: &v1alpha1.ReplacementReference{
			Namespace: requester.Namespace,
			Name:      "target",
			UID:       "deleted-target-uid",
		},
	}

	recreated := lifecycleDeployment(
		v1alpha1.ActivationDesiredStateActive,
		v1alpha1.ActivationWhenFullQueue,
	)
	recreated.Name = "target"
	recreated.UID = "recreated-target-uid"
	recreated.Status.Phase = v1alpha1.ModelDeploymentPhaseActive
	recreated.Status.ObservedGeneration = recreated.Generation
	recreated.Status.AssignedNode = "gpu-node-1"

	observer := lifecycleDeployment(
		v1alpha1.ActivationDesiredStateActive,
		v1alpha1.ActivationWhenFullQueue,
	)
	observer.Name = "observer"
	_, reconciler := lifecycleTestController(
		t,
		requester,
		recreated,
		observer,
		lifecycleRuntime(),
	)
	allocations, err := reconciler.gpuAllocations(context.Background(), observer)
	if err != nil {
		t.Fatalf("gpuAllocations() error = %v", err)
	}
	if len(allocations) != 2 {
		t.Fatalf("allocations = %#v, want handoff and recreated target reservations", allocations)
	}
}

func TestLifecycleRejectsPersistedCrossNamespaceReplacement(t *testing.T) {
	t.Parallel()

	requester := lifecycleDeployment(
		v1alpha1.ActivationDesiredStateActive,
		v1alpha1.ActivationWhenFullReplaceOldest,
	)
	requester.Status.Phase = v1alpha1.ModelDeploymentPhaseWaitingForGPU
	requester.Status.AssignedNode = "gpu-node-1"
	requester.Status.Replacement = &v1alpha1.ReplacementStatus{
		Phase: v1alpha1.ReplacementPhaseDraining,
		Target: &v1alpha1.ReplacementReference{
			Namespace: "other-team",
			Name:      "target",
			UID:       "target-uid",
		},
	}
	target := lifecycleDeployment(
		v1alpha1.ActivationDesiredStateActive,
		v1alpha1.ActivationWhenFullQueue,
	)
	target.Namespace = "other-team"
	target.Name = "target"
	target.UID = "target-uid"
	target.Status.Phase = v1alpha1.ModelDeploymentPhaseActive
	target.Status.AssignedNode = "gpu-node-1"

	c, reconciler := lifecycleTestController(t, requester, target, lifecycleRuntime())
	handled, _, err := reconciler.reconcileReplacementRequester(
		context.Background(),
		requester,
		requester.DeepCopy(),
	)
	if err != nil {
		t.Fatalf("reconcileReplacementRequester() error = %v", err)
	}
	if !handled {
		t.Fatal("cross-namespace replacement was not handled as a terminal failure")
	}

	var updated v1alpha1.ModelDeployment
	getObject(t, c, client.ObjectKeyFromObject(requester), &updated)
	if updated.Status.Phase != v1alpha1.ModelDeploymentPhaseFailed {
		t.Fatalf("requester phase = %q, want Failed", updated.Status.Phase)
	}
	assertLifecycleCondition(
		t,
		updated.Status.Conditions,
		v1alpha1.ConditionReplacement,
		metav1.ConditionFalse,
		v1alpha1.ReasonReplacementNoCandidate,
	)
	getObject(t, c, client.ObjectKeyFromObject(target), &updated)
	if updated.Status.Replacement != nil {
		t.Fatalf("cross-namespace target was modified: %#v", updated.Status.Replacement)
	}
}

func TestRuntimeActivationFailureRequiresCurrentGeneration(t *testing.T) {
	t.Parallel()

	runtimeDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Generation: 2},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 1,
			Conditions: []appsv1.DeploymentCondition{{
				Type:    appsv1.DeploymentProgressing,
				Status:  corev1.ConditionFalse,
				Reason:  "ProgressDeadlineExceeded",
				Message: "stale rollout failure",
			}},
		},
	}
	if message, failed := runtimeActivationFailure(runtimeDeployment); failed {
		t.Fatalf("stale failure = (%q, %t), want ignored", message, failed)
	}
	runtimeDeployment.Status.ObservedGeneration = runtimeDeployment.Generation
	if message, failed := runtimeActivationFailure(runtimeDeployment); !failed ||
		message != "stale rollout failure" {
		t.Fatalf("current failure = (%q, %t), want detected", message, failed)
	}
}

func TestLifecycleRecoversGPUReservationFromManagedDeployment(t *testing.T) {
	t.Parallel()

	deployment := lifecycleDeployment(v1alpha1.ActivationDesiredStateActive, v1alpha1.ActivationWhenFullQueue)
	cache := lifecycleReadyCache(t, deployment, "gpu-node-1")
	replicas := int32(1)
	existingRuntime := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "orphan-runtime", Namespace: "default",
			Labels: map[string]string{
				resources.LabelManagedBy:       resources.ValueManagedBy,
				resources.LabelModelDeployment: "orphan",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Affinity: &corev1.Affinity{NodeAffinity: resources.NodeAffinityForCacheNode("gpu-node-1")},
				Containers: []corev1.Container{{
					Name: "runtime",
					Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
						"nvidia.com/gpu": resource.MustParse("1"),
					}},
				}},
			}},
		},
	}
	c, reconciler := lifecycleTestController(
		t, deployment, lifecycleRuntime(), cache, lifecycleGPUNode("gpu-node-1", 1), existingRuntime,
	)
	reconcileLifecycle(t, reconciler, deployment)

	var updated v1alpha1.ModelDeployment
	getObject(t, c, client.ObjectKeyFromObject(deployment), &updated)
	if updated.Status.Phase != v1alpha1.ModelDeploymentPhaseWaitingForGPU {
		t.Fatalf("phase = %q, want WaitingForGPU", updated.Status.Phase)
	}
}

func TestReadyCacheSpecsCompatible(t *testing.T) {
	t.Parallel()

	base := v1alpha1.ModelCacheSpec{
		ModelRepo: "org/model",
		Revision:  "main",
		SecretRef: "old-token",
		Storage: v1alpha1.ModelCacheStorage{
			Type: "nodeLocal", Size: "20Gi", Path: "/cache/model",
		},
	}
	tests := []struct {
		name       string
		mutate     func(*v1alpha1.ModelCacheSpec)
		compatible bool
	}{
		{name: "identical", compatible: true},
		{
			name: "credential rotation",
			mutate: func(spec *v1alpha1.ModelCacheSpec) {
				spec.SecretRef = "new-token"
			},
			compatible: true,
		},
		{
			name: "revision change",
			mutate: func(spec *v1alpha1.ModelCacheSpec) {
				spec.Revision = "v2"
			},
		},
		{
			name: "path change",
			mutate: func(spec *v1alpha1.ModelCacheSpec) {
				spec.Storage.Path = "/cache/other"
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			desired := *base.DeepCopy()
			if tt.mutate != nil {
				tt.mutate(&desired)
			}
			if got := readyCacheSpecsCompatible(base, desired); got != tt.compatible {
				t.Errorf("readyCacheSpecsCompatible() = %t, want %t", got, tt.compatible)
			}
		})
	}
}

func TestLifecycleDeletionRefreshesQueueDepthMetric(t *testing.T) {
	t.Parallel()

	deployment := lifecycleDeployment(v1alpha1.ActivationDesiredStateActive, v1alpha1.ActivationWhenFullQueue)
	deployment.Status.Phase = v1alpha1.ModelDeploymentPhaseWaitingForGPU
	metrics := &recordingControllerMetrics{queueDepth: 99}
	c, reconciler := lifecycleTestControllerWithMetrics(t, metrics, deployment, lifecycleRuntime())
	if err := c.Delete(context.Background(), deployment); err != nil {
		t.Fatalf("delete ModelDeployment: %v", err)
	}

	reconcileLifecycle(t, reconciler, deployment)
	if metrics.queueDepth != 0 {
		t.Errorf("activation queue depth = %v, want 0 after deletion", metrics.queueDepth)
	}
}

func lifecycleTestController(
	t *testing.T,
	objects ...client.Object,
) (client.Client, *ModelDeploymentController) {
	return lifecycleTestControllerWithMetrics(t, nil, objects...)
}

func lifecycleTestControllerWithMetrics(
	t *testing.T,
	metricsRecorder *recordingControllerMetrics,
	objects ...client.Object,
) (client.Client, *ModelDeploymentController) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.ModelDeployment{}, &v1alpha1.ModelCache{}, &appsv1.Deployment{}).
		WithObjects(objects...).
		Build()
	c := &patchCountingClient{Client: fakeClient}
	var recorderMetrics controllermetrics.Recorder
	if metricsRecorder != nil {
		recorderMetrics = metricsRecorder
	}
	reconciler, err := NewModelDeploymentController(
		c,
		scheme,
		ModelDeploymentControllerConfig{
			CacheRoot:        "/var/lib/inferops/models",
			DefaultCacheSize: "20Gi",
			DownloaderImage:  "ghcr.io/inferops/model-downloader:v0.1.0",
		},
		record.NewFakeRecorder(100),
		recorderMetrics,
	)
	if err != nil {
		t.Fatalf("NewModelDeploymentController() error = %v", err)
	}
	return c, reconciler
}

type recordingControllerMetrics struct {
	queueDepth float64
}

func (*recordingControllerMetrics) SetGPUSlots(string, float64, float64, float64) {}
func (m *recordingControllerMetrics) SetActivationQueueDepth(depth float64) {
	m.queueDepth = depth
}
func (*recordingControllerMetrics) ObserveActivationDuration(time.Duration)    {}
func (*recordingControllerMetrics) ObserveCacheDownloadDuration(time.Duration) {}
func (*recordingControllerMetrics) IncFailure(string, string)                  {}

type patchCountingClient struct {
	client.Client
	deploymentPatches            int
	deploymentPatchData          []string
	modelDeploymentStatusPatches int
}

func (c *patchCountingClient) Status() client.SubResourceWriter {
	return &countingStatusWriter{SubResourceWriter: c.Client.Status(), client: c}
}

type countingStatusWriter struct {
	client.SubResourceWriter
	client *patchCountingClient
}

func (w *countingStatusWriter) Patch(
	ctx context.Context,
	object client.Object,
	patch client.Patch,
	options ...client.SubResourcePatchOption,
) error {
	if _, ok := object.(*v1alpha1.ModelDeployment); ok {
		w.client.modelDeploymentStatusPatches++
	}
	return w.SubResourceWriter.Patch(ctx, object, patch, options...)
}

func (c *patchCountingClient) Patch(
	ctx context.Context,
	object client.Object,
	patch client.Patch,
	options ...client.PatchOption,
) error {
	if _, ok := object.(*appsv1.Deployment); ok {
		c.deploymentPatches++
		if data, err := patch.Data(object); err == nil {
			c.deploymentPatchData = append(c.deploymentPatchData, string(data))
		}
	}
	return c.Client.Patch(ctx, object, patch, options...)
}

func lifecycleDeployment(
	desired v1alpha1.ActivationDesiredState,
	whenFull v1alpha1.ActivationWhenFull,
) *v1alpha1.ModelDeployment {
	return &v1alpha1.ModelDeployment{
		TypeMeta: metav1.TypeMeta{APIVersion: v1alpha1.GroupVersion.String(), Kind: "ModelDeployment"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "qwen", Namespace: "default", UID: "qwen-uid", Generation: 1,
		},
		Spec: v1alpha1.ModelDeploymentSpec{
			Model: v1alpha1.ModelSpec{
				Repo: "Qwen/Qwen2.5", Revision: "main", Source: "huggingface",
			},
			Runtime: v1alpha1.RuntimeSpec{Ref: "nano-vllm"},
			Resources: v1alpha1.ResourceRequirements{
				CPU: "2", Memory: "8Gi",
				GPU: &v1alpha1.GPUResourceRequest{Count: 1, Vendor: "nvidia"},
			},
			Activation: v1alpha1.ActivationSpec{
				DesiredState: desired, WhenFull: whenFull, DrainTimeout: "5m",
			},
			Scaling: v1alpha1.ScalingSpec{MinReplicas: 0, MaxReplicas: 1},
			Routing: v1alpha1.RoutingSpec{
				Enabled: true, OpenAICompatible: true,
			},
			Cache: v1alpha1.CacheSpec{
				Enabled: true, Type: "nodeLocal", Size: "20Gi",
				Path: "/var/lib/inferops/models",
			},
			Secrets: v1alpha1.SecretReferences{HuggingFaceTokenSecretName: "hf-token"},
		},
	}
}

func lifecycleRuntime() *v1alpha1.ModelRuntime {
	return &v1alpha1.ModelRuntime{
		TypeMeta:   metav1.TypeMeta{APIVersion: v1alpha1.GroupVersion.String(), Kind: "ModelRuntime"},
		ObjectMeta: metav1.ObjectMeta{Name: "nano-vllm", Namespace: "default", UID: "runtime-uid"},
		Spec: v1alpha1.ModelRuntimeSpec{
			Engine: "nano-vllm", Protocol: "openai",
			DefaultImage: "ghcr.io/inferops/nano-vllm:v0.1.0",
			Port:         8000, HealthPath: "/health", ReadinessPath: "/health",
		},
	}
}

func lifecycleReadyCache(
	t *testing.T,
	deployment *v1alpha1.ModelDeployment,
	node string,
) *v1alpha1.ModelCache {
	t.Helper()
	name, err := cachecontract.Name(deployment)
	if err != nil {
		t.Fatal(err)
	}
	path, err := cachecontract.Path(deployment, "/var/lib/inferops/models")
	if err != nil {
		t.Fatal(err)
	}
	return &v1alpha1.ModelCache{
		TypeMeta: metav1.TypeMeta{APIVersion: v1alpha1.GroupVersion.String(), Kind: "ModelCache"},
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: deployment.Namespace, UID: types.UID(name + "-uid"),
			Labels: map[string]string{resources.LabelModelDeployment: deployment.Name},
		},
		Spec: v1alpha1.ModelCacheSpec{
			ModelRepo: deployment.Spec.Model.Repo,
			Revision:  deployment.Spec.Model.Revision,
			SecretRef: deployment.Spec.Secrets.HuggingFaceTokenSecretName,
			Storage: v1alpha1.ModelCacheStorage{
				Type:         "nodeLocal",
				Size:         "20Gi",
				Path:         path,
				NodeSelector: copyStringMap(deployment.Spec.Scheduling.NodeSelector),
				Tolerations:  copyTolerations(deployment.Spec.Scheduling.Tolerations),
			},
		},
		Status: v1alpha1.ModelCacheStatus{
			ObservedGeneration: 1,
			Phase:              v1alpha1.ModelCachePhaseReady,
			Revision:           "main", NodeName: node, NodeUID: node + "-uid",
			Path: path, Size: "20Gi", ReservedSize: "20Gi",
			Conditions: []v1alpha1.Condition{{
				Type: v1alpha1.CacheConditionReady, Status: metav1.ConditionTrue,
				Reason: v1alpha1.CacheReasonCacheReady,
			}},
		},
	}
}

func lifecycleGPUNode(name string, count int64) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(name + "-uid")},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				"nvidia.com/gpu":      *resource.NewQuantity(count, resource.DecimalSI),
				corev1.ResourceCPU:    resource.MustParse("32"),
				corev1.ResourceMemory: resource.MustParse("128Gi"),
			},
			Conditions: []corev1.NodeCondition{{
				Type: corev1.NodeReady, Status: corev1.ConditionTrue,
			}},
		},
	}
}

func lifecycleReplacementFixture(
	t *testing.T,
	policy v1alpha1.ActivationWhenFull,
) (client.Client, *ModelDeploymentController, *v1alpha1.ModelDeployment, *v1alpha1.ModelDeployment) {
	t.Helper()
	victim := lifecycleDeployment(
		v1alpha1.ActivationDesiredStateActive,
		v1alpha1.ActivationWhenFullQueue,
	)
	victim.Name = "victim"
	victim.UID = "victim-uid"
	victim.CreationTimestamp = metav1.NewTime(time.Now().Add(-time.Hour))
	victim.Spec.Model.Repo = "acme/victim"
	victim.Spec.Activation.DrainTimeout = "1s"
	victimCache := lifecycleReadyCache(t, victim, "gpu-node-1")

	incoming := lifecycleDeployment(v1alpha1.ActivationDesiredStateActive, policy)
	incoming.Spec.Model.Repo = "acme/incoming"
	incoming.Spec.Activation.Priority = 100
	incomingCache := lifecycleReadyCache(t, incoming, "gpu-node-1")

	c, reconciler := lifecycleTestController(
		t,
		victim,
		incoming,
		lifecycleRuntime(),
		victimCache,
		incomingCache,
		lifecycleGPUNode("gpu-node-1", 1),
	)
	reconcileLifecycle(t, reconciler, victim)
	markLifecycleRuntimeReady(t, c, victim)
	reconcileLifecycle(t, reconciler, victim)
	return c, reconciler, incoming, victim
}

func startAndDrainReplacement(
	t *testing.T,
	c client.Client,
	reconciler *ModelDeploymentController,
	incoming, victim *v1alpha1.ModelDeployment,
) {
	t.Helper()
	reconcileLifecycle(t, reconciler, incoming)
	var updated v1alpha1.ModelDeployment
	getObject(t, c, client.ObjectKeyFromObject(incoming), &updated)
	if updated.Status.Replacement == nil ||
		updated.Status.Replacement.Phase != v1alpha1.ReplacementPhaseDraining {
		t.Fatalf("replacement status = %#v, want Draining", updated.Status.Replacement)
	}
	if updated.Status.AssignedNode != "gpu-node-1" {
		t.Fatalf("handoff reservation node = %q, want gpu-node-1", updated.Status.AssignedNode)
	}
	assertLifecycleCondition(
		t,
		updated.Status.Conditions,
		v1alpha1.ConditionGPUAssigned,
		metav1.ConditionUnknown,
		v1alpha1.ReasonReplacementDraining,
	)
	observer := lifecycleDeployment(
		v1alpha1.ActivationDesiredStateActive,
		v1alpha1.ActivationWhenFullQueue,
	)
	observer.Name = "observer"
	allocations, err := reconciler.gpuAllocations(context.Background(), observer)
	if err != nil {
		t.Fatalf("gpuAllocations() error = %v", err)
	}
	if len(allocations) != 1 || allocations[0].Count != 1 ||
		allocations[0].NodeName != "gpu-node-1" {
		t.Fatalf("handoff allocations = %#v, want one reserved GPU on gpu-node-1", allocations)
	}

	reconcileLifecycle(t, reconciler, victim)
	getObject(t, c, client.ObjectKeyFromObject(victim), &updated)
	if updated.Status.Phase != v1alpha1.ModelDeploymentPhaseDraining {
		t.Fatalf("victim phase = %q, want Draining", updated.Status.Phase)
	}
	var victimService corev1.Service
	getObject(
		t,
		c,
		types.NamespacedName{Namespace: victim.Namespace, Name: templates.RuntimeServiceName(victim.Name)},
		&victimService,
	)
	if len(victimService.Spec.Selector) != 0 {
		t.Errorf("replacement target Service selector = %#v, want no selected endpoints", victimService.Spec.Selector)
	}
	expired := metav1.NewTime(time.Now().Add(-2 * time.Second))
	updated.Status.DrainStartedAt = &expired
	if err := c.Status().Update(context.Background(), &updated); err != nil {
		t.Fatalf("expire victim drain: %v", err)
	}
	reconcileLifecycle(t, reconciler, victim)
	reconcileLifecycle(t, reconciler, victim)
	getObject(t, c, client.ObjectKeyFromObject(victim), &updated)
	if updated.Status.Phase != v1alpha1.ModelDeploymentPhaseCached {
		t.Fatalf("victim phase = %q, want Cached", updated.Status.Phase)
	}
}

func markLifecycleRuntimeReady(
	t *testing.T,
	c client.Client,
	deployment *v1alpha1.ModelDeployment,
) {
	t.Helper()
	var runtimeDeployment appsv1.Deployment
	getObject(t, c, types.NamespacedName{
		Namespace: deployment.Namespace,
		Name:      templates.RuntimeServiceName(deployment.Name),
	}, &runtimeDeployment)
	runtimeDeployment.Status.ObservedGeneration = runtimeDeployment.Generation
	runtimeDeployment.Status.ReadyReplicas = 1
	runtimeDeployment.Status.AvailableReplicas = 1
	runtimeDeployment.Status.Conditions = []appsv1.DeploymentCondition{{
		Type:   appsv1.DeploymentProgressing,
		Status: corev1.ConditionTrue,
		Reason: "NewReplicaSetAvailable",
	}}
	if err := c.Status().Update(context.Background(), &runtimeDeployment); err != nil {
		t.Fatalf("mark runtime ready: %v", err)
	}
}

func markLifecycleRuntimeFailed(
	t *testing.T,
	c client.Client,
	deployment *v1alpha1.ModelDeployment,
	message string,
) {
	t.Helper()
	var runtimeDeployment appsv1.Deployment
	getObject(t, c, types.NamespacedName{
		Namespace: deployment.Namespace,
		Name:      templates.RuntimeServiceName(deployment.Name),
	}, &runtimeDeployment)
	runtimeDeployment.Status.ObservedGeneration = runtimeDeployment.Generation
	runtimeDeployment.Status.Conditions = []appsv1.DeploymentCondition{{
		Type:    appsv1.DeploymentProgressing,
		Status:  corev1.ConditionFalse,
		Reason:  "ProgressDeadlineExceeded",
		Message: message,
	}}
	if err := c.Status().Update(context.Background(), &runtimeDeployment); err != nil {
		t.Fatalf("mark runtime failed: %v", err)
	}
}

func assertRecordedEventReasons(
	t *testing.T,
	reconciler *ModelDeploymentController,
	reasons ...string,
) {
	t.Helper()
	recorder, ok := reconciler.eventRecorder.(*record.FakeRecorder)
	if !ok {
		t.Fatalf("event recorder = %T, want *record.FakeRecorder", reconciler.eventRecorder)
	}
	events := make([]string, 0)
	for {
		select {
		case event := <-recorder.Events:
			events = append(events, event)
		default:
			for _, reason := range reasons {
				found := false
				for _, event := range events {
					if strings.Contains(event, " "+reason+" ") {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("event reason %q not found in %#v", reason, events)
				}
			}
			return
		}
	}
}

func reconcileLifecycle(t *testing.T, reconciler *ModelDeploymentController, deployment *v1alpha1.ModelDeployment) {
	t.Helper()
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKeyFromObject(deployment),
	}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
}

func getLifecycleCache(
	t *testing.T,
	c client.Client,
	deployment *v1alpha1.ModelDeployment,
) *v1alpha1.ModelCache {
	t.Helper()
	name, err := cachecontract.Name(deployment)
	if err != nil {
		t.Fatal(err)
	}
	var cache v1alpha1.ModelCache
	getObject(t, c, types.NamespacedName{Namespace: deployment.Namespace, Name: name}, &cache)
	return &cache
}

func markLifecycleCacheReady(
	t *testing.T,
	c client.Client,
	cache *v1alpha1.ModelCache,
	node string,
) {
	t.Helper()
	cache.Status.Phase = v1alpha1.ModelCachePhaseReady
	cache.Status.Revision = "main"
	cache.Status.NodeName = node
	cache.Status.NodeUID = node + "-uid"
	cache.Status.Path = cache.Spec.Storage.Path
	cache.Status.Conditions = []v1alpha1.Condition{{
		Type: v1alpha1.CacheConditionReady, Status: metav1.ConditionTrue,
		Reason: v1alpha1.CacheReasonCacheReady,
	}}
	if err := c.Status().Update(context.Background(), cache); err != nil {
		t.Fatalf("mark cache ready: %v", err)
	}
}

func assertLifecycleCondition(
	t *testing.T,
	conditions []v1alpha1.Condition,
	conditionType string,
	wantStatus metav1.ConditionStatus,
	wantReason string,
) {
	t.Helper()
	condition, found := status.FindCondition(conditions, conditionType)
	if !found {
		t.Fatalf("condition %q not found", conditionType)
	}
	if condition.Status != wantStatus || condition.Reason != wantReason {
		t.Errorf("condition %q = (%s, %s), want (%s, %s)",
			conditionType, condition.Status, condition.Reason, wantStatus, wantReason)
	}
}

func assertObjectExists(t *testing.T, c client.Client, key client.ObjectKey, object client.Object) {
	t.Helper()
	if err := c.Get(context.Background(), key, object); err != nil {
		t.Fatalf("get %T %s: %v", object, key, err)
	}
}

func assertObjectMissing(t *testing.T, c client.Client, key client.ObjectKey, object client.Object) {
	t.Helper()
	if err := c.Get(context.Background(), key, object); !apierrors.IsNotFound(err) {
		t.Fatalf("get %T %s error = %v, want NotFound", object, key, err)
	}
}

func getObject(t *testing.T, c client.Client, key client.ObjectKey, object client.Object) {
	t.Helper()
	if err := c.Get(context.Background(), key, object); err != nil {
		t.Fatalf("get %T %s: %v", object, key, err)
	}
}
