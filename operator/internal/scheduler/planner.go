package scheduler

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	"github.com/brassinai/inferops/operator/internal/paths"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
)

const (
	// DefaultCacheCapacityAnnotation is the node annotation that advertises the
	// configured cache capacity in bytes for InferOps node-local caches.
	DefaultCacheCapacityAnnotation = "inferops.dev/cache-capacity"
)

var (
	// ErrNoEligibleNode indicates that no Ready, schedulable node matched the
	// configured cache selector.
	ErrNoEligibleNode = errors.New("no eligible cache node")
	// ErrPinnedNodeUnavailable indicates that the user-selected node cannot
	// currently host the cache.
	ErrPinnedNodeUnavailable = errors.New("pinned cache node unavailable")
	// ErrInsufficientCacheCapacity indicates that eligible nodes exist but none
	// has enough unreserved configured cache capacity.
	ErrInsufficientCacheCapacity = errors.New("insufficient configured cache capacity")
	// ErrCachePathConflict indicates that another cache already owns the same
	// path on the selected node.
	ErrCachePathConflict = errors.New("cache path already reserved")
)

// PlannerConfig configures cache placement decisions. All values are trusted
// operator configuration, not user input.
type PlannerConfig struct {
	// CacheRoot is the configured hostPath root for node-local caches.
	CacheRoot string
	// NodeSelector restricts which nodes may host a cache.
	NodeSelector map[string]string
	// CapacityAnnotation is the node annotation key that holds the configured
	// cache capacity in bytes.
	CapacityAnnotation string
	// RequiredResources restricts placement to nodes advertising positive
	// allocatable quantities for every named resource.
	RequiredResources []corev1.ResourceName
}

// Placement is the result of selecting a cache destination.
type Placement struct {
	NodeName     string
	NodeUID      string
	Path         string
	ReservedSize resource.Quantity
}

// CachePlanner selects a suitable node and path for a ModelCache.
type CachePlanner struct {
	config PlannerConfig
}

// NewCachePlanner creates a planner with validated configuration.
func NewCachePlanner(config PlannerConfig) (*CachePlanner, error) {
	cacheRoot, err := paths.CleanAbsolutePath(config.CacheRoot, "cache root")
	if err != nil {
		return nil, err
	}
	if cacheRoot == "/" {
		return nil, errors.New("cache root must not be the filesystem root")
	}
	capacityAnnotation := config.CapacityAnnotation
	if capacityAnnotation == "" {
		capacityAnnotation = DefaultCacheCapacityAnnotation
	}
	if messages := utilvalidation.IsQualifiedName(capacityAnnotation); len(messages) != 0 {
		return nil, fmt.Errorf("capacity annotation %q is invalid: %s", capacityAnnotation, strings.Join(messages, ", "))
	}
	for key, value := range config.NodeSelector {
		if messages := utilvalidation.IsQualifiedName(key); len(messages) != 0 {
			return nil, fmt.Errorf("cache node selector key %q is invalid: %s", key, strings.Join(messages, ", "))
		}
		if messages := utilvalidation.IsValidLabelValue(value); len(messages) != 0 {
			return nil, fmt.Errorf("cache node selector value %q for key %q is invalid: %s", value, key, strings.Join(messages, ", "))
		}
	}
	requiredResources := append([]corev1.ResourceName(nil), config.RequiredResources...)
	for _, name := range requiredResources {
		if messages := utilvalidation.IsQualifiedName(string(name)); len(messages) != 0 {
			return nil, fmt.Errorf("required resource %q is invalid: %s", name, strings.Join(messages, ", "))
		}
	}
	return &CachePlanner{
		config: PlannerConfig{
			CacheRoot:          cacheRoot,
			NodeSelector:       copyStringMap(config.NodeSelector),
			CapacityAnnotation: capacityAnnotation,
			RequiredResources:  requiredResources,
		},
	}, nil
}

// Plan selects a placement for the requested cache. It honors an explicit
// spec.storage.nodeName pin, filters to ready schedulable nodes matching the
// configured selector, accounts for capacity reserved by other non-terminal
// caches, and breaks ties deterministically.
func (p *CachePlanner) Plan(
	cache *v1alpha1.ModelCache,
	nodes []corev1.Node,
	caches []v1alpha1.ModelCache,
) (*Placement, error) {
	if cache == nil {
		return nil, errors.New("model cache is required")
	}
	if cache.Spec.Storage.Type != "nodeLocal" {
		return nil, fmt.Errorf("storage type %q is not supported by the node-local planner", cache.Spec.Storage.Type)
	}

	cachePath, err := paths.CleanAbsolutePath(cache.Spec.Storage.Path, "cache path")
	if err != nil {
		return nil, err
	}
	if err := paths.UnderRoot(cachePath, p.config.CacheRoot, "cache path"); err != nil {
		return nil, err
	}

	size, err := resource.ParseQuantity(cache.Spec.Storage.Size)
	if err != nil {
		return nil, fmt.Errorf("parse storage size %q: %w", cache.Spec.Storage.Size, err)
	}
	if size.Sign() <= 0 {
		return nil, fmt.Errorf("storage size %q must be greater than zero", cache.Spec.Storage.Size)
	}

	candidates := p.filterNodes(nodes, cache.Spec.Storage)
	if len(candidates) == 0 {
		return nil, fmt.Errorf(
			"%w: no Ready, schedulable nodes match the cache selectors and tolerations",
			ErrNoEligibleNode,
		)
	}

	pinnedNode := cache.Spec.Storage.NodeName
	if pinnedNode != "" {
		found := false
		for i := range candidates {
			if candidates[i].Name == pinnedNode {
				candidates = []corev1.Node{candidates[i]}
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("%w: node %q is not Ready, schedulable, or selected for caching", ErrPinnedNodeUnavailable, pinnedNode)
		}
	}

	reserved := reservedBytesByNode(caches, string(cache.UID))
	conflicts := conflictingNodesForPath(caches, string(cache.UID), cachePath)
	nodesWithReadyCopy := collectReadyCopyNodes(cache, caches)
	if pinnedNode != "" && conflicts[pinnedNode] {
		return nil, fmt.Errorf("%w: path %q on pinned node %q", ErrCachePathConflict, cachePath, pinnedNode)
	}
	ranked := rankNodes(cache, candidates, reserved, conflicts, nodesWithReadyCopy, p.config.CapacityAnnotation)
	if len(ranked) == 0 {
		if pinnedNode != "" {
			return nil, fmt.Errorf("%w: pinned node %q cannot reserve %s", ErrInsufficientCacheCapacity, pinnedNode, size.String())
		}
		return nil, fmt.Errorf("%w: no eligible node can reserve %s", ErrInsufficientCacheCapacity, size.String())
	}

	selected := ranked[0]
	return &Placement{
		NodeName:     selected.Node.Name,
		NodeUID:      string(selected.Node.UID),
		Path:         cachePath,
		ReservedSize: size,
	}, nil
}

// PlanPath validates the requested path for a cache without selecting a node.
func (p *CachePlanner) PlanPath(cache *v1alpha1.ModelCache) (string, error) {
	if cache == nil {
		return "", errors.New("model cache is required")
	}
	cachePath, err := paths.CleanAbsolutePath(cache.Spec.Storage.Path, "cache path")
	if err != nil {
		return "", err
	}
	if err := paths.UnderRoot(cachePath, p.config.CacheRoot, "cache path"); err != nil {
		return "", err
	}
	return cachePath, nil
}

func (p *CachePlanner) filterNodes(
	nodes []corev1.Node,
	storage v1alpha1.ModelCacheStorage,
) []corev1.Node {
	result := make([]corev1.Node, 0, len(nodes))
	for i := range nodes {
		if !IsNodeEligibleForCache(nodes[i]) {
			continue
		}
		if !nodeMatchesSelector(nodes[i], p.config.NodeSelector) {
			continue
		}
		if !nodeMatchesSelector(nodes[i], storage.NodeSelector) {
			continue
		}
		if hasUntoleratedSchedulingTaint(nodes[i], storage.Tolerations) {
			continue
		}
		if !nodeHasRequiredResources(nodes[i], p.config.RequiredResources) {
			continue
		}
		result = append(result, nodes[i])
	}
	return result
}

func hasUntoleratedSchedulingTaint(node corev1.Node, tolerations []v1alpha1.Toleration) bool {
	for i := range node.Spec.Taints {
		taint := node.Spec.Taints[i]
		if taint.Effect != corev1.TaintEffectNoSchedule &&
			taint.Effect != corev1.TaintEffectNoExecute {
			continue
		}
		if !isTaintTolerated(taint, tolerations) {
			return true
		}
	}
	return false
}

func nodeHasRequiredResources(node corev1.Node, required []corev1.ResourceName) bool {
	for _, name := range required {
		quantity, found := node.Status.Allocatable[name]
		if !found || quantity.Sign() <= 0 {
			return false
		}
	}
	return true
}

// IsNodeEligibleForCache reports whether a node may host a cache.
func IsNodeEligibleForCache(node corev1.Node) bool {
	if node.Spec.Unschedulable {
		return false
	}
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func nodeMatchesSelector(node corev1.Node, selector map[string]string) bool {
	labels := node.Labels
	for key, value := range selector {
		if labels[key] != value {
			return false
		}
	}
	return true
}

func reservedBytesByNode(caches []v1alpha1.ModelCache, selfUID string) map[string]int64 {
	reserved := make(map[string]int64)
	for i := range caches {
		cache := &caches[i]
		if string(cache.UID) == selfUID {
			continue
		}
		if cache.Status.NodeName == "" {
			continue
		}
		if cache.Status.Phase == v1alpha1.ModelCachePhaseFailed &&
			!cacheConditionTrue(cache.Status.Conditions, v1alpha1.CacheConditionDownloaded) {
			continue
		}
		size, err := resource.ParseQuantity(cache.Status.ReservedSize)
		if err != nil || size.Sign() <= 0 {
			size, err = resource.ParseQuantity(cache.Spec.Storage.Size)
		}
		if err != nil {
			// Invalid caches are rejected by validation and do not own a valid
			// reservation. Do not let malformed status poison all placement.
			continue
		}
		value := size.Value()
		current := reserved[cache.Status.NodeName]
		if value > math.MaxInt64-current {
			reserved[cache.Status.NodeName] = math.MaxInt64
			continue
		}
		reserved[cache.Status.NodeName] = current + value
	}
	return reserved
}

func conflictingNodesForPath(
	caches []v1alpha1.ModelCache,
	selfUID string,
	cachePath string,
) map[string]bool {
	conflicts := make(map[string]bool)
	for i := range caches {
		cache := &caches[i]
		if string(cache.UID) == selfUID ||
			cache.Status.NodeName == "" ||
			cache.Status.Path != cachePath {
			continue
		}
		if cache.Status.Phase == v1alpha1.ModelCachePhaseFailed &&
			!cacheConditionTrue(cache.Status.Conditions, v1alpha1.CacheConditionDownloaded) {
			continue
		}
		conflicts[cache.Status.NodeName] = true
	}
	return conflicts
}

func cacheConditionTrue(conditions []v1alpha1.Condition, conditionType string) bool {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return conditions[i].Status == metav1.ConditionTrue
		}
	}
	return false
}

func nodeCacheCapacity(node corev1.Node, annotation string) (int64, bool) {
	value, ok := node.Annotations[annotation]
	if !ok {
		return 0, false
	}
	quantity, err := resource.ParseQuantity(value)
	if err != nil || quantity.Sign() <= 0 {
		return 0, false
	}
	return quantity.Value(), true
}

type rankedNode struct {
	Node                 corev1.Node
	Capacity             int64
	Reserved             int64
	HasExistingPlacement bool
	FreeCapacity         int64
}

func collectReadyCopyNodes(requested *v1alpha1.ModelCache, caches []v1alpha1.ModelCache) map[string]bool {
	nodes := make(map[string]bool)
	requestedRevision := EffectiveRevision(requested)
	for i := range caches {
		cache := &caches[i]
		if cache.Status.NodeName == "" ||
			cache.Status.Phase != v1alpha1.ModelCachePhaseReady ||
			!cacheConditionTrue(cache.Status.Conditions, v1alpha1.CacheConditionReady) ||
			cache.Spec.ModelRepo != requested.Spec.ModelRepo ||
			EffectiveRevision(cache) != requestedRevision {
			continue
		}
		nodes[cache.Status.NodeName] = true
	}
	return nodes
}

func rankNodes(
	cache *v1alpha1.ModelCache,
	nodes []corev1.Node,
	reserved map[string]int64,
	conflicts map[string]bool,
	readyCopyNodes map[string]bool,
	capacityAnnotation string,
) []rankedNode {
	requested, err := resource.ParseQuantity(cache.Spec.Storage.Size)
	if err != nil {
		return nil
	}
	requestedBytes := requested.Value()

	ranked := make([]rankedNode, 0, len(nodes))
	for i := range nodes {
		if conflicts[nodes[i].Name] {
			continue
		}
		capacity, ok := nodeCacheCapacity(nodes[i], capacityAnnotation)
		if !ok {
			continue
		}
		nodeReserved := reserved[nodes[i].Name]
		free := capacity - nodeReserved
		if free < requestedBytes {
			continue
		}

		hasExistingPlacement := (cache.Status.NodeName == nodes[i].Name &&
			cache.Status.Path == cache.Spec.Storage.Path &&
			cache.Status.Phase != v1alpha1.ModelCachePhaseFailed) ||
			readyCopyNodes[nodes[i].Name]
		ranked = append(ranked, rankedNode{
			Node:                 nodes[i],
			Capacity:             capacity,
			Reserved:             nodeReserved,
			HasExistingPlacement: hasExistingPlacement,
			FreeCapacity:         free,
		})
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].HasExistingPlacement != ranked[j].HasExistingPlacement {
			return ranked[i].HasExistingPlacement
		}
		if ranked[i].FreeCapacity != ranked[j].FreeCapacity {
			return ranked[i].FreeCapacity > ranked[j].FreeCapacity
		}
		return ranked[i].Node.Name < ranked[j].Node.Name
	})
	return ranked
}

func copyStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

// EffectiveRevision returns the resolved revision for a cache. For now this is
// the requested revision; future work may resolve Hugging Face branches/tags.
func EffectiveRevision(cache *v1alpha1.ModelCache) string {
	if cache == nil {
		return ""
	}
	if cache.Spec.Revision != "" {
		return cache.Spec.Revision
	}
	return "main"
}
