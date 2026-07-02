package scheduler

import (
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGPUPlanner(t *testing.T) {
	t.Parallel()

	node := func(name string, count int64, ready bool) corev1.Node {
		status := corev1.ConditionFalse
		if ready {
			status = corev1.ConditionTrue
		}
		return corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   name,
				Labels: map[string]string{"inferops.dev/gpu-type": "a100"},
			},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{"nvidia.com/gpu": Quantity(count)},
				Conditions: []corev1.NodeCondition{{
					Type: corev1.NodeReady, Status: status,
				}},
			},
		}
	}
	nodes := []corev1.Node{node("node-b", 2, true), node("node-a", 2, true), node("down", 8, false)}
	planner, err := NewGPUPlanner(GPUPlannerConfig{})
	if err != nil {
		t.Fatalf("NewGPUPlanner() error = %v", err)
	}

	tests := []struct {
		name        string
		request     GPURequest
		allocations []GPUAllocation
		wantNode    string
		wantErr     error
	}{
		{
			name:     "deterministic tie",
			request:  GPURequest{ResourceName: "nvidia.com/gpu", Count: 1},
			wantNode: "node-a",
		},
		{
			name:    "cache preferred",
			request: GPURequest{ResourceName: "nvidia.com/gpu", Count: 1, PreferredNode: "node-b"},
			allocations: []GPUAllocation{{
				NodeName: "node-a", ResourceName: "nvidia.com/gpu", Count: 1,
			}},
			wantNode: "node-b",
		},
		{
			name:    "insufficient",
			request: GPURequest{ResourceName: "nvidia.com/gpu", Count: 2},
			allocations: []GPUAllocation{
				{NodeName: "node-a", ResourceName: "nvidia.com/gpu", Count: 1},
				{NodeName: "node-b", ResourceName: "nvidia.com/gpu", Count: 1},
			},
			wantErr: ErrInsufficientGPUCapacity,
		},
		{
			name:    "type mismatch",
			request: GPURequest{ResourceName: "nvidia.com/gpu", Count: 1, Type: "h100"},
			wantErr: ErrNoCompatibleGPUNode,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := planner.Plan(tt.request, nodes, tt.allocations)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Plan() error = %v, want %v", err, tt.wantErr)
			}
			if got.NodeName != tt.wantNode {
				t.Errorf("Plan() node = %q, want %q", got.NodeName, tt.wantNode)
			}
		})
	}
}
