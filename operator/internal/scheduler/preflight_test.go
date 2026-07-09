package scheduler

import (
	"errors"
	"testing"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestValidateRuntimeNode(t *testing.T) {
	t.Parallel()

	deployment := &v1alpha1.ModelDeployment{
		Spec: v1alpha1.ModelDeploymentSpec{
			Resources: v1alpha1.ResourceRequirements{CPU: "2", Memory: "4Gi"},
			Scaling:   v1alpha1.ScalingSpec{MinReplicas: 2, MaxReplicas: 2},
			Scheduling: v1alpha1.SchedulingSpec{
				NodeSelector: map[string]string{"inferops.dev/pool": "inference"},
				Tolerations: []v1alpha1.Toleration{
					{
						Key:      "inferops.dev/gpu",
						Operator: string(corev1.TolerationOpExists),
						Effect:   string(corev1.TaintEffectNoSchedule),
					},
				},
			},
		},
	}
	node := &corev1.Node{
		Spec: corev1.NodeSpec{
			Taints: []corev1.Taint{
				{
					Key:    "inferops.dev/gpu",
					Value:  "true",
					Effect: corev1.TaintEffectNoSchedule,
				},
			},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("8"),
				corev1.ResourceMemory: resource.MustParse("16Gi"),
			},
		},
	}
	node.Name = "gpu-node"
	node.Labels = map[string]string{"inferops.dev/pool": "inference"}

	if err := ValidateRuntimeNode(deployment, node); err != nil {
		t.Fatalf("ValidateRuntimeNode() error = %v", err)
	}
}

func TestValidateRuntimeNodeFailures(t *testing.T) {
	t.Parallel()

	baseDeployment := &v1alpha1.ModelDeployment{
		Spec: v1alpha1.ModelDeploymentSpec{
			Resources: v1alpha1.ResourceRequirements{CPU: "4", Memory: "8Gi"},
			Scaling:   v1alpha1.ScalingSpec{MinReplicas: 1, MaxReplicas: 1},
			Scheduling: v1alpha1.SchedulingSpec{
				NodeSelector: map[string]string{"inferops.dev/pool": "inference"},
			},
		},
	}
	baseNode := &corev1.Node{
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("8"),
				corev1.ResourceMemory: resource.MustParse("16Gi"),
			},
		},
	}
	baseNode.Name = "node-1"
	baseNode.Labels = map[string]string{"inferops.dev/pool": "inference"}

	tests := []struct {
		name   string
		mutate func(*v1alpha1.ModelDeployment, *corev1.Node)
		target error
	}{
		{
			name: "selector mismatch",
			mutate: func(_ *v1alpha1.ModelDeployment, node *corev1.Node) {
				node.Labels["inferops.dev/pool"] = "general"
			},
			target: ErrSchedulingConstraints,
		},
		{
			name: "untolerated taint",
			mutate: func(_ *v1alpha1.ModelDeployment, node *corev1.Node) {
				node.Spec.Taints = []corev1.Taint{{
					Key:    "dedicated",
					Value:  "gpu",
					Effect: corev1.TaintEffectNoSchedule,
				}}
			},
			target: ErrSchedulingConstraints,
		},
		{
			name: "insufficient aggregate CPU",
			mutate: func(deployment *v1alpha1.ModelDeployment, _ *corev1.Node) {
				deployment.Spec.Scaling.MinReplicas = 3
				deployment.Spec.Scaling.MaxReplicas = 3
			},
			target: ErrInsufficientComputeCapacity,
		},
		{
			name: "insufficient aggregate CPU for planned replicas",
			mutate: func(deployment *v1alpha1.ModelDeployment, _ *corev1.Node) {
				deployment.Spec.Scaling.MaxReplicas = 3
				deployment.Status.Scaling.DesiredReplicas = 3
			},
			target: ErrInsufficientComputeCapacity,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			deployment := baseDeployment.DeepCopy()
			node := baseNode.DeepCopy()
			test.mutate(deployment, node)
			err := ValidateRuntimeNode(deployment, node)
			if !errors.Is(err, test.target) {
				t.Fatalf("ValidateRuntimeNode() error = %v, want %v", err, test.target)
			}
		})
	}
}
