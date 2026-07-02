package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// ModelDeployment describes a desired inference runtime model endpoint.
// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=mdeploy
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Desired",type=string,JSONPath=`.spec.activation.desiredState`
// +kubebuilder:printcolumn:name="Endpoint",type=string,JSONPath=`.status.endpoint`
type ModelDeployment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ModelDeploymentSpec   `json:"spec,omitempty"`
	Status ModelDeploymentStatus `json:"status,omitempty"`
}

// ModelDeploymentSpec contains user-configurable deployment settings.
// +kubebuilder:validation:XValidation:rule="has(self.resources) && (has(self.resources.gpu) || (has(self.resources.cpu) && has(self.resources.memory)))",message="CPU-only deployments must specify resources.cpu and resources.memory"
// +kubebuilder:validation:XValidation:rule="(has(self.resources) && has(self.resources.gpu)) || (!has(self.runtime.tensorParallelSize) && !has(self.runtime.gpuMemoryUtilization))",message="tensorParallelSize and gpuMemoryUtilization require resources.gpu"
// +kubebuilder:validation:XValidation:rule="!has(self.runtime.tensorParallelSize) || (has(self.resources) && has(self.resources.gpu) && self.runtime.tensorParallelSize <= self.resources.gpu.count)",message="tensorParallelSize must not exceed resources.gpu.count"
type ModelDeploymentSpec struct {
	// +kubebuilder:validation:Required
	Model ModelSpec `json:"model"`
	// +kubebuilder:validation:Required
	Runtime    RuntimeSpec          `json:"runtime"`
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

// Well-known condition types for ModelDeployment status.
const (
	// ConditionSpecValid indicates that the deployment spec passed both static
	// and reconciliation-time validation.
	ConditionSpecValid = "SpecValid"
	// ConditionRuntimeResolved indicates that the referenced ModelRuntime was
	// found and an effective runtime configuration could be produced.
	ConditionRuntimeResolved = "RuntimeResolved"
	// ConditionSecretsReady indicates that required Secrets are referenced.
	ConditionSecretsReady = "SecretsReady"
	// ConditionCacheReady indicates that the model cache is ready for use.
	ConditionCacheReady = "CacheReady"
	// ConditionReady aggregates the overall readiness of the deployment.
	ConditionReady = "Ready"
)

// Stable condition/Event reason codes for ModelDeployment reconciliation.
const (
	ReasonSpecValidated       = "SpecValidated"
	ReasonSpecInvalid         = "SpecInvalid"
	ReasonRuntimeResolved     = "RuntimeResolved"
	ReasonRuntimeNotFound     = "RuntimeNotFound"
	ReasonSecretsAvailable    = "SecretsAvailable"
	ReasonSecretRequired      = "SecretRequired"
	ReasonInvalidCachePath    = "InvalidCachePath"
	ReasonInvalidDrainTimeout = "InvalidDrainTimeout"
)

// ModelDeploymentPhase is the observed lifecycle phase of a model deployment.
// +kubebuilder:validation:Enum=Pending;Downloading;Cached;WaitingForCapacity;WaitingForGPU;Activating;Active;Draining;Deactivating;Failed
type ModelDeploymentPhase string

const (
	ModelDeploymentPhasePending            ModelDeploymentPhase = "Pending"
	ModelDeploymentPhaseDownloading        ModelDeploymentPhase = "Downloading"
	ModelDeploymentPhaseCached             ModelDeploymentPhase = "Cached"
	ModelDeploymentPhaseWaitingForCapacity ModelDeploymentPhase = "WaitingForCapacity"
	ModelDeploymentPhaseWaitingForGPU      ModelDeploymentPhase = "WaitingForGPU"
	ModelDeploymentPhaseActivating         ModelDeploymentPhase = "Activating"
	ModelDeploymentPhaseActive             ModelDeploymentPhase = "Active"
	ModelDeploymentPhaseDraining           ModelDeploymentPhase = "Draining"
	ModelDeploymentPhaseDeactivating       ModelDeploymentPhase = "Deactivating"
	ModelDeploymentPhaseFailed             ModelDeploymentPhase = "Failed"
)

// ModelSpec identifies the model artifact to cache and serve.
type ModelSpec struct {
	Name   string `json:"name,omitempty"`
	Source string `json:"source,omitempty"`
	// +kubebuilder:validation:Required
	Repo     string `json:"repo"`
	Revision string `json:"revision,omitempty"`
}

// RuntimeSpec selects a ModelRuntime and supplies common inference overrides.
type RuntimeSpec struct {
	// +kubebuilder:validation:Required
	Ref   string `json:"ref"`
	Image string `json:"image,omitempty"`
	DType string `json:"dtype,omitempty"`
	// +kubebuilder:validation:Minimum=1
	MaxModelLen int32 `json:"maxModelLen,omitempty"`
	// +kubebuilder:validation:Minimum=1
	TensorParallelSize int32 `json:"tensorParallelSize,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	GPUMemoryUtilization float64 `json:"gpuMemoryUtilization,omitempty"`
}

// ResourceRequirements captures compute requirements for inference workloads.
type ResourceRequirements struct {
	CPU    string              `json:"cpu,omitempty"`
	Memory string              `json:"memory,omitempty"`
	GPU    *GPUResourceRequest `json:"gpu,omitempty"`
}

// GPUResourceRequest requests whole GPU devices from a vendor resource.
type GPUResourceRequest struct {
	// +kubebuilder:validation:Minimum=1
	Count  int32  `json:"count,omitempty"`
	Vendor string `json:"vendor,omitempty"`
	Type   string `json:"type,omitempty"`
}

// ActivationSpec controls whether and how a deployment acquires compute capacity.
type ActivationSpec struct {
	DesiredState ActivationDesiredState `json:"desiredState,omitempty"`
	WhenFull     ActivationWhenFull     `json:"whenFull,omitempty"`
	Priority     int32                  `json:"priority,omitempty"`
	DrainTimeout string                 `json:"drainTimeout,omitempty"`
}

// ActivationDesiredState is the requested runtime activation state.
// +kubebuilder:validation:Enum=Inactive;Active
type ActivationDesiredState string

const (
	ActivationDesiredStateInactive ActivationDesiredState = "Inactive"
	ActivationDesiredStateActive   ActivationDesiredState = "Active"
)

// ActivationWhenFull defines behavior when compatible compute capacity is full.
// +kubebuilder:validation:Enum=Queue;Reject;ReplaceOldest;ReplaceLowestPriority
type ActivationWhenFull string

const (
	ActivationWhenFullQueue                 ActivationWhenFull = "Queue"
	ActivationWhenFullReject                ActivationWhenFull = "Reject"
	ActivationWhenFullReplaceOldest         ActivationWhenFull = "ReplaceOldest"
	ActivationWhenFullReplaceLowestPriority ActivationWhenFull = "ReplaceLowestPriority"
)

// ScalingSpec defines explicit replica bounds for a deployment.
// +kubebuilder:validation:XValidation:rule="self.maxReplicas >= self.minReplicas",message="maxReplicas must be greater than or equal to minReplicas"
type ScalingSpec struct {
	MinReplicas int32 `json:"minReplicas,omitempty"`
	MaxReplicas int32 `json:"maxReplicas,omitempty"`
}

// RoutingSpec controls exposure through the InferOps gateway.
type RoutingSpec struct {
	Enabled bool `json:"enabled,omitempty"`
	// +kubebuilder:validation:Pattern=^/
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

// Condition describes observed state for a custom resource.
type Condition struct {
	// +kubebuilder:validation:Required
	Type string `json:"type"`
	// +kubebuilder:validation:Required
	Status             metav1.ConditionStatus `json:"status"`
	ObservedGeneration int64                  `json:"observedGeneration,omitempty"`
	LastTransitionTime metav1.Time            `json:"lastTransitionTime,omitempty"`
	Reason             string                 `json:"reason,omitempty"`
	Message            string                 `json:"message,omitempty"`
}

// ModelDeploymentList contains a list of ModelDeployment.
type ModelDeploymentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ModelDeployment `json:"items"`
}

// DeepCopyInto copies the receiver, writing into out.
func (in *ModelDeployment) DeepCopyInto(out *ModelDeployment) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy creates a copy.
func (in *ModelDeployment) DeepCopy() *ModelDeployment {
	if in == nil {
		return nil
	}
	out := new(ModelDeployment)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject returns a runtime.Object.
func (in *ModelDeployment) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies the receiver, writing into out.
func (in *ModelDeploymentList) DeepCopyInto(out *ModelDeploymentList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]ModelDeployment, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy creates a copy.
func (in *ModelDeploymentList) DeepCopy() *ModelDeploymentList {
	if in == nil {
		return nil
	}
	out := new(ModelDeploymentList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject returns a runtime.Object.
func (in *ModelDeploymentList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies the receiver, writing into out.
func (in *ModelDeploymentSpec) DeepCopyInto(out *ModelDeploymentSpec) {
	*out = *in
	if in.Resources.GPU != nil {
		out.Resources.GPU = new(GPUResourceRequest)
		*out.Resources.GPU = *in.Resources.GPU
	}
}

// DeepCopy creates a copy.
func (in *ModelDeploymentSpec) DeepCopy() *ModelDeploymentSpec {
	if in == nil {
		return nil
	}
	out := new(ModelDeploymentSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies the receiver, writing into out.
func (in *ModelDeploymentStatus) DeepCopyInto(out *ModelDeploymentStatus) {
	*out = *in
	if in.AssignedGPUs != nil {
		out.AssignedGPUs = make([]string, len(in.AssignedGPUs))
		copy(out.AssignedGPUs, in.AssignedGPUs)
	}
	if in.Conditions != nil {
		out.Conditions = make([]Condition, len(in.Conditions))
		for i := range in.Conditions {
			in.Conditions[i].DeepCopyInto(&out.Conditions[i])
		}
	}
}

// DeepCopy creates a copy.
func (in *ModelDeploymentStatus) DeepCopy() *ModelDeploymentStatus {
	if in == nil {
		return nil
	}
	out := new(ModelDeploymentStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies the receiver, writing into out.
func (in *Condition) DeepCopyInto(out *Condition) {
	*out = *in
	in.LastTransitionTime.DeepCopyInto(&out.LastTransitionTime)
}

// DeepCopy creates a copy.
func (in *Condition) DeepCopy() *Condition {
	if in == nil {
		return nil
	}
	out := new(Condition)
	in.DeepCopyInto(out)
	return out
}
