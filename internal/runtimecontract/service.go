// Package runtimecontract defines the stable Kubernetes Service contract shared
// by the operator and gateway.
package runtimecontract

const (
	// ServiceSuffix is appended to a ModelDeployment name for its runtime Service.
	ServiceSuffix = "-runtime"
	// HTTPPortName is the required runtime Service port name.
	HTTPPortName = "http"
	// ModelDeploymentLabel identifies the ModelDeployment owning selected pods.
	ModelDeploymentLabel = "inferops.dev/modeldeployment"
	// ModelDeploymentAPIGroup is the Kubernetes API group for InferOps ModelDeployment resources.
	ModelDeploymentAPIGroup = "inference.inferops.dev"
	// ModelDeploymentAPIVersion is the current Kubernetes API version for ModelDeployment resources.
	ModelDeploymentAPIVersion = ModelDeploymentAPIGroup + "/v1alpha1"
	// ModelDeploymentKind is the Kubernetes kind for InferOps model deployments.
	ModelDeploymentKind = "ModelDeployment"
	// LabelPartOf identifies InferOps-managed workload resources.
	LabelPartOf = "app.kubernetes.io/part-of"
	// LabelManagedBy identifies the InferOps controller managing runtime pods.
	LabelManagedBy = "app.kubernetes.io/managed-by"
	// LabelComponent identifies the InferOps runtime component on workload pods.
	LabelComponent = "app.kubernetes.io/component"
	// ValuePartOf is the required InferOps workload family label value.
	ValuePartOf = "inferops"
	// ValueManagedBy is the required InferOps controller label value.
	ValueManagedBy = "inferops-operator"
	// ValueRuntimeComponent is the required runtime pod component label value.
	ValueRuntimeComponent = "model-runtime"
)

// ServiceName returns the stable runtime Service name for a ModelDeployment.
func ServiceName(modelDeploymentName string) string {
	return modelDeploymentName + ServiceSuffix
}
