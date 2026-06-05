package v1alpha1

// ModelRuntime describes a reusable nano-vLLM runtime configuration.
type ModelRuntime struct {
	APIVersion string             `json:"apiVersion,omitempty"`
	Kind       string             `json:"kind,omitempty"`
	Metadata   ObjectMeta         `json:"metadata,omitempty"`
	Spec       ModelRuntimeSpec   `json:"spec,omitempty"`
	Status     ModelRuntimeStatus `json:"status,omitempty"`
}

// ModelRuntimeSpec contains image and runtime-level configuration.
type ModelRuntimeSpec struct {
	Image       string            `json:"image,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Resources   ResourceRequirements
	Command     []string `json:"command,omitempty"`
	Args        []string `json:"args,omitempty"`
	ServicePort int32    `json:"servicePort,omitempty"`
}

// ModelRuntimeStatus reports runtime availability.
type ModelRuntimeStatus struct {
	Conditions []Condition `json:"conditions,omitempty"`
}
