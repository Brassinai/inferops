package controllers

import (
	"context"
	"fmt"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
)

// ModelRuntimeReconciler owns reconciliation for ModelRuntime resources.
type ModelRuntimeReconciler struct{}

// Reconcile validates the placeholder resource and reserves the future controller boundary.
func (r *ModelRuntimeReconciler) Reconcile(ctx context.Context, runtime v1alpha1.ModelRuntime) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if runtime.Metadata.Name == "" {
		return fmt.Errorf("modelruntime name is required")
	}
	return nil
}
