package validation

import (
	"testing"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
)

func TestValidateModelDeployment(t *testing.T) {
	t.Parallel()

	valid := v1alpha1.ModelDeployment{
		Metadata: v1alpha1.ObjectMeta{Name: "qwen-chat"},
		Spec: v1alpha1.ModelDeploymentSpec{
			Model:      v1alpha1.ModelSpec{Repo: "Qwen/Qwen2.5-7B-Instruct"},
			Runtime:    v1alpha1.RuntimeSpec{Ref: "nano-vllm"},
			Resources:  v1alpha1.ResourceRequirements{CPU: "4", Memory: "16Gi", GPU: &v1alpha1.GPUResourceRequest{Count: 1}},
			Activation: v1alpha1.ActivationSpec{DesiredState: v1alpha1.ActivationDesiredStateInactive, WhenFull: v1alpha1.ActivationWhenFullQueue},
			Scaling:    v1alpha1.ScalingSpec{MinReplicas: 0, MaxReplicas: 1},
		},
	}

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
		{name: "missing runtime ref", mutate: func(d *v1alpha1.ModelDeployment) { d.Spec.Runtime.Ref = "" }, wantErr: true},
		{name: "invalid desired state", mutate: func(d *v1alpha1.ModelDeployment) { d.Spec.Activation.DesiredState = "Warm" }, wantErr: true},
		{name: "invalid full policy", mutate: func(d *v1alpha1.ModelDeployment) { d.Spec.Activation.WhenFull = "EvictAny" }, wantErr: true},
		{name: "invalid gpu count", mutate: func(d *v1alpha1.ModelDeployment) { d.Spec.Resources.GPU.Count = 0 }, wantErr: true},
		{name: "tensor parallel size exceeds gpu count", mutate: func(d *v1alpha1.ModelDeployment) {
			d.Spec.Runtime.TensorParallelSize = 2
		}, wantErr: true},
		{name: "invalid scaling bounds", mutate: func(d *v1alpha1.ModelDeployment) { d.Spec.Scaling.MinReplicas = 2 }, wantErr: true},
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
