package controllers

import (
	"context"
	"testing"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	"github.com/brassinai/inferops/operator/internal/resources"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	testCacheRoot       = "/var/lib/inferops/models"
	testDownloaderImage = "ghcr.io/inferops/model-downloader:v0.0.0"
)

func testScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = batchv1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)
	return scheme
}

func testReconciler(t *testing.T, c client.Client) *ModelCacheReconciler {
	t.Helper()
	r, err := NewModelCacheReconciler(
		c,
		ModelCacheReconcilerConfig{
			CacheRoot:               testCacheRoot,
			DownloaderImage:         testDownloaderImage,
			CacheCapacityAnnotation: "inferops.dev/cache-capacity",
		},
		&record.FakeRecorder{},
	)
	if err != nil {
		t.Fatalf("NewModelCacheReconciler() error = %v", err)
	}
	return r
}

func testCache(name string) *v1alpha1.ModelCache {
	return &v1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  "default",
			UID:        types.UID(name + "-uid"),
			Generation: 1,
		},
		Spec: v1alpha1.ModelCacheSpec{
			ModelRepo: "Qwen/Qwen2.5-7B-Instruct",
			Revision:  "main",
			Storage: v1alpha1.ModelCacheStorage{
				Type: "nodeLocal",
				Size: "100Gi",
				Path: testCacheRoot + "/" + name,
			},
		},
	}
}

func readyNode(name string, capacity string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			UID:         types.UID(name + "-uid"),
			Annotations: map[string]string{"inferops.dev/cache-capacity": capacity},
		},
		Spec: corev1.NodeSpec{Unschedulable: false},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

func TestModelCacheReconcilerCreatesJob(t *testing.T) {
	t.Parallel()

	cache := testCache("qwen-chat")
	node := readyNode("gpu-node-1", "500Gi")
	c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(cache, node).WithStatusSubresource(cache).Build()

	r := testReconciler(t, c)
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: cache.Name, Namespace: cache.Namespace}}
	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	var reserved v1alpha1.ModelCache
	if err := c.Get(context.Background(), req.NamespacedName, &reserved); err != nil {
		t.Fatalf("Get reserved cache error = %v", err)
	}
	if reserved.Status.Phase != v1alpha1.ModelCachePhasePending || reserved.Status.NodeUID == "" {
		t.Fatalf("reservation status = %#v, want Pending with node UID", reserved.Status)
	}
	var absentJob batchv1.Job
	if err := c.Get(context.Background(), types.NamespacedName{Name: cache.Name + "-download", Namespace: cache.Namespace}, &absentJob); !apierrors.IsNotFound(err) {
		t.Fatalf("Job exists before placement reservation is persisted: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}

	var updated v1alpha1.ModelCache
	if err := c.Get(context.Background(), types.NamespacedName{Name: cache.Name, Namespace: cache.Namespace}, &updated); err != nil {
		t.Fatalf("Get cache error = %v", err)
	}
	if updated.Status.Phase != v1alpha1.ModelCachePhaseDownloading {
		t.Errorf("phase = %q, want Downloading", updated.Status.Phase)
	}

	var job batchv1.Job
	jobName := cache.Name + "-download"
	if err := c.Get(context.Background(), types.NamespacedName{Name: jobName, Namespace: cache.Namespace}, &job); err != nil {
		t.Fatalf("Get Job error = %v", err)
	}
	if job.Spec.Template.Spec.NodeSelector["kubernetes.io/hostname"] != "gpu-node-1" {
		t.Errorf("job node = %q, want gpu-node-1", job.Spec.Template.Spec.NodeSelector["kubernetes.io/hostname"])
	}
}

func TestModelCacheReconcilerReachesReady(t *testing.T) {
	t.Parallel()

	cache := testCache("qwen-chat")
	node := readyNode("gpu-node-1", "500Gi")
	c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(cache, node).WithStatusSubresource(cache).Build()

	r := testReconciler(t, c)

	// First reconcile records placement; second creates the Job.
	_, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: cache.Name, Namespace: cache.Namespace}})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: cache.Name, Namespace: cache.Namespace}}); err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}

	// Mark the Job complete.
	var job batchv1.Job
	jobName := cache.Name + "-download"
	if err := c.Get(context.Background(), types.NamespacedName{Name: jobName, Namespace: cache.Namespace}, &job); err != nil {
		t.Fatalf("Get Job error = %v", err)
	}
	job.Status.Conditions = []batchv1.JobCondition{
		{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
	}
	if err := c.Status().Update(context.Background(), &job); err != nil {
		t.Fatalf("Update Job status error = %v", err)
	}

	// Second reconcile observes completion.
	_, err = r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: cache.Name, Namespace: cache.Namespace}})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	var updated v1alpha1.ModelCache
	if err := c.Get(context.Background(), types.NamespacedName{Name: cache.Name, Namespace: cache.Namespace}, &updated); err != nil {
		t.Fatalf("Get cache error = %v", err)
	}
	if updated.Status.Phase != v1alpha1.ModelCachePhaseReady {
		t.Errorf("phase = %q, want Ready", updated.Status.Phase)
	}
	if updated.Status.NodeName != "gpu-node-1" {
		t.Errorf("node = %q, want gpu-node-1", updated.Status.NodeName)
	}
}

func TestModelCacheReconcilerIdempotent(t *testing.T) {
	t.Parallel()

	cache := testCache("qwen-chat")
	node := readyNode("gpu-node-1", "500Gi")
	c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(cache, node).WithStatusSubresource(cache).Build()

	r := testReconciler(t, c)
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: cache.Name, Namespace: cache.Namespace}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first Reconcile() error = %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}

	var firstStatus v1alpha1.ModelCache
	if err := c.Get(context.Background(), req.NamespacedName, &firstStatus); err != nil {
		t.Fatalf("Get cache error = %v", err)
	}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("third Reconcile() error = %v", err)
	}

	var secondStatus v1alpha1.ModelCache
	if err := c.Get(context.Background(), req.NamespacedName, &secondStatus); err != nil {
		t.Fatalf("Get cache error = %v", err)
	}

	// ResourceVersion should not change between the second reconcile and a
	// third one with no changes.
	if firstStatus.ResourceVersion != secondStatus.ResourceVersion {
		t.Fatalf("reconciliation was not idempotent: resource version changed from %s to %s", firstStatus.ResourceVersion, secondStatus.ResourceVersion)
	}
}

func TestModelCacheReconcilerFailsUnsupportedStorage(t *testing.T) {
	t.Parallel()

	cache := testCache("qwen-chat")
	cache.Spec.Storage.Type = "pvc"
	node := readyNode("gpu-node-1", "500Gi")
	c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(cache, node).WithStatusSubresource(cache).Build()

	r := testReconciler(t, c)
	_, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: cache.Name, Namespace: cache.Namespace}})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	var updated v1alpha1.ModelCache
	if err := c.Get(context.Background(), types.NamespacedName{Name: cache.Name, Namespace: cache.Namespace}, &updated); err != nil {
		t.Fatalf("Get cache error = %v", err)
	}
	if updated.Status.Phase != v1alpha1.ModelCachePhaseFailed {
		t.Errorf("phase = %q, want Failed", updated.Status.Phase)
	}
}

func TestModelCacheReconcilerHandlesNodeLoss(t *testing.T) {
	t.Parallel()

	cache := testCache("qwen-chat")
	cache.Status.Phase = v1alpha1.ModelCachePhaseReady
	cache.Status.NodeName = "gpu-node-1"
	cache.Status.Path = cache.Spec.Storage.Path
	cache.Status.ReservedSize = "100Gi"

	node := readyNode("gpu-node-2", "500Gi")
	c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(cache, node).WithStatusSubresource(cache).Build()

	r := testReconciler(t, c)
	_, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: cache.Name, Namespace: cache.Namespace}})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	var updated v1alpha1.ModelCache
	if err := c.Get(context.Background(), types.NamespacedName{Name: cache.Name, Namespace: cache.Namespace}, &updated); err != nil {
		t.Fatalf("Get cache error = %v", err)
	}
	if updated.Status.Phase != v1alpha1.ModelCachePhasePending {
		t.Errorf("phase = %q, want Pending after node loss", updated.Status.Phase)
	}

	// A later reconcile must not silently create a replacement copy on the
	// other node. Multi-node distribution belongs to MVP-503.
	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: cache.Name, Namespace: cache.Namespace}}); err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}
	var jobs batchv1.JobList
	if err := c.List(context.Background(), &jobs); err != nil {
		t.Fatalf("List Jobs error = %v", err)
	}
	if len(jobs.Items) != 0 {
		t.Fatalf("node loss created %d replacement Jobs, want none", len(jobs.Items))
	}
}

func TestCacheInputHashForcesJobReplacement(t *testing.T) {
	t.Parallel()

	cache := testCache("qwen-chat")
	node := readyNode("gpu-node-1", "500Gi")
	c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(cache, node).WithStatusSubresource(cache).Build()

	r := testReconciler(t, c)
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: cache.Name, Namespace: cache.Namespace}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first Reconcile() error = %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}

	var job batchv1.Job
	jobName := cache.Name + "-download"
	if err := c.Get(context.Background(), types.NamespacedName{Name: jobName, Namespace: cache.Namespace}, &job); err != nil {
		t.Fatalf("Get Job error = %v", err)
	}
	oldHash := resources.CacheInputHashFromJob(&job)

	// Change the cache revision, which must change the input hash.
	var latest v1alpha1.ModelCache
	if err := c.Get(context.Background(), req.NamespacedName, &latest); err != nil {
		t.Fatalf("Get cache error = %v", err)
	}
	latest.Spec.Revision = "v2"
	latest.Generation = 2
	if err := c.Update(context.Background(), &latest); err != nil {
		t.Fatalf("Update cache error = %v", err)
	}

	result, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("replacement Reconcile() error = %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Fatal("replacement Reconcile() did not request a follow-up")
	}

	// The fake client deletes synchronously, so the Job should be gone.
	if err := c.Get(context.Background(), types.NamespacedName{Name: jobName, Namespace: cache.Namespace}, &job); !apierrors.IsNotFound(err) {
		t.Fatalf("expected Job to be deleted, got err = %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("replacement reset Reconcile() error = %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("replacement creation Reconcile() error = %v", err)
	}
	if err := c.Get(context.Background(), types.NamespacedName{Name: jobName, Namespace: cache.Namespace}, &job); err != nil {
		t.Fatalf("Get replacement Job error = %v", err)
	}
	if newHash := resources.CacheInputHashFromJob(&job); newHash == oldHash {
		t.Fatalf("replacement input hash = %q, want different from %q", newHash, oldHash)
	}
}

func TestModelCacheReconcilerTreatsJobFailedConditionAsTerminal(t *testing.T) {
	t.Parallel()

	cache := testCache("qwen-chat")
	node := readyNode("gpu-node-1", "500Gi")
	c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(cache, node).WithStatusSubresource(cache).Build()
	r := testReconciler(t, c)
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: cache.Name, Namespace: cache.Namespace}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("initial Reconcile() error = %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("job creation Reconcile() error = %v", err)
	}

	var job batchv1.Job
	if err := c.Get(context.Background(), types.NamespacedName{Name: cache.Name + "-download", Namespace: cache.Namespace}, &job); err != nil {
		t.Fatalf("Get Job error = %v", err)
	}
	job.Status.Failed = 1
	job.Status.Conditions = []batchv1.JobCondition{{
		Type:    batchv1.JobFailed,
		Status:  corev1.ConditionTrue,
		Reason:  "BackoffLimitExceeded",
		Message: "download retries exhausted",
	}}
	if err := c.Status().Update(context.Background(), &job); err != nil {
		t.Fatalf("Update Job status error = %v", err)
	}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("terminal failure Reconcile() error = %v", err)
	}
	var updated v1alpha1.ModelCache
	if err := c.Get(context.Background(), req.NamespacedName, &updated); err != nil {
		t.Fatalf("Get cache error = %v", err)
	}
	if updated.Status.Phase != v1alpha1.ModelCachePhaseFailed {
		t.Fatalf("phase = %q, want Failed", updated.Status.Phase)
	}
}

func TestModelCacheRetryTokenReplacesJobOnce(t *testing.T) {
	t.Parallel()

	cache := testCache("qwen-chat")
	node := readyNode("gpu-node-1", "500Gi")
	c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(cache, node).WithStatusSubresource(cache).Build()
	r := testReconciler(t, c)
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: cache.Name, Namespace: cache.Namespace}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("initial Reconcile() error = %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("job creation Reconcile() error = %v", err)
	}

	var latest v1alpha1.ModelCache
	if err := c.Get(context.Background(), req.NamespacedName, &latest); err != nil {
		t.Fatalf("Get cache error = %v", err)
	}
	if latest.Annotations == nil {
		latest.Annotations = map[string]string{}
	}
	latest.Annotations[RetryAnnotation] = "attempt-2"
	if err := c.Update(context.Background(), &latest); err != nil {
		t.Fatalf("Update retry token error = %v", err)
	}

	result, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("retry replacement Reconcile() error = %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Fatal("retry replacement did not request a follow-up")
	}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("retry creation Reconcile() error = %v", err)
	}
	var replacement batchv1.Job
	jobKey := types.NamespacedName{Name: cache.Name + "-download", Namespace: cache.Namespace}
	if err := c.Get(context.Background(), jobKey, &replacement); err != nil {
		t.Fatalf("Get replacement Job error = %v", err)
	}
	if got := replacement.Annotations[resources.CacheRetryTokenAnnotation]; got != "attempt-2" {
		t.Fatalf("replacement retry token = %q, want attempt-2", got)
	}
	replacementResourceVersion := replacement.ResourceVersion

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("idempotent retry Reconcile() error = %v", err)
	}
	if err := c.Get(context.Background(), jobKey, &replacement); err != nil {
		t.Fatalf("Get stable replacement Job error = %v", err)
	}
	if replacement.ResourceVersion != replacementResourceVersion {
		t.Fatalf("retry token caused repeated Job replacement: resource version changed from %s to %s", replacementResourceVersion, replacement.ResourceVersion)
	}
}

func TestReadyCacheSurvivesJobGarbageCollectionAndNodeCordon(t *testing.T) {
	t.Parallel()

	cache := testCache("qwen-chat")
	node := readyNode("gpu-node-1", "500Gi")
	c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(cache, node).WithStatusSubresource(cache).Build()
	r := testReconciler(t, c)
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: cache.Name, Namespace: cache.Namespace}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("initial Reconcile() error = %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("job creation Reconcile() error = %v", err)
	}
	var job batchv1.Job
	jobKey := types.NamespacedName{Name: cache.Name + "-download", Namespace: cache.Namespace}
	if err := c.Get(context.Background(), jobKey, &job); err != nil {
		t.Fatalf("Get Job error = %v", err)
	}
	job.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}
	if err := c.Status().Update(context.Background(), &job); err != nil {
		t.Fatalf("complete Job error = %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("ready Reconcile() error = %v", err)
	}
	if err := c.Delete(context.Background(), &job); err != nil {
		t.Fatalf("delete completed Job error = %v", err)
	}

	var latestNode corev1.Node
	if err := c.Get(context.Background(), types.NamespacedName{Name: node.Name}, &latestNode); err != nil {
		t.Fatalf("Get node error = %v", err)
	}
	latestNode.Spec.Unschedulable = true
	latestNode.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}}
	if err := c.Update(context.Background(), &latestNode); err != nil {
		t.Fatalf("cordon node error = %v", err)
	}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("post-GC Reconcile() error = %v", err)
	}
	var updated v1alpha1.ModelCache
	if err := c.Get(context.Background(), req.NamespacedName, &updated); err != nil {
		t.Fatalf("Get cache error = %v", err)
	}
	if updated.Status.Phase != v1alpha1.ModelCachePhaseReady {
		t.Fatalf("phase = %q, want Ready", updated.Status.Phase)
	}
}

func TestInFlightDownloadIsNotDuplicatedWhenNodeBecomesUnready(t *testing.T) {
	t.Parallel()

	cache := testCache("qwen-chat")
	node := readyNode("gpu-node-1", "500Gi")
	otherNode := readyNode("gpu-node-2", "500Gi")
	c := fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(cache, node, otherNode).
		WithStatusSubresource(cache).
		Build()
	r := testReconciler(t, c)
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: cache.Name, Namespace: cache.Namespace}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("placement Reconcile() error = %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("job creation Reconcile() error = %v", err)
	}
	var originalJob batchv1.Job
	jobKey := types.NamespacedName{Name: cache.Name + "-download", Namespace: cache.Namespace}
	if err := c.Get(context.Background(), jobKey, &originalJob); err != nil {
		t.Fatalf("Get Job error = %v", err)
	}

	var latestNode corev1.Node
	if err := c.Get(context.Background(), types.NamespacedName{Name: node.Name}, &latestNode); err != nil {
		t.Fatalf("Get node error = %v", err)
	}
	latestNode.Spec.Unschedulable = true
	latestNode.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}}
	if err := c.Update(context.Background(), &latestNode); err != nil {
		t.Fatalf("update node error = %v", err)
	}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("in-flight Reconcile() error = %v", err)
	}
	var stableJob batchv1.Job
	if err := c.Get(context.Background(), jobKey, &stableJob); err != nil {
		t.Fatalf("Get stable Job error = %v", err)
	}
	if stableJob.ResourceVersion != originalJob.ResourceVersion {
		t.Fatalf("in-flight Job changed from resource version %s to %s", originalJob.ResourceVersion, stableJob.ResourceVersion)
	}
	if got := stableJob.Spec.Template.Spec.NodeSelector[corev1.LabelHostname]; got != node.Name {
		t.Fatalf("in-flight Job moved to %q, want %q", got, node.Name)
	}
}

func TestPendingCacheUsesBoundedRequeue(t *testing.T) {
	t.Parallel()

	cache := testCache("qwen-chat")
	c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(cache).WithStatusSubresource(cache).Build()
	r := testReconciler(t, c)
	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: cache.Name, Namespace: cache.Namespace},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if result.RequeueAfter != pendingRequeueAfter {
		t.Fatalf("requeueAfter = %s, want %s", result.RequeueAfter, pendingRequeueAfter)
	}
}

func TestMissingSecretWaitsWithoutCreatingJob(t *testing.T) {
	t.Parallel()

	cache := testCache("qwen-chat")
	cache.Spec.SecretRef = "hf-token"
	node := readyNode("gpu-node-1", "500Gi")
	c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(cache, node).WithStatusSubresource(cache).Build()
	r := testReconciler(t, c)
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: cache.Name, Namespace: cache.Namespace}}

	result, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if result.RequeueAfter != pendingRequeueAfter {
		t.Fatalf("requeueAfter = %s, want %s", result.RequeueAfter, pendingRequeueAfter)
	}
	var updated v1alpha1.ModelCache
	if err := c.Get(context.Background(), req.NamespacedName, &updated); err != nil {
		t.Fatalf("Get cache error = %v", err)
	}
	ready, found := conditionByType(updated.Status.Conditions, v1alpha1.CacheConditionReady)
	if !found || ready.Reason != v1alpha1.CacheReasonSecretNotFound {
		t.Fatalf("Ready condition = %#v, want reason %s", ready, v1alpha1.CacheReasonSecretNotFound)
	}
	var jobs batchv1.JobList
	if err := c.List(context.Background(), &jobs); err != nil {
		t.Fatalf("List Jobs error = %v", err)
	}
	if len(jobs.Items) != 0 {
		t.Fatalf("missing Secret created %d Jobs, want none", len(jobs.Items))
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "hf-token", Namespace: cache.Namespace},
		Data:       map[string][]byte{"token": []byte("test-token")},
	}
	if err := c.Create(context.Background(), secret); err != nil {
		t.Fatalf("Create Secret error = %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("recovery Reconcile() error = %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("recovery Job Reconcile() error = %v", err)
	}
	if err := c.List(context.Background(), &jobs); err != nil {
		t.Fatalf("List recovery Jobs error = %v", err)
	}
	if len(jobs.Items) != 1 {
		t.Fatalf("Secret recovery created %d Jobs, want one", len(jobs.Items))
	}
}

func TestReadyCacheIdentityCannotBeChangedInPlace(t *testing.T) {
	t.Parallel()

	cache := testCache("qwen-chat")
	node := readyNode("gpu-node-1", "500Gi")
	c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(cache, node).WithStatusSubresource(cache).Build()
	r := testReconciler(t, c)
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: cache.Name, Namespace: cache.Namespace}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("initial Reconcile() error = %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("job creation Reconcile() error = %v", err)
	}
	var job batchv1.Job
	jobKey := types.NamespacedName{Name: cache.Name + "-download", Namespace: cache.Namespace}
	if err := c.Get(context.Background(), jobKey, &job); err != nil {
		t.Fatalf("Get Job error = %v", err)
	}
	job.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}
	if err := c.Status().Update(context.Background(), &job); err != nil {
		t.Fatalf("complete Job error = %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("ready Reconcile() error = %v", err)
	}

	var latest v1alpha1.ModelCache
	if err := c.Get(context.Background(), req.NamespacedName, &latest); err != nil {
		t.Fatalf("Get cache error = %v", err)
	}
	latest.Spec.Revision = "v2"
	latest.Generation = 2
	if err := c.Update(context.Background(), &latest); err != nil {
		t.Fatalf("Update cache error = %v", err)
	}

	for i := 0; i < 2; i++ {
		if _, err := r.Reconcile(context.Background(), req); err != nil {
			t.Fatalf("identity-change Reconcile() %d error = %v", i+1, err)
		}
	}
	if err := c.Get(context.Background(), req.NamespacedName, &latest); err != nil {
		t.Fatalf("Get failed cache error = %v", err)
	}
	if latest.Status.Phase != v1alpha1.ModelCachePhaseFailed {
		t.Fatalf("phase = %q, want Failed", latest.Status.Phase)
	}
	ready, found := conditionByType(latest.Status.Conditions, v1alpha1.CacheConditionReady)
	if !found || ready.Reason != v1alpha1.CacheReasonIdentityChanged {
		t.Fatalf("Ready condition = %#v, want reason %s", ready, v1alpha1.CacheReasonIdentityChanged)
	}
	if err := c.Get(context.Background(), jobKey, &job); err != nil {
		t.Fatalf("original completed Job was replaced: %v", err)
	}
}

func TestReadyCacheRejectsRecreatedNodeWithSameName(t *testing.T) {
	t.Parallel()

	cache := testCache("qwen-chat")
	cache.Status = v1alpha1.ModelCacheStatus{
		ObservedGeneration: 1,
		Phase:              v1alpha1.ModelCachePhaseReady,
		NodeName:           "gpu-node-1",
		NodeUID:            "old-node-uid",
		Path:               cache.Spec.Storage.Path,
		ReservedSize:       "100Gi",
		Conditions: []v1alpha1.Condition{{
			Type:               v1alpha1.CacheConditionDownloaded,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: 1,
			Reason:             v1alpha1.CacheReasonDownloadSucceeded,
		}},
	}
	node := readyNode("gpu-node-1", "500Gi")
	node.UID = "new-node-uid"
	c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(cache, node).WithStatusSubresource(cache).Build()
	r := testReconciler(t, c)
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: cache.Name, Namespace: cache.Namespace}}

	for i := 0; i < 2; i++ {
		if _, err := r.Reconcile(context.Background(), req); err != nil {
			t.Fatalf("Reconcile() %d error = %v", i+1, err)
		}
	}
	var updated v1alpha1.ModelCache
	if err := c.Get(context.Background(), req.NamespacedName, &updated); err != nil {
		t.Fatalf("Get cache error = %v", err)
	}
	if updated.Status.Phase != v1alpha1.ModelCachePhasePending {
		t.Fatalf("phase = %q, want Pending", updated.Status.Phase)
	}
	var jobs batchv1.JobList
	if err := c.List(context.Background(), &jobs); err != nil {
		t.Fatalf("List Jobs error = %v", err)
	}
	if len(jobs.Items) != 0 {
		t.Fatalf("recreated node received %d downloader Jobs, want none", len(jobs.Items))
	}
}

func TestNodeChangeEnqueuesWaitingAndAssignedCaches(t *testing.T) {
	t.Parallel()

	waiting := testCache("waiting")
	waiting.Status.Phase = v1alpha1.ModelCachePhasePending
	assigned := testCache("assigned")
	assigned.Status.Phase = v1alpha1.ModelCachePhaseReady
	assigned.Status.NodeName = "gpu-node-1"
	unrelated := testCache("unrelated")
	unrelated.Status.Phase = v1alpha1.ModelCachePhaseReady
	unrelated.Status.NodeName = "gpu-node-2"

	c := fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(waiting, assigned, unrelated).
		WithStatusSubresource(waiting, assigned, unrelated).
		Build()
	r := testReconciler(t, c)
	requests := r.requestsForNodeChange(context.Background(), readyNode("gpu-node-1", "500Gi"))

	got := make(map[string]bool, len(requests))
	for _, request := range requests {
		got[request.Name] = true
	}
	if !got["waiting"] || !got["assigned"] || got["unrelated"] {
		t.Fatalf("requests = %#v, want waiting and assigned only", requests)
	}
}

func conditionByType(conditions []v1alpha1.Condition, conditionType string) (v1alpha1.Condition, bool) {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return conditions[i], true
		}
	}
	return v1alpha1.Condition{}, false
}
