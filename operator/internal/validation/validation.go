package validation

import (
	"errors"
	"fmt"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
)

// ValidateModelDeployment validates the minimal fields needed for a model deployment.
func ValidateModelDeployment(deployment v1alpha1.ModelDeployment) error {
	if deployment.Name == "" {
		return errors.New("metadata.name is required")
	}
	if deployment.Spec.Model.Repo == "" {
		return errors.New("spec.model.repo is required")
	}
	if deployment.Spec.Runtime.Ref == "" {
		return errors.New("spec.runtime.ref is required")
	}
	if deployment.Spec.Resources.GPU == nil {
		if deployment.Spec.Resources.CPU == "" {
			return errors.New("spec.resources.cpu is required for CPU-only deployments")
		}
		if deployment.Spec.Resources.Memory == "" {
			return errors.New("spec.resources.memory is required for CPU-only deployments")
		}
		if deployment.Spec.Runtime.TensorParallelSize != 0 {
			return errors.New("spec.runtime.tensorParallelSize requires spec.resources.gpu")
		}
		if deployment.Spec.Runtime.GPUMemoryUtilization != 0 {
			return errors.New("spec.runtime.gpuMemoryUtilization requires spec.resources.gpu")
		}
	} else {
		if deployment.Spec.Resources.GPU.Count < 1 {
			return errors.New("spec.resources.gpu.count must be at least 1")
		}
		if deployment.Spec.Runtime.TensorParallelSize > deployment.Spec.Resources.GPU.Count {
			return fmt.Errorf("spec.runtime.tensorParallelSize (%d) must not exceed spec.resources.gpu.count (%d)",
				deployment.Spec.Runtime.TensorParallelSize, deployment.Spec.Resources.GPU.Count)
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
		return errors.New("spec.scaling.minReplicas must not be negative")
	}
	if deployment.Spec.Scaling.MaxReplicas < deployment.Spec.Scaling.MinReplicas {
		return errors.New("spec.scaling.maxReplicas must be greater than or equal to minReplicas")
	}
	return nil
}

// ValidateModelRuntime validates a runtime definition.
func ValidateModelRuntime(runtime v1alpha1.ModelRuntime) error {
	if runtime.Name == "" {
		return errors.New("metadata.name is required")
	}
	if runtime.Spec.Engine == "" {
		return errors.New("spec.engine is required")
	}
	if runtime.Spec.Protocol == "" {
		return errors.New("spec.protocol is required")
	}
	if runtime.Spec.DefaultImage == "" {
		return errors.New("spec.defaultImage is required")
	}
	if runtime.Spec.Port < 1 || runtime.Spec.Port > 65535 {
		return errors.New("spec.port must be between 1 and 65535")
	}
	if runtime.Spec.HealthPath == "" {
		return errors.New("spec.healthPath is required")
	}
	return nil
}

// ValidateModelCache validates a cache definition.
func ValidateModelCache(cache v1alpha1.ModelCache) error {
	if cache.Name == "" {
		return errors.New("metadata.name is required")
	}
	if cache.Spec.ModelRepo == "" {
		return errors.New("spec.modelRepo is required")
	}
	if cache.Spec.Storage.Type == "" {
		return errors.New("spec.storage.type is required")
	}
	if cache.Spec.Storage.Size == "" {
		return errors.New("spec.storage.size is required")
	}
	if cache.Spec.Storage.Path == "" {
		return errors.New("spec.storage.path is required")
	}
	return nil
}
