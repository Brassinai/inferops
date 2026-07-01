package resources

import (
	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	"github.com/brassinai/inferops/operator/internal/templates"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Well-known label keys used across all managed resources.
const (
	LabelPartOf          = "app.kubernetes.io/part-of"
	LabelName            = "app.kubernetes.io/name"
	LabelManagedBy       = "app.kubernetes.io/managed-by"
	LabelComponent       = "app.kubernetes.io/component"
	LabelModelDeployment = "inferops.dev/modeldeployment"
	LabelModelCache      = "inferops.dev/modelcache"
)

// Common label values.
const (
	ValuePartOf    = "inferops"
	ValueManagedBy = "inferops-operator"
	ValueComponent = "model-runtime"
)

// BaseLabels returns the shared label set for a ModelDeployment's managed resources.
func BaseLabels(modelDeploymentName string) map[string]string {
	return map[string]string{
		LabelPartOf:          ValuePartOf,
		LabelName:            templates.RuntimeServiceName(modelDeploymentName),
		LabelManagedBy:       ValueManagedBy,
		LabelComponent:       ValueComponent,
		LabelModelDeployment: modelDeploymentName,
	}
}

// SelectorLabels returns the minimal selector labels for pods and services.
func SelectorLabels(modelDeploymentName string) map[string]string {
	return map[string]string{
		LabelName:            templates.RuntimeServiceName(modelDeploymentName),
		LabelModelDeployment: modelDeploymentName,
	}
}

// OwnerReferenceForModelDeployment returns an owner reference pointing to the ModelDeployment.
func OwnerReferenceForModelDeployment(md *v1alpha1.ModelDeployment) metav1.OwnerReference {
	return *metav1.NewControllerRef(md, v1alpha1.GroupVersion.WithKind("ModelDeployment"))
}

// CacheLabels returns labels for resources managed on behalf of a ModelCache.
func CacheLabels(modelCacheName string) map[string]string {
	return map[string]string{
		LabelPartOf:     ValuePartOf,
		LabelName:       modelCacheName,
		LabelManagedBy:  ValueManagedBy,
		LabelComponent:  "model-cache",
		LabelModelCache: modelCacheName,
	}
}

// OwnerReferenceForModelCache returns an owner reference pointing to the ModelCache.
func OwnerReferenceForModelCache(cache *v1alpha1.ModelCache) metav1.OwnerReference {
	return *metav1.NewControllerRef(cache, v1alpha1.GroupVersion.WithKind("ModelCache"))
}
