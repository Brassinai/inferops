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

// ModelCacheSpec contains storage settings for downloaded model artifacts.
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
	Path               string          `json:"path,omitempty"`
	Size               string          `json:"size,omitempty"`
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
