package controllers

import (
	"context"
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
