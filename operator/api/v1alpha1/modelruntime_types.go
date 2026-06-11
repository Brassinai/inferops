package v1alpha1

// ModelRuntime describes a reusable inference runtime configuration.
type ModelRuntime struct {
	APIVersion string             `json:"apiVersion,omitempty"`
	Kind       string             `json:"kind,omitempty"`
	Metadata   ObjectMeta         `json:"metadata,omitempty"`
	Spec       ModelRuntimeSpec   `json:"spec,omitempty"`
	Status     ModelRuntimeStatus `json:"status,omitempty"`
}

// ModelRuntimeSpec contains image and runtime-level configuration.
type ModelRuntimeSpec struct {
	Engine       string            `json:"engine,omitempty"`
	Protocol     string            `json:"protocol,omitempty"`
	DefaultImage string            `json:"defaultImage,omitempty"`
	Port         int32             `json:"port,omitempty"`
	HealthPath   string            `json:"healthPath,omitempty"`
	MetricsPath  string            `json:"metricsPath,omitempty"`
	Command      []string          `json:"command,omitempty"`
	Args         []string          `json:"args,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
}

// ModelRuntimeStatus reports runtime availability.
type ModelRuntimeStatus struct {
	ObservedGeneration int64             `json:"observedGeneration,omitempty"`
	Phase              ModelRuntimePhase `json:"phase,omitempty"`
	Conditions         []Condition       `json:"conditions,omitempty"`
}

// ModelRuntimePhase is the observed availability of a runtime definition.
type ModelRuntimePhase string

const (
	ModelRuntimePhasePending     ModelRuntimePhase = "Pending"
	ModelRuntimePhaseReady       ModelRuntimePhase = "Ready"
	ModelRuntimePhaseUnavailable ModelRuntimePhase = "Unavailable"
	ModelRuntimePhaseFailed      ModelRuntimePhase = "Failed"
)
