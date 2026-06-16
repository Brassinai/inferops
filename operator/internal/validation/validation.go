package validation

import (
	"fmt"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
)

// ValidateModelDeployment validates the minimal fields needed for a model deployment.
func ValidateModelDeployment(deployment v1alpha1.ModelDeployment) error {
	if deployment.Metadata.Name == "" {
		return fmt.Errorf("metadata.name is required")
	}
	if deployment.Spec.Model.Repo == "" {
		return fmt.Errorf("spec.model.repo is required")
	}
	if deployment.Spec.Runtime.Ref == "" {
		return fmt.Errorf("spec.runtime.ref is required")
	}
	if deployment.Spec.Resources.GPU == nil {
		if deployment.Spec.Resources.CPU == "" {
			return fmt.Errorf("spec.resources.cpu is required for CPU-only deployments")
		}
		if deployment.Spec.Resources.Memory == "" {
			return fmt.Errorf("spec.resources.memory is required for CPU-only deployments")
		}
		if deployment.Spec.Runtime.TensorParallelSize != 0 {
			return fmt.Errorf("spec.runtime.tensorParallelSize requires spec.resources.gpu")
		}
		if deployment.Spec.Runtime.GPUMemoryUtilization != 0 {
			return fmt.Errorf("spec.runtime.gpuMemoryUtilization requires spec.resources.gpu")
		}
	} else {
		if deployment.Spec.Resources.GPU.Count < 1 {
			return fmt.Errorf("spec.resources.gpu.count must be at least 1")
		}
		if deployment.Spec.Runtime.TensorParallelSize > deployment.Spec.Resources.GPU.Count {
			return fmt.Errorf("spec.runtime.tensorParallelSize must not exceed spec.resources.gpu.count")
		}
	}
	switch deployment.Spec.Activation.DesiredState {
	case "", v1alpha1.ActivationDesiredStateInactive, v1alpha1.ActivationDesiredStateActive:
	default:
		return fmt.Errorf("spec.activation.desiredState %q is invalid", deployment.Spec.Activation.DesiredState)
	}
	switch deployment.Spec.Activation.WhenFull {
	case "", v1alpha1.ActivationWhenFullQueue, v1alpha1.ActivationWhenFullReject,
		v1alpha1.ActivationWhenFullReplaceOldest, v1alpha1.ActivationWhenFullReplaceLowestPriority:
	default:
		return fmt.Errorf("spec.activation.whenFull %q is invalid", deployment.Spec.Activation.WhenFull)
	}
	if deployment.Spec.Scaling.MinReplicas < 0 {
		return fmt.Errorf("spec.scaling.minReplicas must not be negative")
	}
	if deployment.Spec.Scaling.MaxReplicas < deployment.Spec.Scaling.MinReplicas {
		return fmt.Errorf("spec.scaling.maxReplicas must be greater than or equal to minReplicas")
	}
	return nil
}
