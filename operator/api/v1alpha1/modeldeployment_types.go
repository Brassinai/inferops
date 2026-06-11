package v1alpha1

// ModelDeployment describes a desired inference runtime model endpoint.
type ModelDeployment struct {
	APIVersion string                `json:"apiVersion,omitempty"`
	Kind       string                `json:"kind,omitempty"`
	Metadata   ObjectMeta            `json:"metadata,omitempty"`
	Spec       ModelDeploymentSpec   `json:"spec,omitempty"`
	Status     ModelDeploymentStatus `json:"status,omitempty"`
}

// ModelDeploymentSpec contains user-configurable deployment settings.
type ModelDeploymentSpec struct {
	Model      ModelSpec            `json:"model,omitempty"`
	Runtime    RuntimeSpec          `json:"runtime,omitempty"`
	Resources  ResourceRequirements `json:"resources,omitempty"`
	Activation ActivationSpec       `json:"activation,omitempty"`
	Scaling    ScalingSpec          `json:"scaling,omitempty"`
	Routing    RoutingSpec          `json:"routing,omitempty"`
	Cache      CacheSpec            `json:"cache,omitempty"`
	Secrets    SecretReferences     `json:"secrets,omitempty"`
}

// ModelDeploymentStatus reports observed deployment state.
type ModelDeploymentStatus struct {
	ObservedGeneration int64                `json:"observedGeneration,omitempty"`
	Phase              ModelDeploymentPhase `json:"phase,omitempty"`
	Endpoint           string               `json:"endpoint,omitempty"`
	ServiceName        string               `json:"serviceName,omitempty"`
	AssignedNode       string               `json:"assignedNode,omitempty"`
	AssignedGPUs       []string             `json:"assignedGPUs,omitempty"`
	Cache              ModelCacheSummary    `json:"cache,omitempty"`
	Replicas           ReplicaStatus        `json:"replicas,omitempty"`
	Model              ModelStatus          `json:"model,omitempty"`
	Conditions         []Condition          `json:"conditions,omitempty"`
}

// ModelDeploymentPhase is the observed lifecycle phase of a model deployment.
type ModelDeploymentPhase string

const (
	ModelDeploymentPhasePending       ModelDeploymentPhase = "Pending"
	ModelDeploymentPhaseDownloading   ModelDeploymentPhase = "Downloading"
	ModelDeploymentPhaseCached        ModelDeploymentPhase = "Cached"
	ModelDeploymentPhaseWaitingForGPU ModelDeploymentPhase = "WaitingForGPU"
	ModelDeploymentPhaseActivating    ModelDeploymentPhase = "Activating"
	ModelDeploymentPhaseActive        ModelDeploymentPhase = "Active"
	ModelDeploymentPhaseDraining      ModelDeploymentPhase = "Draining"
	ModelDeploymentPhaseDeactivating  ModelDeploymentPhase = "Deactivating"
	ModelDeploymentPhaseFailed        ModelDeploymentPhase = "Failed"
)

// ModelSpec identifies the model artifact to cache and serve.
type ModelSpec struct {
	Name     string `json:"name,omitempty"`
	Source   string `json:"source,omitempty"`
	Repo     string `json:"repo,omitempty"`
	Revision string `json:"revision,omitempty"`
}

// RuntimeSpec selects a ModelRuntime and supplies common inference overrides.
type RuntimeSpec struct {
	Ref                  string  `json:"ref,omitempty"`
	Image                string  `json:"image,omitempty"`
	DType                string  `json:"dtype,omitempty"`
	MaxModelLen          int32   `json:"maxModelLen,omitempty"`
	TensorParallelSize   int32   `json:"tensorParallelSize,omitempty"`
	GPUMemoryUtilization float64 `json:"gpuMemoryUtilization,omitempty"`
}

// ResourceRequirements captures compute requirements for inference workloads.
type ResourceRequirements struct {
	CPU    string             `json:"cpu,omitempty"`
	Memory string             `json:"memory,omitempty"`
	GPU    GPUResourceRequest `json:"gpu,omitempty"`
}

// GPUResourceRequest requests whole GPU devices from a vendor resource.
type GPUResourceRequest struct {
	Count  int32  `json:"count,omitempty"`
	Vendor string `json:"vendor,omitempty"`
	Type   string `json:"type,omitempty"`
}

// ActivationSpec controls whether and how a deployment acquires GPU capacity.
type ActivationSpec struct {
	DesiredState ActivationDesiredState `json:"desiredState,omitempty"`
	WhenFull     ActivationWhenFull     `json:"whenFull,omitempty"`
	Priority     int32                  `json:"priority,omitempty"`
	DrainTimeout string                 `json:"drainTimeout,omitempty"`
}

// ActivationDesiredState is the requested runtime activation state.
type ActivationDesiredState string

const (
	ActivationDesiredStateInactive ActivationDesiredState = "Inactive"
	ActivationDesiredStateActive   ActivationDesiredState = "Active"
)

// ActivationWhenFull defines behavior when compatible GPU capacity is full.
type ActivationWhenFull string

const (
	ActivationWhenFullQueue                 ActivationWhenFull = "Queue"
	ActivationWhenFullReject                ActivationWhenFull = "Reject"
	ActivationWhenFullReplaceOldest         ActivationWhenFull = "ReplaceOldest"
	ActivationWhenFullReplaceLowestPriority ActivationWhenFull = "ReplaceLowestPriority"
)

// ScalingSpec defines explicit replica bounds for a deployment.
type ScalingSpec struct {
	MinReplicas int32 `json:"minReplicas,omitempty"`
	MaxReplicas int32 `json:"maxReplicas,omitempty"`
}

// RoutingSpec controls exposure through the InferOps gateway.
type RoutingSpec struct {
	Enabled          bool   `json:"enabled,omitempty"`
	Path             string `json:"path,omitempty"`
	OpenAICompatible bool   `json:"openAICompatible,omitempty"`
}

// CacheSpec requests a persistent model artifact cache.
type CacheSpec struct {
	Enabled bool   `json:"enabled,omitempty"`
	Type    string `json:"type,omitempty"`
	Size    string `json:"size,omitempty"`
	Path    string `json:"path,omitempty"`
}

// SecretReferences names Secrets used to access external model sources.
type SecretReferences struct {
	HuggingFaceTokenSecretName string `json:"huggingFaceTokenSecretName,omitempty"`
}

// ModelCacheSummary reports the cache selected for a deployment.
type ModelCacheSummary struct {
	State    string `json:"state,omitempty"`
	NodeName string `json:"nodeName,omitempty"`
	Path     string `json:"path,omitempty"`
}

// ReplicaStatus reports desired and ready runtime replicas.
type ReplicaStatus struct {
	Desired int32 `json:"desired,omitempty"`
	Ready   int32 `json:"ready,omitempty"`
}

// ModelStatus reports whether the model is loaded by the runtime.
type ModelStatus struct {
	Loaded bool   `json:"loaded,omitempty"`
	Repo   string `json:"repo,omitempty"`
}

// ObjectMeta is a lightweight placeholder for Kubernetes object metadata.
type ObjectMeta struct {
	Name        string            `json:"name,omitempty"`
	Namespace   string            `json:"namespace,omitempty"`
	Generation  int64             `json:"generation,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Condition describes observed state for a custom resource.
type Condition struct {
	Type               string `json:"type,omitempty"`
	Status             string `json:"status,omitempty"`
	ObservedGeneration int64  `json:"observedGeneration,omitempty"`
	LastTransitionTime string `json:"lastTransitionTime,omitempty"`
	Reason             string `json:"reason,omitempty"`
	Message            string `json:"message,omitempty"`
}
