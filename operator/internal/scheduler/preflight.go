package scheduler

import (
	"errors"
	"fmt"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

var (
	// ErrSchedulingConstraints means the selected cache node cannot satisfy
	// explicit runtime placement constraints.
	ErrSchedulingConstraints = errors.New("runtime scheduling constraints are unsatisfied")
	// ErrInsufficientComputeCapacity means a runtime request cannot fit in the
	// selected node's total allocatable CPU or memory.
	ErrInsufficientComputeCapacity = errors.New("insufficient compute capacity")
)

// ValidateRuntimeNode performs deterministic checks that can be evaluated
// without reading all pods in the cluster. Kubernetes remains authoritative
// for currently free CPU and memory at bind time.
func ValidateRuntimeNode(
	deployment *v1alpha1.ModelDeployment,
	node *corev1.Node,
) error {
	if deployment == nil {
		return errors.New("model deployment is required")
	}
	if node == nil || node.Name == "" {
		return errors.New("runtime node is required")
	}
	for key, value := range deployment.Spec.Scheduling.NodeSelector {
		if node.Labels[key] != value {
			return fmt.Errorf(
				"%w: node %q has label %s=%q, need %q",
				ErrSchedulingConstraints,
				node.Name,
				key,
				node.Labels[key],
				value,
			)
		}
	}
	for i := range node.Spec.Taints {
		taint := &node.Spec.Taints[i]
		if taint.Effect != corev1.TaintEffectNoSchedule &&
			taint.Effect != corev1.TaintEffectNoExecute {
			continue
		}
		if !isTaintTolerated(*taint, deployment.Spec.Scheduling.Tolerations) {
			return fmt.Errorf(
				"%w: node %q has untolerated taint %s=%s:%s",
				ErrSchedulingConstraints,
				node.Name,
				taint.Key,
				taint.Value,
				taint.Effect,
			)
		}
	}

	replicas := runtimeReplicaCount(deployment)
	for _, requested := range []struct {
		name  corev1.ResourceName
		value string
	}{
		{name: corev1.ResourceCPU, value: deployment.Spec.Resources.CPU},
		{name: corev1.ResourceMemory, value: deployment.Spec.Resources.Memory},
	} {
		if requested.value == "" {
			continue
		}
		quantity, err := resource.ParseQuantity(requested.value)
		if err != nil {
			return fmt.Errorf("parse requested %s %q: %w", requested.name, requested.value, err)
		}
		if ok := quantity.Mul(replicas); !ok {
			return fmt.Errorf("scale requested %s by %d replicas: quantity overflow", requested.name, replicas)
		}
		allocatable := node.Status.Allocatable[requested.name]
		if allocatable.Cmp(quantity) < 0 {
			return fmt.Errorf(
				"%w: node %q has %s allocatable %s, need %s for %d replica(s)",
				ErrInsufficientComputeCapacity,
				node.Name,
				requested.name,
				allocatable.String(),
				quantity.String(),
				replicas,
			)
		}
	}
	return nil
}

func runtimeReplicaCount(deployment *v1alpha1.ModelDeployment) int64 {
	replicas := int64(deployment.Status.Scaling.DesiredReplicas)
	if replicas < 1 {
		replicas = int64(deployment.Spec.Scaling.MinReplicas)
	}
	if replicas < 1 {
		return 1
	}
	return replicas
}

func isTaintTolerated(taint corev1.Taint, tolerations []v1alpha1.Toleration) bool {
	for i := range tolerations {
		toleration := tolerations[i]
		if toleration.Effect != "" && corev1.TaintEffect(toleration.Effect) != taint.Effect {
			continue
		}
		operator := corev1.TolerationOperator(toleration.Operator)
		if operator == "" {
			operator = corev1.TolerationOpEqual
		}
		switch operator {
		case corev1.TolerationOpExists:
			if toleration.Key == "" || toleration.Key == taint.Key {
				return true
			}
		case corev1.TolerationOpEqual:
			if toleration.Key == taint.Key && toleration.Value == taint.Value {
				return true
			}
		}
	}
	return false
}
