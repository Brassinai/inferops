package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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
	Runtime      RuntimeSpec          `json:"runtime"`
	Resources    ResourceRequirements `json:"resources,omitempty"`
	Activation   ActivationSpec       `json:"activation,omitempty"`
	Scaling      ScalingSpec          `json:"scaling,omitempty"`
	Routing      RoutingSpec          `json:"routing,omitempty"`
	Cache        CacheSpec            `json:"cache,omitempty"`
	Secrets      SecretReferences     `json:"secrets,omitempty"`
	Scheduling   SchedulingSpec       `json:"scheduling,omitempty"`
	Availability AvailabilitySpec     `json:"availability,omitempty"`
	Rollout      RolloutSpec          `json:"rollout,omitempty"`
}

// ModelDeploymentStatus reports observed deployment state.
type ModelDeploymentStatus struct {
	ObservedGeneration int64                `json:"observedGeneration,omitempty"`
	Phase              ModelDeploymentPhase `json:"phase,omitempty"`
	Endpoint           string               `json:"endpoint,omitempty"`
	ServiceName        string               `json:"serviceName,omitempty"`
	AssignedNode       string               `json:"assignedNode,omitempty"`
	AssignedGPUs       []string             `json:"assignedGPUs,omitempty"`
	DrainStartedAt     *metav1.Time         `json:"drainStartedAt,omitempty"`
	Replacement        *ReplacementStatus   `json:"replacement,omitempty"`
	Cache              ModelCacheSummary    `json:"cache,omitempty"`
	Replicas           ReplicaStatus        `json:"replicas,omitempty"`
	Scaling            ScalingStatus        `json:"scaling,omitempty"`
	Model              ModelStatus          `json:"model,omitempty"`
	Conditions         []Condition          `json:"conditions,omitempty"`
}

// ReplacementStatus records a durable, controller-owned single-GPU
// replacement transaction. Target is set on the incoming deployment and
// RequestedBy is set on the deployment whose runtime is being displaced.
type ReplacementStatus struct {
	Phase             ReplacementPhase      `json:"phase,omitempty"`
	RequestGeneration int64                 `json:"requestGeneration,omitempty"`
	Target            *ReplacementReference `json:"target,omitempty"`
	RequestedBy       *ReplacementReference `json:"requestedBy,omitempty"`
	StartedAt         *metav1.Time          `json:"startedAt,omitempty"`
	Message           string                `json:"message,omitempty"`
}

// ReplacementReference identifies one participant in a replacement
// transaction and protects the transaction from name reuse.
type ReplacementReference struct {
	Namespace string    `json:"namespace"`
	Name      string    `json:"name"`
	UID       types.UID `json:"uid"`
}

// ReplacementPhase is the observed state of a replacement transaction.
type ReplacementPhase string

const (
	ReplacementPhaseDraining       ReplacementPhase = "Draining"
	ReplacementPhaseActivating     ReplacementPhase = "Activating"
	ReplacementPhaseSucceeded      ReplacementPhase = "Succeeded"
	ReplacementPhaseDisplaced      ReplacementPhase = "Displaced"
	ReplacementPhaseRollingBack    ReplacementPhase = "RollingBack"
	ReplacementPhaseRolledBack     ReplacementPhase = "RolledBack"
	ReplacementPhaseRollbackFailed ReplacementPhase = "RollbackFailed"
)

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
	// ConditionGPUAssigned indicates that node-level whole-GPU capacity is
	// reserved. It does not claim a physical device UUID.
	ConditionGPUAssigned = "GPUAssigned"
	// ConditionRuntimeReady indicates that the managed runtime Deployment has
	// its desired number of ready replicas.
	ConditionRuntimeReady = "RuntimeReady"
	// ConditionModelLoaded mirrors runtime readiness for model consumers.
	ConditionModelLoaded = "ModelLoaded"
	// ConditionRoutingReady indicates whether the stable Service may receive
	// traffic from the gateway.
	ConditionRoutingReady = "RoutingReady"
	// ConditionReplacement reports an explicitly requested single-GPU
	// replacement and any rollback attempt.
	ConditionReplacement = "Replacement"
	// ConditionRollout reports whether an in-place runtime rollout can proceed
	// under the configured rollout strategy and currently visible capacity.
	ConditionRollout = "Rollout"
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
	ReasonCachePending        = "CachePending"
	ReasonCacheDownloading    = "CacheDownloading"
	ReasonCacheVerified       = "CacheVerified"
	ReasonCacheFailed         = "CacheFailed"
	ReasonInactive            = "Inactive"
	ReasonGPUCapacityReserved = "GPUCapacityReserved"
	ReasonWaitingForCapacity  = "WaitingForCapacity"
	ReasonWaitingForGPU       = "WaitingForGPU"
	ReasonInsufficientGPU     = "InsufficientGPUCapacity"
	ReasonInsufficientCompute = "InsufficientComputeCapacity"
	ReasonSchedulingBlocked   = "SchedulingConstraintsUnsatisfied"
	// ReasonReplacementPending is retained for clients compiled against the
	// pre-replacement API. The lifecycle controller no longer emits it.
	ReasonReplacementPending          = "ReplacementNotImplemented"
	ReasonDrainStarted                = "DrainStarted"
	ReasonDrainComplete               = "DrainComplete"
	ReasonDeactivationStarted         = "DeactivationStarted"
	ReasonReplacementStarted          = "ReplacementStarted"
	ReasonReplacementCanceled         = "ReplacementCanceled"
	ReasonReplacementDraining         = "ReplacementDraining"
	ReasonReplacementActivating       = "ReplacementActivating"
	ReasonReplacementSucceeded        = "ReplacementSucceeded"
	ReasonReplacementUnsupported      = "ReplacementUnsupported"
	ReasonReplacementNoCandidate      = "ReplacementNoCandidate"
	ReasonReplacementDisplaced        = "ReplacedByWorkload"
	ReasonReplacementActivationFailed = "ReplacementActivationFailed"
	ReasonReplacementRollbackStarted  = "ReplacementRollbackStarted"
	ReasonReplacementRolledBack       = "ReplacementRolledBack"
	ReasonReplacementRollbackFailed   = "ReplacementRollbackFailed"
	ReasonRuntimeCreating             = "RuntimeCreating"
	ReasonRuntimeReady                = "RuntimeReady"
	ReasonRuntimeUnavailable          = "RuntimeUnavailable"
	ReasonIdleScaledToZero            = "IdleScaledToZero"
	ReasonScalingMetricsUnavailable   = "ScalingMetricsUnavailable"
	ReasonScalingCapacityCapped       = "ScalingCapacityCapped"
	ReasonRolloutCapacityReserved     = "RolloutCapacityReserved"
	ReasonRolloutComplete             = "RolloutComplete"
	ReasonRolloutWaitingForCapacity   = "RolloutWaitingForCapacity"
	ReasonRolloutRejected             = "RolloutRejected"
	ReasonRouteEnabled                = "RouteEnabled"
	ReasonRouteDisabled               = "RouteDisabled"
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
	// TargetPendingRequests enables runtime-metrics-driven scale-up when the
	// selected runtime exposes a bounded pending request metric.
	// +kubebuilder:validation:Minimum=1
	TargetPendingRequests int32 `json:"targetPendingRequests,omitempty"`
	// IdleTimeout scales an active deployment to zero replicas after runtime
	// metrics report no running or pending work for the configured duration.
	IdleTimeout string `json:"idleTimeout,omitempty"`
}

// RoutingSpec controls exposure through the InferOps gateway.
type RoutingSpec struct {
	Enabled bool `json:"enabled,omitempty"`
	// +kubebuilder:validation:Pattern=^/
	Path             string            `json:"path,omitempty"`
	OpenAICompatible bool              `json:"openAICompatible,omitempty"`
	Policy           RoutingPolicySpec `json:"policy,omitempty"`
}

// RoutingPolicySpec controls gateway admission and candidate selection for
// routes that may be shared by stable and canary deployments.
type RoutingPolicySpec struct {
	// +kubebuilder:validation:Enum=LeastLoaded;Weighted
	RoutingStrategy RoutingStrategy `json:"routingStrategy,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1000000
	Weight *int32 `json:"weight,omitempty"`
	// RateLimit applies per gateway process and per backend. Use an upstream
	// API gateway for globally consistent tenant limits.
	RateLimit      *RateLimitSpec       `json:"rateLimit,omitempty"`
	RequestLogging RequestLoggingPolicy `json:"requestLogging,omitempty"`
}

// RoutingStrategy selects among ready backends sharing the same route path.
type RoutingStrategy string

const (
	RoutingStrategyLeastLoaded RoutingStrategy = "LeastLoaded"
	RoutingStrategyWeighted    RoutingStrategy = "Weighted"
)

// RateLimitSpec configures local gateway token-bucket admission.
type RateLimitSpec struct {
	// +kubebuilder:validation:Minimum=1
	RequestsPerMinute int32 `json:"requestsPerMinute,omitempty"`
	// +kubebuilder:validation:Minimum=0
	Burst int32 `json:"burst,omitempty"`
}

// RequestLoggingPolicy controls sanitized gateway request logs for a route.
type RequestLoggingPolicy struct {
	Enabled *bool `json:"enabled,omitempty"`
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

// SchedulingSpec controls placement constraints for runtime pods. InferOps
// combines these constraints with the node affinity required by node-local
// model caches.
type SchedulingSpec struct {
	NodeSelector              map[string]string          `json:"nodeSelector,omitempty"`
	Tolerations               []Toleration               `json:"tolerations,omitempty"`
	TopologySpreadConstraints []TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`
}

// Toleration is the supported subset of a Kubernetes pod toleration.
type Toleration struct {
	Key      string `json:"key,omitempty"`
	Operator string `json:"operator,omitempty"`
	Value    string `json:"value,omitempty"`
	Effect   string `json:"effect,omitempty"`
	// +kubebuilder:validation:Minimum=0
	TolerationSeconds *int64 `json:"tolerationSeconds,omitempty"`
}

// TopologySpreadConstraint distributes runtime pods across a node topology.
// The operator supplies the label selector for the managed runtime pods.
type TopologySpreadConstraint struct {
	// +kubebuilder:validation:Minimum=1
	MaxSkew int32 `json:"maxSkew"`
	// +kubebuilder:validation:Required
	TopologyKey string `json:"topologyKey"`
	// +kubebuilder:validation:Enum=DoNotSchedule;ScheduleAnyway
	WhenUnsatisfiable string `json:"whenUnsatisfiable"`
}

// AvailabilitySpec controls disruption protection for runtime pods.
type AvailabilitySpec struct {
	PodDisruptionBudget PodDisruptionBudgetSpec `json:"podDisruptionBudget,omitempty"`
}

// RolloutSpec controls how runtime pod-template changes are applied. Strategies
// that keep old and new pods live at the same time require spare compatible GPU
// capacity before the controller starts the rollout.
type RolloutSpec struct {
	// +kubebuilder:validation:Enum=Recreate;Rolling;BlueGreen;Canary
	Strategy RolloutStrategy `json:"strategy,omitempty"`
	// +kubebuilder:validation:Enum=Queue;Reject
	WhenCapacityUnavailable RolloutWhenCapacityUnavailable `json:"whenCapacityUnavailable,omitempty"`
	// CanaryWeightPercent documents the intended canary traffic share. Gateway
	// traffic selection still uses spec.routing.policy.weight for the backend.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=99
	CanaryWeightPercent *int32 `json:"canaryWeightPercent,omitempty"`
}

// RolloutStrategy selects the pod rollout shape for runtime changes.
type RolloutStrategy string

const (
	RolloutStrategyRecreate  RolloutStrategy = "Recreate"
	RolloutStrategyRolling   RolloutStrategy = "Rolling"
	RolloutStrategyBlueGreen RolloutStrategy = "BlueGreen"
	RolloutStrategyCanary    RolloutStrategy = "Canary"
)

// RolloutWhenCapacityUnavailable selects behavior when a surge rollout cannot
// fit beside the currently running runtime pods.
type RolloutWhenCapacityUnavailable string

const (
	RolloutWhenCapacityUnavailableQueue  RolloutWhenCapacityUnavailable = "Queue"
	RolloutWhenCapacityUnavailableReject RolloutWhenCapacityUnavailable = "Reject"
)

// PodDisruptionBudgetSpec configures the managed runtime PodDisruptionBudget.
// Protection is enabled by default when enabled is omitted.
type PodDisruptionBudgetSpec struct {
	Enabled *bool `json:"enabled,omitempty"`
	// +kubebuilder:validation:Minimum=0
	MinAvailable *int32 `json:"minAvailable,omitempty"`
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

// ScalingStatus reports the controller's replica planning decision.
type ScalingStatus struct {
	DesiredReplicas  int32        `json:"desiredReplicas,omitempty"`
	PendingRequests  int64        `json:"pendingRequests,omitempty"`
	RunningRequests  int64        `json:"runningRequests,omitempty"`
	LastActivityTime *metav1.Time `json:"lastActivityTime,omitempty"`
	CapacityLimited  bool         `json:"capacityLimited,omitempty"`
	Reason           string       `json:"reason,omitempty"`
	Message          string       `json:"message,omitempty"`
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
	if in.Scheduling.NodeSelector != nil {
		out.Scheduling.NodeSelector = make(map[string]string, len(in.Scheduling.NodeSelector))
		for key, value := range in.Scheduling.NodeSelector {
			out.Scheduling.NodeSelector[key] = value
		}
	}
	if in.Scheduling.Tolerations != nil {
		out.Scheduling.Tolerations = make([]Toleration, len(in.Scheduling.Tolerations))
		for i := range in.Scheduling.Tolerations {
			out.Scheduling.Tolerations[i] = in.Scheduling.Tolerations[i]
			if in.Scheduling.Tolerations[i].TolerationSeconds != nil {
				value := *in.Scheduling.Tolerations[i].TolerationSeconds
				out.Scheduling.Tolerations[i].TolerationSeconds = &value
			}
		}
	}
	if in.Scheduling.TopologySpreadConstraints != nil {
		out.Scheduling.TopologySpreadConstraints = make(
			[]TopologySpreadConstraint,
			len(in.Scheduling.TopologySpreadConstraints),
		)
		copy(out.Scheduling.TopologySpreadConstraints, in.Scheduling.TopologySpreadConstraints)
	}
	if in.Availability.PodDisruptionBudget.Enabled != nil {
		value := *in.Availability.PodDisruptionBudget.Enabled
		out.Availability.PodDisruptionBudget.Enabled = &value
	}
	if in.Availability.PodDisruptionBudget.MinAvailable != nil {
		value := *in.Availability.PodDisruptionBudget.MinAvailable
		out.Availability.PodDisruptionBudget.MinAvailable = &value
	}
	if in.Rollout.CanaryWeightPercent != nil {
		value := *in.Rollout.CanaryWeightPercent
		out.Rollout.CanaryWeightPercent = &value
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
	if in.DrainStartedAt != nil {
		out.DrainStartedAt = in.DrainStartedAt.DeepCopy()
	}
	if in.Replacement != nil {
		out.Replacement = new(ReplacementStatus)
		in.Replacement.DeepCopyInto(out.Replacement)
	}
	if in.Scaling.LastActivityTime != nil {
		out.Scaling.LastActivityTime = in.Scaling.LastActivityTime.DeepCopy()
	}
	if in.Conditions != nil {
		out.Conditions = make([]Condition, len(in.Conditions))
		for i := range in.Conditions {
			in.Conditions[i].DeepCopyInto(&out.Conditions[i])
		}
	}
}

// DeepCopyInto copies the receiver, writing into out.
func (in *ReplacementStatus) DeepCopyInto(out *ReplacementStatus) {
	*out = *in
	if in.Target != nil {
		out.Target = new(ReplacementReference)
		*out.Target = *in.Target
	}
	if in.RequestedBy != nil {
		out.RequestedBy = new(ReplacementReference)
		*out.RequestedBy = *in.RequestedBy
	}
	if in.StartedAt != nil {
		out.StartedAt = in.StartedAt.DeepCopy()
	}
}

// DeepCopy creates a copy.
func (in *ReplacementStatus) DeepCopy() *ReplacementStatus {
	if in == nil {
		return nil
	}
	out := new(ReplacementStatus)
	in.DeepCopyInto(out)
	return out
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
