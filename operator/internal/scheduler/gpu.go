package scheduler

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
)

var (
	// ErrNoCompatibleGPUNode means no Ready schedulable node advertises the
	// requested GPU resource and optional type.
	ErrNoCompatibleGPUNode = errors.New("no compatible GPU node")
	// ErrInsufficientGPUCapacity means compatible nodes exist but all whole-GPU
	// slots are reserved.
	ErrInsufficientGPUCapacity = errors.New("insufficient GPU capacity")
)

// GPURequest describes one activation reservation.
type GPURequest struct {
	ResourceName  corev1.ResourceName
	Count         int64
	Type          string
	PreferredNode string
}

// GPUAllocation is an existing InferOps whole-GPU reservation.
type GPUAllocation struct {
	NodeName     string
	ResourceName corev1.ResourceName
	Count        int64
}

// GPUPlacement identifies the selected node and aggregate slot counts.
type GPUPlacement struct {
	NodeName  string
	Total     int64
	Occupied  int64
	Available int64
}

// GPUPlannerConfig contains cluster policy that is independent of workload
// custom resources.
type GPUPlannerConfig struct {
	NodeSelector map[string]string
	GPUTypeLabel string
}

// GPUPlanner selects node-level whole-GPU capacity. Kubernetes and the device
// plugin remain responsible for assigning physical devices.
type GPUPlanner struct {
	nodeSelector map[string]string
	gpuTypeLabel string
}

// NewGPUPlanner validates and creates a GPU planner.
func NewGPUPlanner(config GPUPlannerConfig) (*GPUPlanner, error) {
	if config.GPUTypeLabel == "" {
		config.GPUTypeLabel = "inferops.dev/gpu-type"
	}
	if messages := utilvalidation.IsQualifiedName(config.GPUTypeLabel); len(messages) != 0 {
		return nil, fmt.Errorf("GPU type label %q is invalid: %s", config.GPUTypeLabel, strings.Join(messages, ", "))
	}
	for key, value := range config.NodeSelector {
		if messages := utilvalidation.IsQualifiedName(key); len(messages) != 0 {
			return nil, fmt.Errorf("GPU node selector key %q is invalid: %s", key, strings.Join(messages, ", "))
		}
		if messages := utilvalidation.IsValidLabelValue(value); len(messages) != 0 {
			return nil, fmt.Errorf("GPU node selector value %q is invalid: %s", value, strings.Join(messages, ", "))
		}
	}
	return &GPUPlanner{
		nodeSelector: copyStringMap(config.NodeSelector),
		gpuTypeLabel: config.GPUTypeLabel,
	}, nil
}

// Plan selects compatible free capacity deterministically. A preferred node
// wins when it has enough capacity; remaining ties prefer more available slots
// and then node name.
func (p *GPUPlanner) Plan(
	request GPURequest,
	nodes []corev1.Node,
	allocations []GPUAllocation,
) (GPUPlacement, error) {
	if p == nil {
		return GPUPlacement{}, errors.New("GPU planner is required")
	}
	if request.ResourceName == "" {
		return GPUPlacement{}, errors.New("GPU resource name is required")
	}
	if request.Count <= 0 {
		return GPUPlacement{}, errors.New("GPU count must be greater than zero")
	}

	occupied := make(map[string]int64)
	for _, allocation := range allocations {
		if allocation.ResourceName == request.ResourceName && allocation.Count > 0 {
			occupied[allocation.NodeName] += allocation.Count
		}
	}

	type candidate struct {
		placement GPUPlacement
		preferred bool
	}
	var candidates []candidate
	compatible := false
	for i := range nodes {
		node := nodes[i]
		if !IsNodeEligibleForCache(node) || !nodeMatchesSelector(node, p.nodeSelector) {
			continue
		}
		if request.Type != "" && node.Labels[p.gpuTypeLabel] != request.Type {
			continue
		}
		quantity, found := node.Status.Allocatable[request.ResourceName]
		if !found || quantity.Sign() <= 0 {
			continue
		}
		compatible = true
		total := quantity.Value()
		used := occupied[node.Name]
		available := total - used
		if available < request.Count {
			continue
		}
		candidates = append(candidates, candidate{
			placement: GPUPlacement{
				NodeName:  node.Name,
				Total:     total,
				Occupied:  used,
				Available: available,
			},
			preferred: node.Name == request.PreferredNode,
		})
	}
	if len(candidates) == 0 {
		if compatible {
			return GPUPlacement{}, fmt.Errorf("%w: need %d %s slot(s)", ErrInsufficientGPUCapacity, request.Count, request.ResourceName)
		}
		return GPUPlacement{}, fmt.Errorf("%w: no node advertises %s", ErrNoCompatibleGPUNode, request.ResourceName)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].preferred != candidates[j].preferred {
			return candidates[i].preferred
		}
		if candidates[i].placement.Available != candidates[j].placement.Available {
			return candidates[i].placement.Available > candidates[j].placement.Available
		}
		return candidates[i].placement.NodeName < candidates[j].placement.NodeName
	})
	return candidates[0].placement, nil
}

// Snapshot computes bounded aggregate metrics for one GPU resource.
func Snapshot(resourceName corev1.ResourceName, nodes []corev1.Node, allocations []GPUAllocation) (total, occupied, available int64) {
	for i := range nodes {
		quantity := nodes[i].Status.Allocatable[resourceName]
		total += quantity.Value()
	}
	for _, allocation := range allocations {
		if allocation.ResourceName == resourceName && allocation.Count > 0 {
			occupied += allocation.Count
		}
	}
	available = total - occupied
	if available < 0 {
		available = 0
	}
	return total, occupied, available
}

// Quantity is a test helper that creates an allocatable whole-device quantity.
func Quantity(value int64) resource.Quantity {
	return *resource.NewQuantity(value, resource.DecimalSI)
}
