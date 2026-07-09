package v1alpha1

import (
	"encoding/json"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestModelDeploymentImplementsRuntimeObject(t *testing.T) {
	t.Parallel()
	var _ runtime.Object = &ModelDeployment{}
	var _ runtime.Object = &ModelDeploymentList{}
}

func TestModelRuntimeImplementsRuntimeObject(t *testing.T) {
	t.Parallel()
	var _ runtime.Object = &ModelRuntime{}
	var _ runtime.Object = &ModelRuntimeList{}
}

func TestModelCacheImplementsRuntimeObject(t *testing.T) {
	t.Parallel()
	var _ runtime.Object = &ModelCache{}
	var _ runtime.Object = &ModelCacheList{}
}

func TestModelDeploymentDeepCopy(t *testing.T) {
	t.Parallel()

	original := &ModelDeployment{
		Spec: ModelDeploymentSpec{
			Model: ModelSpec{
				Name:   "qwen-chat",
				Repo:   "Qwen/Qwen2.5-7B-Instruct",
				Source: "huggingface",
			},
			Runtime: RuntimeSpec{
				Ref:                "nano-vllm",
				TensorParallelSize: 1,
			},
			Resources: ResourceRequirements{
				GPU: &GPUResourceRequest{Count: 1, Vendor: "nvidia"},
			},
			Activation: ActivationSpec{
				DesiredState: ActivationDesiredStateActive,
				WhenFull:     ActivationWhenFullQueue,
			},
			Scheduling: SchedulingSpec{
				NodeSelector: map[string]string{"inferops.dev/pool": "gpu"},
				Tolerations: []Toleration{{
					Key:               "dedicated",
					TolerationSeconds: int64Pointer(60),
				}},
				TopologySpreadConstraints: []TopologySpreadConstraint{{
					MaxSkew:           1,
					TopologyKey:       "topology.kubernetes.io/zone",
					WhenUnsatisfiable: "ScheduleAnyway",
				}},
			},
			Availability: AvailabilitySpec{
				PodDisruptionBudget: PodDisruptionBudgetSpec{
					Enabled:      boolPointer(true),
					MinAvailable: int32Pointer(1),
				},
			},
			Rollout: RolloutSpec{
				Strategy:            RolloutStrategyCanary,
				CanaryWeightPercent: int32Pointer(10),
			},
		},
		Status: ModelDeploymentStatus{
			Phase:        ModelDeploymentPhaseActive,
			AssignedGPUs: []string{"gpu-0"},
			DrainStartedAt: func() *metav1.Time {
				value := metav1.Now()
				return &value
			}(),
			Replacement: &ReplacementStatus{
				Phase:  ReplacementPhaseDraining,
				Target: &ReplacementReference{Name: "victim", UID: "victim-uid"},
				RequestedBy: &ReplacementReference{
					Name: "requester",
					UID:  "requester-uid",
				},
				StartedAt: func() *metav1.Time {
					value := metav1.Now()
					return &value
				}(),
			},
			Conditions: []Condition{
				{Type: "Ready", Status: metav1.ConditionTrue, Reason: "RuntimeReady", LastTransitionTime: metav1.Now()},
			},
		},
	}
	original.Name = "qwen-chat"

	copied := original.DeepCopy()
	if copied == original {
		t.Fatal("DeepCopy returned the same pointer")
	}

	// Mutate the copy and verify the original is unchanged.
	copied.Spec.Model.Name = "modified"
	copied.Spec.Scheduling.NodeSelector["inferops.dev/pool"] = "modified"
	*copied.Spec.Scheduling.Tolerations[0].TolerationSeconds = 0
	copied.Spec.Scheduling.TopologySpreadConstraints[0].TopologyKey = "modified"
	*copied.Spec.Availability.PodDisruptionBudget.Enabled = false
	*copied.Spec.Availability.PodDisruptionBudget.MinAvailable = 0
	*copied.Spec.Rollout.CanaryWeightPercent = 50
	copied.Status.AssignedGPUs[0] = "modified"
	copied.Status.DrainStartedAt.Time = copied.Status.DrainStartedAt.Add(time.Hour)
	copied.Status.Replacement.Target.Name = "modified"
	copied.Status.Replacement.RequestedBy.Name = "modified"
	copied.Status.Replacement.StartedAt.Time = copied.Status.Replacement.StartedAt.Add(time.Hour)
	copied.Status.Conditions[0].Reason = "modified"
	copied.Status.Conditions[0].LastTransitionTime = metav1.Now()

	if original.Spec.Model.Name != "qwen-chat" {
		t.Errorf("original model name was mutated: got %q", original.Spec.Model.Name)
	}
	if original.Spec.Scheduling.NodeSelector["inferops.dev/pool"] != "gpu" ||
		*original.Spec.Scheduling.Tolerations[0].TolerationSeconds != 60 ||
		original.Spec.Scheduling.TopologySpreadConstraints[0].TopologyKey != "topology.kubernetes.io/zone" {
		t.Error("original scheduling constraints were mutated")
	}
	if !*original.Spec.Availability.PodDisruptionBudget.Enabled ||
		*original.Spec.Availability.PodDisruptionBudget.MinAvailable != 1 {
		t.Error("original availability configuration was mutated")
	}
	if original.Spec.Rollout.CanaryWeightPercent == nil ||
		*original.Spec.Rollout.CanaryWeightPercent != 10 {
		t.Error("original rollout configuration was mutated")
	}
	if original.Status.AssignedGPUs[0] != "gpu-0" {
		t.Errorf("original assignedGPUs was mutated: got %q", original.Status.AssignedGPUs[0])
	}
	if original.Status.Replacement.Target.Name != "victim" ||
		original.Status.Replacement.RequestedBy.Name != "requester" {
		t.Error("original replacement references were mutated")
	}
	if original.Status.DrainStartedAt.Equal(copied.Status.DrainStartedAt) ||
		original.Status.Replacement.StartedAt.Equal(copied.Status.Replacement.StartedAt) {
		t.Error("replacement lifecycle timestamps were not deeply copied")
	}
	if original.Status.Conditions[0].Reason != "RuntimeReady" {
		t.Errorf("original conditions were mutated: got %q", original.Status.Conditions[0].Reason)
	}
}

func boolPointer(value bool) *bool {
	return &value
}

func int32Pointer(value int32) *int32 {
	return &value
}

func int64Pointer(value int64) *int64 {
	return &value
}

func TestModelRuntimeDeepCopy(t *testing.T) {
	t.Parallel()

	original := &ModelRuntime{
		Spec: ModelRuntimeSpec{
			Engine:  "nano-vllm",
			Command: []string{"serve"},
			Args:    []string{"--port", "8000"},
			Env:     map[string]string{"KEY": "value"},
		},
		Status: ModelRuntimeStatus{
			Phase: ModelRuntimePhaseReady,
			Conditions: []Condition{
				{Type: "Ready", Status: metav1.ConditionTrue, LastTransitionTime: metav1.Now()},
			},
		},
	}
	original.Name = "nano-vllm"

	copied := original.DeepCopy()
	if copied == original {
		t.Fatal("DeepCopy returned the same pointer")
	}

	copied.Spec.Command[0] = "modified"
	copied.Spec.Args[0] = "modified"
	copied.Spec.Env["KEY"] = "modified"
	copied.Status.Conditions[0].Status = metav1.ConditionFalse

	if original.Spec.Command[0] != "serve" {
		t.Errorf("original command was mutated")
	}
	if original.Spec.Args[0] != "--port" {
		t.Errorf("original args were mutated")
	}
	if original.Spec.Env["KEY"] != "value" {
		t.Errorf("original env was mutated")
	}
	if original.Status.Conditions[0].Status != metav1.ConditionTrue {
		t.Errorf("original conditions were mutated")
	}
}

func TestModelCacheDeepCopy(t *testing.T) {
	t.Parallel()

	original := &ModelCache{
		Spec: ModelCacheSpec{
			ModelRepo: "Qwen/Qwen2.5-7B-Instruct",
			Storage: ModelCacheStorage{
				Type:         "nodeLocal",
				Size:         "100Gi",
				Path:         "/var/lib/inferops/models/qwen-chat",
				NodeSelector: map[string]string{"inferops.dev/pool": "inference"},
				Tolerations: []Toleration{{
					Key:               "dedicated",
					TolerationSeconds: int64Pointer(60),
				}},
			},
		},
		Status: ModelCacheStatus{
			Phase: ModelCachePhaseReady,
			Conditions: []Condition{
				{Type: "Ready", Status: metav1.ConditionTrue, LastTransitionTime: metav1.Now()},
			},
		},
	}
	original.Name = "qwen-chat-cache"

	copied := original.DeepCopy()
	if copied == original {
		t.Fatal("DeepCopy returned the same pointer")
	}

	copied.Spec.Storage.Path = "/modified"
	copied.Spec.Storage.NodeSelector["inferops.dev/pool"] = "modified"
	*copied.Spec.Storage.Tolerations[0].TolerationSeconds = 0
	copied.Status.Conditions[0].Status = metav1.ConditionFalse

	if original.Spec.Storage.Path != "/var/lib/inferops/models/qwen-chat" {
		t.Errorf("original storage path was mutated")
	}
	if original.Spec.Storage.NodeSelector["inferops.dev/pool"] != "inference" ||
		*original.Spec.Storage.Tolerations[0].TolerationSeconds != 60 {
		t.Error("original cache scheduling constraints were mutated")
	}
	if original.Status.Conditions[0].Status != metav1.ConditionTrue {
		t.Errorf("original conditions were mutated")
	}
}

func TestModelDeploymentJSONRoundTrip(t *testing.T) {
	t.Parallel()

	original := &ModelDeployment{
		Spec: ModelDeploymentSpec{
			Model: ModelSpec{
				Repo:   "Qwen/Qwen2.5-7B-Instruct",
				Source: "huggingface",
			},
			Runtime: RuntimeSpec{
				Ref: "nano-vllm",
			},
			Resources: ResourceRequirements{
				GPU: &GPUResourceRequest{Count: 1, Vendor: "nvidia"},
			},
		},
	}
	original.Name = "qwen-chat"

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	decoded := &ModelDeployment{}
	if err := json.Unmarshal(data, decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Name != original.Name {
		t.Errorf("name mismatch: got %q, want %q", decoded.Name, original.Name)
	}
	if decoded.Spec.Model.Repo != original.Spec.Model.Repo {
		t.Errorf("repo mismatch")
	}
	if decoded.Spec.Resources.GPU == nil || decoded.Spec.Resources.GPU.Count != 1 {
		t.Errorf("gpu mismatch")
	}
}

func TestModelDeploymentPhaseConstants(t *testing.T) {
	t.Parallel()

	phases := []ModelDeploymentPhase{
		ModelDeploymentPhasePending,
		ModelDeploymentPhaseDownloading,
		ModelDeploymentPhaseCached,
		ModelDeploymentPhaseWaitingForCapacity,
		ModelDeploymentPhaseWaitingForGPU,
		ModelDeploymentPhaseActivating,
		ModelDeploymentPhaseActive,
		ModelDeploymentPhaseDraining,
		ModelDeploymentPhaseDeactivating,
		ModelDeploymentPhaseFailed,
	}

	for _, phase := range phases {
		if phase == "" {
			t.Error("phase constant must not be empty")
		}
	}
}

func TestActivationDesiredStateConstants(t *testing.T) {
	t.Parallel()

	states := []ActivationDesiredState{
		ActivationDesiredStateInactive,
		ActivationDesiredStateActive,
	}

	for _, state := range states {
		if state == "" {
			t.Error("activation desired state constant must not be empty")
		}
	}
}

func TestActivationWhenFullConstants(t *testing.T) {
	t.Parallel()

	policies := []ActivationWhenFull{
		ActivationWhenFullQueue,
		ActivationWhenFullReject,
		ActivationWhenFullReplaceOldest,
		ActivationWhenFullReplaceLowestPriority,
	}

	for _, policy := range policies {
		if policy == "" {
			t.Error("activation whenFull constant must not be empty")
		}
	}
}

func TestModelRuntimePhaseConstants(t *testing.T) {
	t.Parallel()

	phases := []ModelRuntimePhase{
		ModelRuntimePhasePending,
		ModelRuntimePhaseReady,
		ModelRuntimePhaseUnavailable,
		ModelRuntimePhaseFailed,
	}

	for _, phase := range phases {
		if phase == "" {
			t.Error("phase constant must not be empty")
		}
	}
}

func TestModelCachePhaseConstants(t *testing.T) {
	t.Parallel()

	phases := []ModelCachePhase{
		ModelCachePhasePending,
		ModelCachePhaseDownloading,
		ModelCachePhaseReady,
		ModelCachePhaseFailed,
	}

	for _, phase := range phases {
		if phase == "" {
			t.Error("phase constant must not be empty")
		}
	}
}
