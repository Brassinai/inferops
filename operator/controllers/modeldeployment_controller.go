package controllers

import (
	"context"
	"fmt"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
)

// ModelDeploymentReconciler owns reconciliation for ModelDeployment resources.
type ModelDeploymentReconciler struct{}

// Reconcile validates the placeholder resource and reserves the future controller boundary.
func (r *ModelDeploymentReconciler) Reconcile(ctx context.Context, deployment v1alpha1.ModelDeployment) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deployment.Name == "" {
		return fmt.Errorf("modeldeployment name is required")
	}
	return nil
}
