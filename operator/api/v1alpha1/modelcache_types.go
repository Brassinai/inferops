package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// ModelCache describes persistent model cache configuration.
// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=mcache
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Node",type=string,JSONPath=`.status.nodeName`
// +kubebuilder:printcolumn:name="Size",type=string,JSONPath=`.status.size`
type ModelCache struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ModelCacheSpec   `json:"spec,omitempty"`
	Status ModelCacheStatus `json:"status,omitempty"`
}

// ModelCacheSpec contains source and destination settings for downloaded model artifacts.
type ModelCacheSpec struct {
	// +kubebuilder:validation:Required
	ModelRepo string `json:"modelRepo"`
	Revision  string `json:"revision,omitempty"`
	// +kubebuilder:validation:Required
	Storage   ModelCacheStorage `json:"storage"`
	SecretRef string            `json:"secretRef,omitempty"`
}

// ModelCacheStatus reports cache readiness.
type ModelCacheStatus struct {
	ObservedGeneration int64           `json:"observedGeneration,omitempty"`
	Phase              ModelCachePhase `json:"phase,omitempty"`
	Revision           string          `json:"revision,omitempty"`
	Checksum           string          `json:"checksum,omitempty"`
	NodeName           string          `json:"nodeName,omitempty"`
	NodeUID            string          `json:"nodeUID,omitempty"`
	Path               string          `json:"path,omitempty"`
	Size               string          `json:"size,omitempty"`
	ReservedSize       string          `json:"reservedSize,omitempty"`
	InputHash          string          `json:"inputHash,omitempty"`
	LastUsedTime       metav1.Time     `json:"lastUsedTime,omitempty"`
	Conditions         []Condition     `json:"conditions,omitempty"`
}

// ModelCacheStorage describes where model artifacts are persisted.
type ModelCacheStorage struct {
	// +kubebuilder:validation:Required
	Type string `json:"type"`
	// +kubebuilder:validation:Required
	Size     string `json:"size"`
	NodeName string `json:"nodeName,omitempty"`
	// NodeSelector and Tolerations keep node-local cache download jobs
	// schedulable on the same constrained nodes as their runtime workloads.
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	Tolerations  []Toleration      `json:"tolerations,omitempty"`
	// +kubebuilder:validation:Pattern=^/
	// +kubebuilder:validation:Required
	Path string `json:"path"`
}

// ModelCachePhase is the observed lifecycle phase of a model cache.
// +kubebuilder:validation:Enum=Pending;Downloading;Ready;Failed
type ModelCachePhase string

const (
	ModelCachePhasePending     ModelCachePhase = "Pending"
	ModelCachePhaseDownloading ModelCachePhase = "Downloading"
	ModelCachePhaseReady       ModelCachePhase = "Ready"
	ModelCachePhaseFailed      ModelCachePhase = "Failed"
)

// Well-known condition types for ModelCache status.
const (
	// CacheConditionSpecValid indicates that the cache spec passed static and
	// reconciliation-time validation.
	CacheConditionSpecValid = "SpecValid"
	// CacheConditionPlaced indicates that a suitable destination node or volume
	// was selected.
	CacheConditionPlaced = "Placed"
	// CacheConditionDownloaded indicates that the download Job finished
	// successfully.
	CacheConditionDownloaded = "Downloaded"
	// CacheConditionVerified indicates that the downloaded artifact passed
	// integrity verification.
	CacheConditionVerified = "Verified"
	// CacheConditionReady aggregates overall cache readiness.
	CacheConditionReady = "Ready"
)

// Stable condition/Event reason codes for ModelCache reconciliation.
const (
	CacheReasonSpecValidated         = "SpecValidated"
	CacheReasonSpecInvalid           = "SpecInvalid"
	CacheReasonPlaced                = "Placed"
	CacheReasonNoEligibleNode        = "NoEligibleNode"
	CacheReasonPinnedNodeUnavailable = "PinnedNodeUnavailable"
	CacheReasonDownloadRunning       = "DownloadRunning"
	CacheReasonDownloadSucceeded     = "DownloadSucceeded"
	CacheReasonDownloadFailed        = "DownloadFailed"
	CacheReasonVerified              = "Verified"
	CacheReasonCacheReady            = "CacheReady"
	CacheReasonCacheFailed           = "CacheFailed"
	CacheReasonInsufficientCapacity  = "InsufficientCapacity"
	CacheReasonPathConflict          = "PathConflict"
	CacheReasonNodeLost              = "NodeLost"
	CacheReasonIdentityChanged       = "CacheIdentityChanged"
	CacheReasonSecretNotFound        = "SecretNotFound"
	CacheReasonSecretKeyMissing      = "SecretKeyMissing"
)

// ModelCacheList contains a list of ModelCache.
type ModelCacheList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ModelCache `json:"items"`
}

// DeepCopyInto copies the receiver, writing into out.
func (in *ModelCache) DeepCopyInto(out *ModelCache) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy creates a copy.
func (in *ModelCache) DeepCopy() *ModelCache {
	if in == nil {
		return nil
	}
	out := new(ModelCache)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject returns a runtime.Object.
func (in *ModelCache) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies the receiver, writing into out.
func (in *ModelCacheList) DeepCopyInto(out *ModelCacheList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]ModelCache, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy creates a copy.
func (in *ModelCacheList) DeepCopy() *ModelCacheList {
	if in == nil {
		return nil
	}
	out := new(ModelCacheList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject returns a runtime.Object.
func (in *ModelCacheList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies the receiver, writing into out.
func (in *ModelCacheSpec) DeepCopyInto(out *ModelCacheSpec) {
	*out = *in
	in.Storage.DeepCopyInto(&out.Storage)
}

// DeepCopy creates a copy.
func (in *ModelCacheSpec) DeepCopy() *ModelCacheSpec {
	if in == nil {
		return nil
	}
	out := new(ModelCacheSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies the receiver, writing into out.
func (in *ModelCacheStatus) DeepCopyInto(out *ModelCacheStatus) {
	*out = *in
	in.LastUsedTime.DeepCopyInto(&out.LastUsedTime)
	if in.Conditions != nil {
		out.Conditions = make([]Condition, len(in.Conditions))
		for i := range in.Conditions {
			in.Conditions[i].DeepCopyInto(&out.Conditions[i])
		}
	}
}

// DeepCopy creates a copy.
func (in *ModelCacheStatus) DeepCopy() *ModelCacheStatus {
	if in == nil {
		return nil
	}
	out := new(ModelCacheStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies the receiver, writing into out.
func (in *ModelCacheStorage) DeepCopyInto(out *ModelCacheStorage) {
	*out = *in
	if in.NodeSelector != nil {
		out.NodeSelector = make(map[string]string, len(in.NodeSelector))
		for key, value := range in.NodeSelector {
			out.NodeSelector[key] = value
		}
	}
	if in.Tolerations != nil {
		out.Tolerations = make([]Toleration, len(in.Tolerations))
		for i := range in.Tolerations {
			out.Tolerations[i] = in.Tolerations[i]
			if in.Tolerations[i].TolerationSeconds != nil {
				value := *in.Tolerations[i].TolerationSeconds
				out.Tolerations[i].TolerationSeconds = &value
			}
		}
	}
}

// DeepCopy creates a copy.
func (in *ModelCacheStorage) DeepCopy() *ModelCacheStorage {
	if in == nil {
		return nil
	}
	out := new(ModelCacheStorage)
	in.DeepCopyInto(out)
	return out
}
