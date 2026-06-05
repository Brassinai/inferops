package controllers

import (
	"context"
	"fmt"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
)

// ModelCacheReconciler owns reconciliation for ModelCache resources.
type ModelCacheReconciler struct{}

// Reconcile validates the placeholder resource and reserves the future controller boundary.
func (r *ModelCacheReconciler) Reconcile(ctx context.Context, cache v1alpha1.ModelCache) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cache.Metadata.Name == "" {
		return fmt.Errorf("modelcache name is required")
	}
	return nil
}
