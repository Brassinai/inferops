package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// ModelRuntime describes a reusable inference runtime configuration.
// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=mruntime
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Engine",type=string,JSONPath=`.spec.engine`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
type ModelRuntime struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ModelRuntimeSpec   `json:"spec,omitempty"`
	Status ModelRuntimeStatus `json:"status,omitempty"`
}

// ModelRuntimeSpec contains image and runtime-level configuration.
type ModelRuntimeSpec struct {
	// +kubebuilder:validation:Required
	Engine string `json:"engine"`
	// +kubebuilder:validation:Required
	Protocol string `json:"protocol"`
	// +kubebuilder:validation:Required
	DefaultImage string `json:"defaultImage"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +kubebuilder:validation:Required
	Port int32 `json:"port"`
	// +kubebuilder:validation:Required
	HealthPath    string            `json:"healthPath"`
	ReadinessPath string            `json:"readinessPath,omitempty"`
	MetricsPath   string            `json:"metricsPath,omitempty"`
	Command       []string          `json:"command,omitempty"`
	Args          []string          `json:"args,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
}

// ModelRuntimeStatus reports runtime availability.
type ModelRuntimeStatus struct {
	ObservedGeneration int64             `json:"observedGeneration,omitempty"`
	Phase              ModelRuntimePhase `json:"phase,omitempty"`
	Conditions         []Condition       `json:"conditions,omitempty"`
}

// ModelRuntimePhase is the observed availability of a runtime definition.
// +kubebuilder:validation:Enum=Pending;Ready;Unavailable;Failed
type ModelRuntimePhase string

const (
	ModelRuntimePhasePending     ModelRuntimePhase = "Pending"
	ModelRuntimePhaseReady       ModelRuntimePhase = "Ready"
	ModelRuntimePhaseUnavailable ModelRuntimePhase = "Unavailable"
	ModelRuntimePhaseFailed      ModelRuntimePhase = "Failed"
)

// ModelRuntimeList contains a list of ModelRuntime.
type ModelRuntimeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ModelRuntime `json:"items"`
}

// DeepCopyInto copies the receiver, writing into out.
func (in *ModelRuntime) DeepCopyInto(out *ModelRuntime) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy creates a copy.
func (in *ModelRuntime) DeepCopy() *ModelRuntime {
	if in == nil {
		return nil
	}
	out := new(ModelRuntime)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject returns a runtime.Object.
func (in *ModelRuntime) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies the receiver, writing into out.
func (in *ModelRuntimeList) DeepCopyInto(out *ModelRuntimeList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]ModelRuntime, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy creates a copy.
func (in *ModelRuntimeList) DeepCopy() *ModelRuntimeList {
	if in == nil {
		return nil
	}
	out := new(ModelRuntimeList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject returns a runtime.Object.
func (in *ModelRuntimeList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies the receiver, writing into out.
func (in *ModelRuntimeSpec) DeepCopyInto(out *ModelRuntimeSpec) {
	*out = *in
	if in.Command != nil {
		out.Command = make([]string, len(in.Command))
		copy(out.Command, in.Command)
	}
	if in.Args != nil {
		out.Args = make([]string, len(in.Args))
		copy(out.Args, in.Args)
	}
	if in.Env != nil {
		out.Env = make(map[string]string, len(in.Env))
		for key, val := range in.Env {
			out.Env[key] = val
		}
	}
}

// DeepCopy creates a copy.
func (in *ModelRuntimeSpec) DeepCopy() *ModelRuntimeSpec {
	if in == nil {
		return nil
	}
	out := new(ModelRuntimeSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies the receiver, writing into out.
func (in *ModelRuntimeStatus) DeepCopyInto(out *ModelRuntimeStatus) {
	*out = *in
	if in.Conditions != nil {
		out.Conditions = make([]Condition, len(in.Conditions))
		for i := range in.Conditions {
			in.Conditions[i].DeepCopyInto(&out.Conditions[i])
		}
	}
}

// DeepCopy creates a copy.
func (in *ModelRuntimeStatus) DeepCopy() *ModelRuntimeStatus {
	if in == nil {
		return nil
	}
	out := new(ModelRuntimeStatus)
	in.DeepCopyInto(out)
	return out
}
