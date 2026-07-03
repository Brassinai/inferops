package scheduler

import (
	"errors"
	"testing"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const testCacheRoot = "/var/lib/inferops/models"

func testPlanner(t *testing.T) *CachePlanner {
	t.Helper()
	planner, err := NewCachePlanner(PlannerConfig{
		CacheRoot:          testCacheRoot,
		CapacityAnnotation: DefaultCacheCapacityAnnotation,
	})
	if err != nil {
		t.Fatalf("NewCachePlanner() error = %v", err)
	}
	return planner
}

func TestNewCachePlannerRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	tests := []PlannerConfig{
		{CacheRoot: "/"},
		{CacheRoot: testCacheRoot, CapacityAnnotation: "not an annotation"},
		{CacheRoot: testCacheRoot, NodeSelector: map[string]string{"not a key": "value"}},
		{CacheRoot: testCacheRoot, NodeSelector: map[string]string{"key": "not a value"}},
		{CacheRoot: testCacheRoot, RequiredResources: []corev1.ResourceName{"not a resource"}},
	}
	for _, config := range tests {
		if _, err := NewCachePlanner(config); err == nil {
			t.Errorf("NewCachePlanner(%#v) expected error", config)
		}
	}
}

func testCache(name string) *v1alpha1.ModelCache {
	return &v1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       types.UID(name + "-uid"),
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

func readyNode(name string, capacity string) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			UID:         types.UID(name + "-uid"),
			Annotations: map[string]string{DefaultCacheCapacityAnnotation: capacity},
		},
		Spec: corev1.NodeSpec{Unschedulable: false},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

func TestCachePlannerSelectsNode(t *testing.T) {
	t.Parallel()

	planner := testPlanner(t)
	cache := testCache("qwen-chat")
	nodes := []corev1.Node{
		readyNode("gpu-node-1", "500Gi"),
		readyNode("gpu-node-2", "500Gi"),
	}

	placement, err := planner.Plan(cache, nodes, nil)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if placement.NodeName == "" {
		t.Fatal("expected a node to be selected")
	}
	if placement.NodeUID != "gpu-node-1-uid" {
		t.Errorf("node UID = %q, want gpu-node-1-uid", placement.NodeUID)
	}
	if placement.Path != cache.Spec.Storage.Path {
		t.Errorf("path = %q, want %q", placement.Path, cache.Spec.Storage.Path)
	}
	if placement.ReservedSize.Cmp(resource.MustParse("100Gi")) != 0 {
		t.Errorf("reserved size = %s, want 100Gi", placement.ReservedSize.String())
	}
}

func TestCachePlannerHonorsPinnedNode(t *testing.T) {
	t.Parallel()

	planner := testPlanner(t)
	cache := testCache("qwen-chat")
	cache.Spec.Storage.NodeName = "gpu-node-2"
	nodes := []corev1.Node{
		readyNode("gpu-node-1", "500Gi"),
		readyNode("gpu-node-2", "500Gi"),
	}

	placement, err := planner.Plan(cache, nodes, nil)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if placement.NodeName != "gpu-node-2" {
		t.Errorf("node = %q, want gpu-node-2", placement.NodeName)
	}
}

func TestCachePlannerRejectsMissingPinnedNode(t *testing.T) {
	t.Parallel()

	planner := testPlanner(t)
	cache := testCache("qwen-chat")
	cache.Spec.Storage.NodeName = "missing-node"
	nodes := []corev1.Node{readyNode("gpu-node-1", "500Gi")}

	_, err := planner.Plan(cache, nodes, nil)
	if !errors.Is(err, ErrPinnedNodeUnavailable) {
		t.Fatalf("Plan() error = %v, want ErrPinnedNodeUnavailable", err)
	}
}

func TestCachePlannerSkipsUnreadyAndUnschedulableNodes(t *testing.T) {
	t.Parallel()

	planner := testPlanner(t)
	cache := testCache("qwen-chat")
	nodes := []corev1.Node{
		readyNode("gpu-node-1", "500Gi"),
		{
			ObjectMeta: metav1.ObjectMeta{Name: "cordon-node", Annotations: map[string]string{DefaultCacheCapacityAnnotation: "500Gi"}},
			Spec:       corev1.NodeSpec{Unschedulable: true},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "not-ready-node", Annotations: map[string]string{DefaultCacheCapacityAnnotation: "500Gi"}},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}},
			},
		},
	}

	placement, err := planner.Plan(cache, nodes, nil)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if placement.NodeName != "gpu-node-1" {
		t.Errorf("node = %q, want gpu-node-1", placement.NodeName)
	}
}

func TestCachePlannerRespectsNodeSelector(t *testing.T) {
	t.Parallel()

	planner, err := NewCachePlanner(PlannerConfig{
		CacheRoot:          testCacheRoot,
		CapacityAnnotation: DefaultCacheCapacityAnnotation,
		NodeSelector:       map[string]string{"inferops.dev/cache": "true"},
	})
	if err != nil {
		t.Fatalf("NewCachePlanner() error = %v", err)
	}

	cache := testCache("qwen-chat")
	nodes := []corev1.Node{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "matching", Labels: map[string]string{"inferops.dev/cache": "true"}, Annotations: map[string]string{DefaultCacheCapacityAnnotation: "500Gi"}},
			Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}},
		},
		readyNode("non-matching", "500Gi"),
	}

	placement, err := planner.Plan(cache, nodes, nil)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if placement.NodeName != "matching" {
		t.Errorf("node = %q, want matching", placement.NodeName)
	}
}

func TestCachePlannerRespectsWorkloadScheduling(t *testing.T) {
	t.Parallel()

	planner := testPlanner(t)
	cache := testCache("cache-a")
	cache.Spec.Storage.Size = "10Gi"
	cache.Spec.Storage.NodeSelector = map[string]string{"inferops.dev/pool": "inference"}
	cache.Spec.Storage.Tolerations = []v1alpha1.Toleration{{
		Key:      "dedicated",
		Operator: string(corev1.TolerationOpEqual),
		Value:    "inference",
		Effect:   string(corev1.TaintEffectNoSchedule),
	}}
	general := readyNode("general", "100Gi")
	general.Labels = map[string]string{"inferops.dev/pool": "general"}
	inference := readyNode("inference", "100Gi")
	inference.Labels = map[string]string{"inferops.dev/pool": "inference"}
	inference.Spec.Taints = []corev1.Taint{{
		Key:    "dedicated",
		Value:  "inference",
		Effect: corev1.TaintEffectNoSchedule,
	}}

	placement, err := planner.Plan(cache, []corev1.Node{general, inference}, nil)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if placement.NodeName != inference.Name {
		t.Fatalf("node = %q, want %q", placement.NodeName, inference.Name)
	}

	cache.Spec.Storage.Tolerations = nil
	if _, err := planner.Plan(cache, []corev1.Node{general, inference}, nil); !errors.Is(err, ErrNoEligibleNode) {
		t.Fatalf("Plan() error = %v, want ErrNoEligibleNode", err)
	}
}

func TestCachePlannerRequiresConfiguredNodeResources(t *testing.T) {
	t.Parallel()

	planner, err := NewCachePlanner(PlannerConfig{
		CacheRoot:          testCacheRoot,
		CapacityAnnotation: DefaultCacheCapacityAnnotation,
		RequiredResources:  []corev1.ResourceName{"nvidia.com/gpu"},
	})
	if err != nil {
		t.Fatalf("NewCachePlanner() error = %v", err)
	}

	cache := testCache("qwen-chat")
	cpuNode := readyNode("cpu-node", "500Gi")
	gpuNode := readyNode("gpu-node", "500Gi")
	gpuNode.Status.Allocatable = corev1.ResourceList{
		"nvidia.com/gpu": resource.MustParse("1"),
	}

	placement, err := planner.Plan(cache, []corev1.Node{cpuNode, gpuNode}, nil)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if placement.NodeName != "gpu-node" {
		t.Fatalf("node = %q, want gpu-node", placement.NodeName)
	}
}

func TestCachePlannerAccountsReservedCapacity(t *testing.T) {
	t.Parallel()

	planner := testPlanner(t)
	cache := testCache("qwen-chat")
	nodes := []corev1.Node{
		readyNode("gpu-node-1", "120Gi"),
		readyNode("gpu-node-2", "500Gi"),
	}
	existing := []v1alpha1.ModelCache{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "existing", UID: types.UID("existing-uid")},
			Status: v1alpha1.ModelCacheStatus{
				Phase:        v1alpha1.ModelCachePhaseReady,
				NodeName:     "gpu-node-1",
				ReservedSize: "100Gi",
			},
		},
	}

	placement, err := planner.Plan(cache, nodes, existing)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if placement.NodeName != "gpu-node-2" {
		t.Errorf("node = %q, want gpu-node-2", placement.NodeName)
	}
}

func TestCachePlannerPrefersExistingReadyCopy(t *testing.T) {
	t.Parallel()

	planner := testPlanner(t)
	cache := testCache("qwen-chat")
	// Mark the cache itself as already Ready on gpu-node-2 with less capacity.
	cache.Status.Phase = v1alpha1.ModelCachePhaseReady
	cache.Status.NodeName = "gpu-node-2"
	cache.Status.Path = cache.Spec.Storage.Path
	cache.Status.Revision = "main"

	nodes := []corev1.Node{
		readyNode("gpu-node-1", "1000Gi"),
		readyNode("gpu-node-2", "500Gi"),
	}

	placement, err := planner.Plan(cache, nodes, nil)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if placement.NodeName != "gpu-node-2" {
		t.Errorf("node = %q, want gpu-node-2 (existing ready copy)", placement.NodeName)
	}
}

func TestCachePlannerPrefersReadyCopyFromAnotherCache(t *testing.T) {
	t.Parallel()

	planner := testPlanner(t)
	cache := testCache("qwen-chat")
	existing := *testCache("existing-copy")
	existing.Status = v1alpha1.ModelCacheStatus{
		Phase:        v1alpha1.ModelCachePhaseReady,
		NodeName:     "gpu-node-2",
		Path:         existing.Spec.Storage.Path,
		ReservedSize: existing.Spec.Storage.Size,
		Conditions: []v1alpha1.Condition{{
			Type: v1alpha1.CacheConditionReady, Status: metav1.ConditionTrue,
		}},
	}
	nodes := []corev1.Node{
		readyNode("gpu-node-1", "1000Gi"),
		readyNode("gpu-node-2", "500Gi"),
	}

	placement, err := planner.Plan(cache, nodes, []v1alpha1.ModelCache{existing})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if placement.NodeName != "gpu-node-2" {
		t.Errorf("node = %q, want gpu-node-2 with an existing ready repo/revision copy", placement.NodeName)
	}
}

func TestCachePlannerDeterministicTieBreak(t *testing.T) {
	t.Parallel()

	planner := testPlanner(t)
	cache := testCache("qwen-chat")
	nodes := []corev1.Node{
		readyNode("gpu-node-b", "500Gi"),
		readyNode("gpu-node-a", "500Gi"),
	}

	placement, err := planner.Plan(cache, nodes, nil)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if placement.NodeName != "gpu-node-a" {
		t.Errorf("node = %q, want gpu-node-a (alphabetical tie-break)", placement.NodeName)
	}
}

func TestCachePlannerRejectsInsufficientCapacity(t *testing.T) {
	t.Parallel()

	planner := testPlanner(t)
	cache := testCache("qwen-chat")
	nodes := []corev1.Node{readyNode("gpu-node-1", "50Gi")}

	_, err := planner.Plan(cache, nodes, nil)
	if !errors.Is(err, ErrInsufficientCacheCapacity) {
		t.Fatalf("Plan() error = %v, want ErrInsufficientCacheCapacity", err)
	}
}

func TestCachePlannerDoesNotIgnoreReservationWithMissingStatusSize(t *testing.T) {
	t.Parallel()

	planner := testPlanner(t)
	cache := testCache("qwen-chat")
	nodes := []corev1.Node{
		readyNode("gpu-node-1", "150Gi"),
		readyNode("gpu-node-2", "500Gi"),
	}
	existing := *testCache("existing")
	existing.Status = v1alpha1.ModelCacheStatus{
		Phase:    v1alpha1.ModelCachePhasePending,
		NodeName: "gpu-node-1",
	}

	placement, err := planner.Plan(cache, nodes, []v1alpha1.ModelCache{existing})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if placement.NodeName != "gpu-node-2" {
		t.Fatalf("node = %q, want gpu-node-2", placement.NodeName)
	}
}

func TestCachePlannerRejectsSameNodePathConflict(t *testing.T) {
	t.Parallel()

	planner := testPlanner(t)
	cache := testCache("qwen-chat")
	cache.Spec.Storage.NodeName = "gpu-node-1"
	existing := *testCache("existing")
	existing.Status = v1alpha1.ModelCacheStatus{
		Phase:        v1alpha1.ModelCachePhaseDownloading,
		NodeName:     "gpu-node-1",
		Path:         cache.Spec.Storage.Path,
		ReservedSize: "100Gi",
	}

	_, err := planner.Plan(
		cache,
		[]corev1.Node{readyNode("gpu-node-1", "500Gi")},
		[]v1alpha1.ModelCache{existing},
	)
	if !errors.Is(err, ErrCachePathConflict) {
		t.Fatalf("Plan() error = %v, want ErrCachePathConflict", err)
	}
}

func TestFailedReadyCacheRetainsReservation(t *testing.T) {
	t.Parallel()

	planner := testPlanner(t)
	cache := testCache("qwen-chat")
	nodes := []corev1.Node{
		readyNode("gpu-node-1", "150Gi"),
		readyNode("gpu-node-2", "500Gi"),
	}
	existing := *testCache("existing")
	existing.Status = v1alpha1.ModelCacheStatus{
		Phase:        v1alpha1.ModelCachePhaseFailed,
		NodeName:     "gpu-node-1",
		Path:         existing.Spec.Storage.Path,
		ReservedSize: "100Gi",
		Conditions: []v1alpha1.Condition{{
			Type:   v1alpha1.CacheConditionDownloaded,
			Status: metav1.ConditionTrue,
		}},
	}

	placement, err := planner.Plan(cache, nodes, []v1alpha1.ModelCache{existing})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if placement.NodeName != "gpu-node-2" {
		t.Fatalf("node = %q, want gpu-node-2", placement.NodeName)
	}
}

func TestCachePlannerRejectsNonPositiveNodeCapacity(t *testing.T) {
	t.Parallel()

	planner := testPlanner(t)
	cache := testCache("qwen-chat")
	_, err := planner.Plan(cache, []corev1.Node{readyNode("gpu-node-1", "0")}, nil)
	if !errors.Is(err, ErrInsufficientCacheCapacity) {
		t.Fatalf("Plan() error = %v, want ErrInsufficientCacheCapacity", err)
	}
}

func TestCachePlannerRejectsInvalidSize(t *testing.T) {
	t.Parallel()

	planner := testPlanner(t)
	cache := testCache("qwen-chat")
	cache.Spec.Storage.Size = "not-a-quantity"
	nodes := []corev1.Node{readyNode("gpu-node-1", "500Gi")}

	_, err := planner.Plan(cache, nodes, nil)
	if err == nil {
		t.Fatal("Plan() expected error for invalid size")
	}
}

func TestCachePlannerRejectsNonPositiveSize(t *testing.T) {
	t.Parallel()

	planner := testPlanner(t)
	cache := testCache("qwen-chat")
	cache.Spec.Storage.Size = "0"
	nodes := []corev1.Node{readyNode("gpu-node-1", "500Gi")}

	_, err := planner.Plan(cache, nodes, nil)
	if err == nil {
		t.Fatal("Plan() expected error for non-positive size")
	}
}

func TestCachePlannerRejectsPathOutsideRoot(t *testing.T) {
	t.Parallel()

	planner := testPlanner(t)
	cache := testCache("qwen-chat")
	cache.Spec.Storage.Path = "/tmp/model"
	nodes := []corev1.Node{readyNode("gpu-node-1", "500Gi")}

	_, err := planner.Plan(cache, nodes, nil)
	if err == nil {
		t.Fatal("Plan() expected error for path outside root")
	}
}

func TestCachePlannerRejectsUnsupportedStorage(t *testing.T) {
	t.Parallel()

	planner := testPlanner(t)
	cache := testCache("qwen-chat")
	cache.Spec.Storage.Type = "pvc"
	nodes := []corev1.Node{readyNode("gpu-node-1", "500Gi")}

	_, err := planner.Plan(cache, nodes, nil)
	if err == nil {
		t.Fatal("Plan() expected error for unsupported storage type")
	}
}
