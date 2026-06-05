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
	StorageClassName string `json:"storageClassName,omitempty"`
	Size             string `json:"size,omitempty"`
	AccessMode       string `json:"accessMode,omitempty"`
}

// ModelCacheStatus reports cache readiness.
type ModelCacheStatus struct {
	ClaimName  string      `json:"claimName,omitempty"`
	Conditions []Condition `json:"conditions,omitempty"`
}
