package v1alpha1

// ModelDeployment describes a desired nano-vLLM model endpoint.
type ModelDeployment struct {
	APIVersion string                `json:"apiVersion,omitempty"`
	Kind       string                `json:"kind,omitempty"`
	Metadata   ObjectMeta            `json:"metadata,omitempty"`
	Spec       ModelDeploymentSpec   `json:"spec,omitempty"`
	Status     ModelDeploymentStatus `json:"status,omitempty"`
}

// ModelDeploymentSpec contains user-configurable deployment settings.
type ModelDeploymentSpec struct {
	Model      string               `json:"model,omitempty"`
	RuntimeRef string               `json:"runtimeRef,omitempty"`
	Replicas   *int32               `json:"replicas,omitempty"`
	Resources  ResourceRequirements `json:"resources,omitempty"`
}

// ModelDeploymentStatus reports observed deployment state.
type ModelDeploymentStatus struct {
	Endpoint   string      `json:"endpoint,omitempty"`
	Conditions []Condition `json:"conditions,omitempty"`
}

// ResourceRequirements captures compute requirements for inference workloads.
type ResourceRequirements struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
	GPU    string `json:"gpu,omitempty"`
}

// ObjectMeta is a lightweight placeholder for Kubernetes object metadata.
type ObjectMeta struct {
	Name        string            `json:"name,omitempty"`
	Namespace   string            `json:"namespace,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Condition describes observed state for a custom resource.
type Condition struct {
	Type    string `json:"type,omitempty"`
	Status  string `json:"status,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}
