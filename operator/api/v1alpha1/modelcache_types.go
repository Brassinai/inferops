package v1alpha1

// ModelCache describes persistent model cache configuration.
type ModelCache struct {
	APIVersion string           `json:"apiVersion,omitempty"`
	Kind       string           `json:"kind,omitempty"`
	Metadata   ObjectMeta       `json:"metadata,omitempty"`
	Spec       ModelCacheSpec   `json:"spec,omitempty"`
	Status     ModelCacheStatus `json:"status,omitempty"`
}

// ModelCacheSpec contains storage settings for downloaded model artifacts.
type ModelCacheSpec struct {
	ModelRepo string            `json:"modelRepo,omitempty"`
	Revision  string            `json:"revision,omitempty"`
	Storage   ModelCacheStorage `json:"storage,omitempty"`
	SecretRef string            `json:"secretRef,omitempty"`
}

// ModelCacheStatus reports cache readiness.
type ModelCacheStatus struct {
	ObservedGeneration int64           `json:"observedGeneration,omitempty"`
	Phase              ModelCachePhase `json:"phase,omitempty"`
	Revision           string          `json:"revision,omitempty"`
	Checksum           string          `json:"checksum,omitempty"`
	NodeName           string          `json:"nodeName,omitempty"`
	Path               string          `json:"path,omitempty"`
	Size               string          `json:"size,omitempty"`
	LastUsedTime       string          `json:"lastUsedTime,omitempty"`
	Conditions         []Condition     `json:"conditions,omitempty"`
}

// ModelCacheStorage describes where model artifacts are persisted.
type ModelCacheStorage struct {
	Type     string `json:"type,omitempty"`
	Size     string `json:"size,omitempty"`
	NodeName string `json:"nodeName,omitempty"`
	Path     string `json:"path,omitempty"`
}

// ModelCachePhase is the observed lifecycle phase of a model cache.
type ModelCachePhase string

const (
	ModelCachePhasePending     ModelCachePhase = "Pending"
	ModelCachePhaseDownloading ModelCachePhase = "Downloading"
	ModelCachePhaseReady       ModelCachePhase = "Ready"
	ModelCachePhaseFailed      ModelCachePhase = "Failed"
)
