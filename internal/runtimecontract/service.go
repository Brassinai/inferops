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
)

// ServiceName returns the stable runtime Service name for a ModelDeployment.
func ServiceName(modelDeploymentName string) string {
	return modelDeploymentName + ServiceSuffix
}
