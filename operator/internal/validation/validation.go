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
	if deployment.Spec.Model == "" {
		return fmt.Errorf("spec.model is required")
	}
	return nil
}
