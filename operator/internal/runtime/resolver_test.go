package runtime

import (
	"context"
	"errors"
	"testing"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	"github.com/brassinai/inferops/operator/internal/templates"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestResolveAppliesDefaults(t *testing.T) {
	t.Parallel()

	md := &v1alpha1.ModelDeployment{
		ObjectMeta: newObjectMeta("qwen-chat"),
		Spec: v1alpha1.ModelDeploymentSpec{
			Model: v1alpha1.ModelSpec{Repo: "Qwen/Qwen2.5-7B-Instruct"},
			Runtime: v1alpha1.RuntimeSpec{
				Ref: "nano-vllm",
			},
			Resources: v1alpha1.ResourceRequirements{
				CPU:    "4",
				Memory: "16Gi",
				GPU:    &v1alpha1.GPUResourceRequest{Count: 1, Vendor: "nvidia"},
			},
		},
	}
	runtime := &v1alpha1.ModelRuntime{
		ObjectMeta: newObjectMeta("nano-vllm"),
		Spec: v1alpha1.ModelRuntimeSpec{
			Engine:       "nano-vllm",
			Protocol:     "openai",
			DefaultImage: "ghcr.io/inferops/nano-vllm:v0.1.0",
		},
	}

	resolver := NewResolver(fakeGetter{runtime: runtime})
	resolved, err := resolver.Resolve(context.Background(), md)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if got, want := resolved.Image(), "ghcr.io/inferops/nano-vllm:v0.1.0"; got != want {
		t.Errorf("Image() = %q, want %q", got, want)
	}
	if got, want := resolved.Port(), int32(8000); got != want {
		t.Errorf("Port() = %d, want %d", got, want)
	}
	if got, want := resolved.HealthPath(), "/health"; got != want {
		t.Errorf("HealthPath() = %q, want %q", got, want)
	}
	if got, want := resolved.ReadinessPath(), "/health"; got != want {
		t.Errorf("ReadinessPath() = %q, want %q", got, want)
	}
	if got, want := resolved.MetricsPath(), "/metrics"; got != want {
		t.Errorf("MetricsPath() = %q, want %q", got, want)
	}
}

func TestResolveHonorsOverrides(t *testing.T) {
	t.Parallel()

	md := &v1alpha1.ModelDeployment{
		ObjectMeta: newObjectMeta("qwen-chat"),
		Spec: v1alpha1.ModelDeploymentSpec{
			Model: v1alpha1.ModelSpec{Repo: "Qwen/Qwen2.5-7B-Instruct"},
			Runtime: v1alpha1.RuntimeSpec{
				Ref:   "nano-vllm",
				Image: "ghcr.io/inferops/custom:v2.0.0",
			},
			Resources: v1alpha1.ResourceRequirements{
				CPU:    "4",
				Memory: "16Gi",
				GPU:    &v1alpha1.GPUResourceRequest{Count: 1},
			},
		},
	}
	runtime := &v1alpha1.ModelRuntime{
		ObjectMeta: newObjectMeta("nano-vllm"),
		Spec: v1alpha1.ModelRuntimeSpec{
			Engine:       "nano-vllm",
			Protocol:     "openai",
			DefaultImage: "ghcr.io/inferops/nano-vllm:v0.1.0",
			Port:         8080,
			HealthPath:   "/healthz",
			MetricsPath:  "/prometheus",
		},
	}

	resolver := NewResolver(fakeGetter{runtime: runtime})
	resolved, err := resolver.Resolve(context.Background(), md)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if got, want := resolved.Image(), "ghcr.io/inferops/custom:v2.0.0"; got != want {
		t.Errorf("Image() = %q, want %q", got, want)
	}
	if got, want := resolved.Port(), int32(8080); got != want {
		t.Errorf("Port() = %d, want %d", got, want)
	}
	if got, want := resolved.HealthPath(), "/healthz"; got != want {
		t.Errorf("HealthPath() = %q, want %q", got, want)
	}
	if got, want := resolved.MetricsPath(), "/prometheus"; got != want {
		t.Errorf("MetricsPath() = %q, want %q", got, want)
	}
}

func TestResolvedRuntimeDoesNotAliasModelRuntime(t *testing.T) {
	t.Parallel()

	md := &v1alpha1.ModelDeployment{
		ObjectMeta: newObjectMeta("qwen-chat"),
		Spec: v1alpha1.ModelDeploymentSpec{
			Runtime:   v1alpha1.RuntimeSpec{Ref: "nano-vllm"},
			Resources: v1alpha1.ResourceRequirements{CPU: "2", Memory: "4Gi"},
		},
	}
	modelRuntime := &v1alpha1.ModelRuntime{
		ObjectMeta: newObjectMeta("nano-vllm"),
		Spec: v1alpha1.ModelRuntimeSpec{
			Engine:       "nano-vllm",
			Protocol:     "openai",
			DefaultImage: "runtime:v1",
			Command:      []string{"serve"},
			Env:          map[string]string{"MODE": "safe"},
		},
	}

	resolved, err := NewResolver(fakeGetter{runtime: modelRuntime}).Resolve(context.Background(), md)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	modelRuntime.Spec.Command[0] = "mutated"
	modelRuntime.Spec.Env["MODE"] = "mutated"
	first := resolved.Spec()
	first.Command[0] = "also-mutated"
	first.Env["MODE"] = "also-mutated"
	second := resolved.Spec()

	if got, want := second.Command[0], "serve"; got != want {
		t.Errorf("resolved command = %q, want %q", got, want)
	}
	if got, want := second.Env["MODE"], "safe"; got != want {
		t.Errorf("resolved env MODE = %q, want %q", got, want)
	}
}

func TestResolveMissingRuntime(t *testing.T) {
	t.Parallel()

	md := &v1alpha1.ModelDeployment{
		ObjectMeta: newObjectMeta("qwen-chat"),
		Spec: v1alpha1.ModelDeploymentSpec{
			Model: v1alpha1.ModelSpec{Repo: "Qwen/Qwen2.5-7B-Instruct"},
			Runtime: v1alpha1.RuntimeSpec{
				Ref: "missing-runtime",
			},
			Resources: v1alpha1.ResourceRequirements{
				CPU:    "4",
				Memory: "16Gi",
				GPU:    &v1alpha1.GPUResourceRequest{Count: 1},
			},
		},
	}

	resolver := NewResolver(fakeGetter{})
	_, err := resolver.Resolve(context.Background(), md)
	if err == nil {
		t.Fatal("Resolve() expected error for missing runtime")
	}
	if !errors.Is(err, ErrRuntimeNotFound) {
		t.Errorf("Resolve() error = %v, want ErrRuntimeNotFound", err)
	}
}

func TestResolvePreservesGetterErrors(t *testing.T) {
	t.Parallel()

	getErr := errors.New("API unavailable")
	resolver := NewResolver(fakeGetter{err: getErr})
	md := &v1alpha1.ModelDeployment{
		ObjectMeta: newObjectMeta("qwen-chat"),
		Spec: v1alpha1.ModelDeploymentSpec{
			Runtime: v1alpha1.RuntimeSpec{Ref: "nano-vllm"},
		},
	}

	_, err := resolver.Resolve(context.Background(), md)
	if !errors.Is(err, getErr) {
		t.Fatalf("Resolve() error = %v, want wrapped getter error", err)
	}
	if errors.Is(err, ErrInvalidRuntimeConfiguration) {
		t.Fatalf("Resolve() classified getter error as invalid configuration: %v", err)
	}
}

func TestResolveClassifiesKubernetesNotFound(t *testing.T) {
	t.Parallel()

	resolver := NewResolver(fakeGetter{
		err: apierrors.NewNotFound(
			schema.GroupResource{Group: v1alpha1.GroupVersion.Group, Resource: "modelruntimes"},
			"missing-runtime",
		),
	})
	md := &v1alpha1.ModelDeployment{
		ObjectMeta: newObjectMeta("qwen-chat"),
		Spec: v1alpha1.ModelDeploymentSpec{
			Runtime: v1alpha1.RuntimeSpec{Ref: "missing-runtime"},
		},
	}

	_, err := resolver.Resolve(context.Background(), md)
	if !errors.Is(err, ErrRuntimeNotFound) {
		t.Fatalf("Resolve() error = %v, want ErrRuntimeNotFound", err)
	}
}

func TestResolveRejectsUnpinnedImage(t *testing.T) {
	t.Parallel()

	md := &v1alpha1.ModelDeployment{
		ObjectMeta: newObjectMeta("qwen-chat"),
		Spec: v1alpha1.ModelDeploymentSpec{
			Model: v1alpha1.ModelSpec{Repo: "Qwen/Qwen2.5-7B-Instruct"},
			Runtime: v1alpha1.RuntimeSpec{
				Ref: "nano-vllm",
			},
			Resources: v1alpha1.ResourceRequirements{
				CPU:    "4",
				Memory: "16Gi",
				GPU:    &v1alpha1.GPUResourceRequest{Count: 1},
			},
		},
	}
	runtime := &v1alpha1.ModelRuntime{
		ObjectMeta: newObjectMeta("nano-vllm"),
		Spec: v1alpha1.ModelRuntimeSpec{
			Engine:       "nano-vllm",
			Protocol:     "openai",
			DefaultImage: "ghcr.io/inferops/nano-vllm:latest",
		},
	}

	resolver := NewResolver(fakeGetter{runtime: runtime})
	_, err := resolver.Resolve(context.Background(), md)
	if err == nil {
		t.Fatal("Resolve() expected error for unpinned image")
	}
	if !errors.Is(err, ErrInvalidRuntimeConfiguration) {
		t.Errorf("Resolve() error = %v, want ErrInvalidRuntimeConfiguration", err)
	}
}

func TestResolveRejectsTensorParallelExceedsGPUCount(t *testing.T) {
	t.Parallel()

	md := &v1alpha1.ModelDeployment{
		ObjectMeta: newObjectMeta("qwen-chat"),
		Spec: v1alpha1.ModelDeploymentSpec{
			Model: v1alpha1.ModelSpec{Repo: "Qwen/Qwen2.5-7B-Instruct"},
			Runtime: v1alpha1.RuntimeSpec{
				Ref:                "nano-vllm",
				TensorParallelSize: 4,
			},
			Resources: v1alpha1.ResourceRequirements{
				CPU:    "4",
				Memory: "16Gi",
				GPU:    &v1alpha1.GPUResourceRequest{Count: 2},
			},
		},
	}
	runtime := &v1alpha1.ModelRuntime{
		ObjectMeta: newObjectMeta("nano-vllm"),
		Spec: v1alpha1.ModelRuntimeSpec{
			Engine:       "nano-vllm",
			Protocol:     "openai",
			DefaultImage: "ghcr.io/inferops/nano-vllm:v0.1.0",
		},
	}

	resolver := NewResolver(fakeGetter{runtime: runtime})
	_, err := resolver.Resolve(context.Background(), md)
	if err == nil {
		t.Fatal("Resolve() expected error for tensorParallelSize > GPU count")
	}
}

func TestResolveGPUFieldsRequireGPU(t *testing.T) {
	t.Parallel()

	md := &v1alpha1.ModelDeployment{
		ObjectMeta: newObjectMeta("qwen-chat"),
		Spec: v1alpha1.ModelDeploymentSpec{
			Model: v1alpha1.ModelSpec{Repo: "Qwen/Qwen2.5-7B-Instruct"},
			Runtime: v1alpha1.RuntimeSpec{
				Ref:                "nano-vllm",
				TensorParallelSize: 1,
			},
			Resources: v1alpha1.ResourceRequirements{
				CPU:    "4",
				Memory: "16Gi",
			},
		},
	}
	runtime := &v1alpha1.ModelRuntime{
		ObjectMeta: newObjectMeta("nano-vllm"),
		Spec: v1alpha1.ModelRuntimeSpec{
			Engine:       "nano-vllm",
			Protocol:     "openai",
			DefaultImage: "ghcr.io/inferops/nano-vllm:v0.1.0",
		},
	}

	resolver := NewResolver(fakeGetter{runtime: runtime})
	_, err := resolver.Resolve(context.Background(), md)
	if err == nil {
		t.Fatal("Resolve() expected error for GPU-only runtime field on CPU deployment")
	}
}

func TestResolvedPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		runtime   v1alpha1.ModelRuntimeSpec
		wantPort  int32
		wantPaths map[string]string
	}{
		{
			name:     "all defaults",
			runtime:  v1alpha1.ModelRuntimeSpec{DefaultImage: "img:v1"},
			wantPort: templates.RuntimeHTTPPort,
			wantPaths: map[string]string{
				"health":    templates.RuntimeHealthPath,
				"readiness": templates.RuntimeReadinessPath,
				"metrics":   templates.RuntimeMetricsPath,
			},
		},
		{
			name: "readiness defaults to health",
			runtime: v1alpha1.ModelRuntimeSpec{
				DefaultImage: "img:v1",
				HealthPath:   "/healthz",
			},
			wantPort: templates.RuntimeHTTPPort,
			wantPaths: map[string]string{
				"health":    "/healthz",
				"readiness": "/healthz",
				"metrics":   templates.RuntimeMetricsPath,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			md := &v1alpha1.ModelDeployment{
				ObjectMeta: newObjectMeta("qwen-chat"),
				Spec: v1alpha1.ModelDeploymentSpec{
					Model: v1alpha1.ModelSpec{Repo: "Qwen/Qwen2.5-7B-Instruct"},
					Runtime: v1alpha1.RuntimeSpec{
						Ref: "nano-vllm",
					},
					Resources: v1alpha1.ResourceRequirements{
						CPU:    "4",
						Memory: "16Gi",
						GPU:    &v1alpha1.GPUResourceRequest{Count: 1},
					},
				},
			}
			runtime := &v1alpha1.ModelRuntime{
				ObjectMeta: newObjectMeta("nano-vllm"),
				Spec:       tt.runtime,
			}
			runtime.Spec.Engine = "nano-vllm"
			runtime.Spec.Protocol = "openai"

			resolver := NewResolver(fakeGetter{runtime: runtime})
			resolved, err := resolver.Resolve(context.Background(), md)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}

			if got := resolved.Port(); got != tt.wantPort {
				t.Errorf("Port() = %d, want %d", got, tt.wantPort)
			}
			if got, want := resolved.HealthPath(), tt.wantPaths["health"]; got != want {
				t.Errorf("HealthPath() = %q, want %q", got, want)
			}
			if got, want := resolved.ReadinessPath(), tt.wantPaths["readiness"]; got != want {
				t.Errorf("ReadinessPath() = %q, want %q", got, want)
			}
			if got, want := resolved.MetricsPath(), tt.wantPaths["metrics"]; got != want {
				t.Errorf("MetricsPath() = %q, want %q", got, want)
			}
		})
	}
}

type fakeGetter struct {
	runtime *v1alpha1.ModelRuntime
	err     error
}

func (f fakeGetter) GetRuntime(_ context.Context, _, _ string) (*v1alpha1.ModelRuntime, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.runtime, nil
}

func newObjectMeta(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name, Namespace: "default"}
}
