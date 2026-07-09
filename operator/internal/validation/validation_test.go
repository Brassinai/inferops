package validation

import (
	"testing"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
)

func TestValidateModelDeployment(t *testing.T) {
	t.Parallel()

	valid := v1alpha1.ModelDeployment{
		Spec: v1alpha1.ModelDeploymentSpec{
			Model:      v1alpha1.ModelSpec{Repo: "Qwen/Qwen2.5-7B-Instruct"},
			Runtime:    v1alpha1.RuntimeSpec{Ref: "nano-vllm"},
			Resources:  v1alpha1.ResourceRequirements{CPU: "4", Memory: "16Gi", GPU: &v1alpha1.GPUResourceRequest{Count: 1}},
			Activation: v1alpha1.ActivationSpec{DesiredState: v1alpha1.ActivationDesiredStateInactive, WhenFull: v1alpha1.ActivationWhenFullQueue},
			Scaling:    v1alpha1.ScalingSpec{MinReplicas: 0, MaxReplicas: 1},
		},
	}
	valid.Name = "qwen-chat"

	tests := []struct {
		name    string
		mutate  func(*v1alpha1.ModelDeployment)
		wantErr bool
	}{
		{name: "valid"},
		{name: "valid cpu only", mutate: func(d *v1alpha1.ModelDeployment) { d.Spec.Resources.GPU = nil }},
		{name: "cpu only missing cpu", mutate: func(d *v1alpha1.ModelDeployment) {
			d.Spec.Resources.GPU = nil
			d.Spec.Resources.CPU = ""
		}, wantErr: true},
		{name: "cpu only missing memory", mutate: func(d *v1alpha1.ModelDeployment) {
			d.Spec.Resources.GPU = nil
			d.Spec.Resources.Memory = ""
		}, wantErr: true},
		{name: "cpu only with tensor parallel size", mutate: func(d *v1alpha1.ModelDeployment) {
			d.Spec.Resources.GPU = nil
			d.Spec.Runtime.TensorParallelSize = 1
		}, wantErr: true},
		{name: "cpu only with gpu memory utilization", mutate: func(d *v1alpha1.ModelDeployment) {
			d.Spec.Resources.GPU = nil
			d.Spec.Runtime.GPUMemoryUtilization = 0.85
		}, wantErr: true},
		{name: "missing model repo", mutate: func(d *v1alpha1.ModelDeployment) { d.Spec.Model.Repo = "" }, wantErr: true},
		{name: "unsupported model source", mutate: func(d *v1alpha1.ModelDeployment) { d.Spec.Model.Source = "s3" }, wantErr: true},
		{name: "missing runtime ref", mutate: func(d *v1alpha1.ModelDeployment) { d.Spec.Runtime.Ref = "" }, wantErr: true},
		{name: "invalid runtime ref", mutate: func(d *v1alpha1.ModelDeployment) { d.Spec.Runtime.Ref = "Not Valid" }, wantErr: true},
		{name: "invalid cpu quantity", mutate: func(d *v1alpha1.ModelDeployment) { d.Spec.Resources.CPU = "many" }, wantErr: true},
		{name: "zero memory quantity", mutate: func(d *v1alpha1.ModelDeployment) { d.Spec.Resources.Memory = "0" }, wantErr: true},
		{name: "invalid desired state", mutate: func(d *v1alpha1.ModelDeployment) { d.Spec.Activation.DesiredState = "Warm" }, wantErr: true},
		{name: "invalid full policy", mutate: func(d *v1alpha1.ModelDeployment) { d.Spec.Activation.WhenFull = "EvictAny" }, wantErr: true},
		{name: "invalid gpu count", mutate: func(d *v1alpha1.ModelDeployment) { d.Spec.Resources.GPU.Count = 0 }, wantErr: true},
		{name: "tensor parallel size exceeds gpu count", mutate: func(d *v1alpha1.ModelDeployment) {
			d.Spec.Runtime.TensorParallelSize = 2
		}, wantErr: true},
		{name: "invalid scaling bounds", mutate: func(d *v1alpha1.ModelDeployment) { d.Spec.Scaling.MinReplicas = 2 }, wantErr: true},
		{name: "active with zero maximum replicas", mutate: func(d *v1alpha1.ModelDeployment) {
			d.Spec.Activation.DesiredState = v1alpha1.ActivationDesiredStateActive
			d.Spec.Scaling.MinReplicas = 0
			d.Spec.Scaling.MaxReplicas = 0
		}, wantErr: true},
		{name: "mutable runtime image", mutate: func(d *v1alpha1.ModelDeployment) {
			d.Spec.Runtime.Image = "ghcr.io/inferops/runtime:latest"
		}, wantErr: true},
		{name: "valid custom route", mutate: func(d *v1alpha1.ModelDeployment) {
			d.Spec.Routing.Path = "/inference/qwen"
		}},
		{name: "non-canonical custom route", mutate: func(d *v1alpha1.ModelDeployment) {
			d.Spec.Routing.Path = "/models/../readyz"
		}, wantErr: true},
		{name: "reserved custom route", mutate: func(d *v1alpha1.ModelDeployment) {
			d.Spec.Routing.Path = "/healthz/model"
		}, wantErr: true},
		{name: "escaped custom route", mutate: func(d *v1alpha1.ModelDeployment) {
			d.Spec.Routing.Path = "/models/qwen%2fother"
		}, wantErr: true},
		{name: "invalid node selector", mutate: func(d *v1alpha1.ModelDeployment) {
			d.Spec.Scheduling.NodeSelector = map[string]string{"not a key": "value"}
		}, wantErr: true},
		{name: "invalid toleration", mutate: func(d *v1alpha1.ModelDeployment) {
			d.Spec.Scheduling.Tolerations = []v1alpha1.Toleration{{Operator: "Equal"}}
		}, wantErr: true},
		{name: "invalid topology spread", mutate: func(d *v1alpha1.ModelDeployment) {
			d.Spec.Scheduling.TopologySpreadConstraints = []v1alpha1.TopologySpreadConstraint{{
				MaxSkew:           0,
				TopologyKey:       "topology.kubernetes.io/zone",
				WhenUnsatisfiable: "ScheduleAnyway",
			}}
		}, wantErr: true},
		{name: "PDB exceeds replicas", mutate: func(d *v1alpha1.ModelDeployment) {
			minAvailable := int32(2)
			d.Spec.Availability.PodDisruptionBudget.MinAvailable = &minAvailable
		}, wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			deployment := valid
			gpu := *valid.Spec.Resources.GPU
			deployment.Spec.Resources.GPU = &gpu
			if tt.mutate != nil {
				tt.mutate(&deployment)
			}
			if err := ValidateModelDeployment(deployment); (err != nil) != tt.wantErr {
				t.Fatalf("ValidateModelDeployment() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateModelRuntime(t *testing.T) {
	t.Parallel()

	valid := v1alpha1.ModelRuntime{
		Spec: v1alpha1.ModelRuntimeSpec{
			Engine:       "nano-vllm",
			Protocol:     "openai",
			DefaultImage: "ghcr.io/inferops/nano-vllm:v0.1.0",
			Port:         8000,
			HealthPath:   "/health",
		},
	}
	valid.Name = "nano-vllm"

	tests := []struct {
		name    string
		mutate  func(*v1alpha1.ModelRuntime)
		wantErr bool
	}{
		{name: "valid"},
		{name: "missing name", mutate: func(r *v1alpha1.ModelRuntime) { r.Name = "" }, wantErr: true},
		{name: "missing engine", mutate: func(r *v1alpha1.ModelRuntime) { r.Spec.Engine = "" }, wantErr: true},
		{name: "missing protocol", mutate: func(r *v1alpha1.ModelRuntime) { r.Spec.Protocol = "" }, wantErr: true},
		{name: "unsupported protocol", mutate: func(r *v1alpha1.ModelRuntime) { r.Spec.Protocol = "grpc" }, wantErr: true},
		{name: "missing defaultImage", mutate: func(r *v1alpha1.ModelRuntime) { r.Spec.DefaultImage = "" }, wantErr: true},
		{name: "invalid port zero", mutate: func(r *v1alpha1.ModelRuntime) { r.Spec.Port = 0 }, wantErr: true},
		{name: "invalid port too high", mutate: func(r *v1alpha1.ModelRuntime) { r.Spec.Port = 70000 }, wantErr: true},
		{name: "missing healthPath", mutate: func(r *v1alpha1.ModelRuntime) { r.Spec.HealthPath = "" }, wantErr: true},
		{name: "mutable image", mutate: func(r *v1alpha1.ModelRuntime) {
			r.Spec.DefaultImage = "ghcr.io/inferops/nano-vllm:latest"
		}, wantErr: true},
		{name: "invalid health path", mutate: func(r *v1alpha1.ModelRuntime) {
			r.Spec.HealthPath = "health"
		}, wantErr: true},
		{name: "invalid environment name", mutate: func(r *v1alpha1.ModelRuntime) {
			r.Spec.Env = map[string]string{"NOT VALID": "value"}
		}, wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			runtime := valid
			if tt.mutate != nil {
				tt.mutate(&runtime)
			}
			if err := ValidateModelRuntime(runtime); (err != nil) != tt.wantErr {
				t.Fatalf("ValidateModelRuntime() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateModelCache(t *testing.T) {
	t.Parallel()

	valid := v1alpha1.ModelCache{
		Spec: v1alpha1.ModelCacheSpec{
			ModelRepo: "Qwen/Qwen2.5-7B-Instruct",
			Storage: v1alpha1.ModelCacheStorage{
				Type: "nodeLocal",
				Size: "100Gi",
				Path: "/var/lib/inferops/models/qwen-chat",
			},
		},
	}
	valid.Name = "qwen-chat-cache"

	tests := []struct {
		name    string
		mutate  func(*v1alpha1.ModelCache)
		wantErr bool
	}{
		{name: "valid"},
		{name: "missing name", mutate: func(c *v1alpha1.ModelCache) { c.Name = "" }, wantErr: true},
		{name: "missing modelRepo", mutate: func(c *v1alpha1.ModelCache) { c.Spec.ModelRepo = "" }, wantErr: true},
		{name: "missing storage type", mutate: func(c *v1alpha1.ModelCache) { c.Spec.Storage.Type = "" }, wantErr: true},
		{name: "missing storage size", mutate: func(c *v1alpha1.ModelCache) { c.Spec.Storage.Size = "" }, wantErr: true},
		{name: "missing storage path", mutate: func(c *v1alpha1.ModelCache) { c.Spec.Storage.Path = "" }, wantErr: true},
		{name: "relative storage path", mutate: func(c *v1alpha1.ModelCache) {
			c.Spec.Storage.Path = "models/qwen"
		}, wantErr: true},
		{name: "invalid storage node selector", mutate: func(c *v1alpha1.ModelCache) {
			c.Spec.Storage.NodeSelector = map[string]string{"not a key": "value"}
		}, wantErr: true},
		{name: "invalid storage toleration", mutate: func(c *v1alpha1.ModelCache) {
			c.Spec.Storage.Tolerations = []v1alpha1.Toleration{{Operator: "Sometimes"}}
		}, wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			cache := valid
			if tt.mutate != nil {
				tt.mutate(&cache)
			}
			if err := ValidateModelCache(cache); (err != nil) != tt.wantErr {
				t.Fatalf("ValidateModelCache() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewReconciliationValidator(t *testing.T) {
	t.Parallel()

	_, err := NewReconciliationValidator("/var/lib/inferops/models")
	if err != nil {
		t.Fatalf("NewReconciliationValidator() error = %v", err)
	}

	_, err = NewReconciliationValidator("relative/path")
	if err == nil {
		t.Error("NewReconciliationValidator() expected error for relative path")
	}

	_, err = NewReconciliationValidator("/")
	if err == nil {
		t.Error("NewReconciliationValidator() expected error for filesystem root")
	}
}

func TestNilReconciliationValidator(t *testing.T) {
	t.Parallel()

	var validator *ReconciliationValidator
	if err := validator.ValidateForReconciliation(&v1alpha1.ModelDeployment{}); err == nil {
		t.Fatal("ValidateForReconciliation() expected an error for a nil validator")
	}
}

func TestReconciliationValidator(t *testing.T) {
	t.Parallel()

	validator, err := NewReconciliationValidator("/var/lib/inferops/models")
	if err != nil {
		t.Fatalf("NewReconciliationValidator() error = %v", err)
	}

	valid := &v1alpha1.ModelDeployment{
		Spec: v1alpha1.ModelDeploymentSpec{
			Model: v1alpha1.ModelSpec{
				Repo:   "Qwen/Qwen2.5-7B-Instruct",
				Source: "huggingface",
			},
			Runtime: v1alpha1.RuntimeSpec{Ref: "nano-vllm"},
			Resources: v1alpha1.ResourceRequirements{
				CPU:    "4",
				Memory: "16Gi",
				GPU:    &v1alpha1.GPUResourceRequest{Count: 1},
			},
			Activation: v1alpha1.ActivationSpec{DrainTimeout: "5m"},
			Cache:      v1alpha1.CacheSpec{Path: "/var/lib/inferops/models"},
			Secrets: v1alpha1.SecretReferences{
				HuggingFaceTokenSecretName: "hf-token",
			},
		},
	}

	tests := []struct {
		name       string
		mutate     func(*v1alpha1.ModelDeployment)
		wantErr    bool
		wantReason string
	}{
		{name: "valid"},
		{
			name: "valid child cache path",
			mutate: func(d *v1alpha1.ModelDeployment) {
				d.Spec.Cache.Path = "/var/lib/inferops/models/qwen-chat"
			},
		},
		{
			name: "invalid drain timeout",
			mutate: func(d *v1alpha1.ModelDeployment) {
				d.Spec.Activation.DrainTimeout = "not-a-duration"
			},
			wantErr:    true,
			wantReason: v1alpha1.ReasonInvalidDrainTimeout,
		},
		{
			name: "zero drain timeout",
			mutate: func(d *v1alpha1.ModelDeployment) {
				d.Spec.Activation.DrainTimeout = "0s"
			},
			wantErr: true,
		},
		{
			name: "cache path outside root",
			mutate: func(d *v1alpha1.ModelDeployment) {
				d.Spec.Cache.Path = "/tmp/models"
			},
			wantErr:    true,
			wantReason: v1alpha1.ReasonInvalidCachePath,
		},
		{
			name: "relative cache path",
			mutate: func(d *v1alpha1.ModelDeployment) {
				d.Spec.Cache.Path = "models"
			},
			wantErr:    true,
			wantReason: v1alpha1.ReasonInvalidCachePath,
		},
		{
			name: "public huggingface model without token secret",
			mutate: func(d *v1alpha1.ModelDeployment) {
				d.Spec.Secrets.HuggingFaceTokenSecretName = ""
			},
		},
		{
			name: "invalid huggingface token secret name",
			mutate: func(d *v1alpha1.ModelDeployment) {
				d.Spec.Secrets.HuggingFaceTokenSecretName = "Not Valid"
			},
			wantErr:    true,
			wantReason: v1alpha1.ReasonSecretRequired,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			deployment := valid.DeepCopy()
			if tt.mutate != nil {
				tt.mutate(deployment)
			}
			err := validator.ValidateForReconciliation(deployment)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateForReconciliation() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantReason != "" && err != nil && !containsSubstring(err.Error(), tt.wantReason) {
				t.Errorf("error %q does not contain reason %q", err.Error(), tt.wantReason)
			}
		})
	}
}

func containsSubstring(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
