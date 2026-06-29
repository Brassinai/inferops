// Package v1alpha1 contains API Schema definitions for the inference v1alpha1 API group.
// +kubebuilder:object:generate=true
// +groupName=inference.inferops.dev
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is group version used to register these objects.
	GroupVersion = schema.GroupVersion{Group: "inference.inferops.dev", Version: "v1alpha1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

// addKnownTypes adds the resource types in this API group to the scheme.
func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion,
		&ModelDeployment{},
		&ModelDeploymentList{},
		&ModelRuntime{},
		&ModelRuntimeList{},
		&ModelCache{},
		&ModelCacheList{},
	)
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
