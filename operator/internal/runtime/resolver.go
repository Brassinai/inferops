package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	"github.com/brassinai/inferops/operator/internal/resources"
	"github.com/brassinai/inferops/operator/internal/templates"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// ErrRuntimeNotFound is returned when a ModelDeployment references a
// ModelRuntime that does not exist.
var ErrRuntimeNotFound = errors.New("referenced ModelRuntime not found")

// ErrInvalidRuntimeConfiguration is returned when the deployment and referenced
// ModelRuntime cannot produce a safe effective runtime configuration.
var ErrInvalidRuntimeConfiguration = errors.New("invalid runtime configuration")

// RuntimeGetter fetches a ModelRuntime by namespace and name.
type RuntimeGetter interface {
	GetRuntime(ctx context.Context, namespace, name string) (*v1alpha1.ModelRuntime, error)
}

// Resolver resolves the effective runtime configuration for a ModelDeployment.
type Resolver struct {
	getter RuntimeGetter
}

// NewResolver creates a runtime resolver.
func NewResolver(getter RuntimeGetter) *Resolver {
	return &Resolver{getter: getter}
}

// Resolve looks up the referenced ModelRuntime and returns an effective runtime
// configuration with defaults applied and compatibility validated.
func (r *Resolver) Resolve(ctx context.Context, deployment *v1alpha1.ModelDeployment) (ResolvedRuntime, error) {
	if err := ctx.Err(); err != nil {
		return ResolvedRuntime{}, err
	}
	if deployment == nil {
		return ResolvedRuntime{}, errors.New("model deployment is required")
	}
	if deployment.Spec.Runtime.Ref == "" {
		return ResolvedRuntime{}, errors.New("spec.runtime.ref is required")
	}
	if r == nil || r.getter == nil {
		return ResolvedRuntime{}, errors.New("runtime getter is required")
	}

	modelRuntime, err := r.getter.GetRuntime(ctx, deployment.Namespace, deployment.Spec.Runtime.Ref)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ResolvedRuntime{}, fmt.Errorf("%w: %s", ErrRuntimeNotFound, deployment.Spec.Runtime.Ref)
		}
		return ResolvedRuntime{}, fmt.Errorf("get model runtime %q: %w", deployment.Spec.Runtime.Ref, err)
	}
	if modelRuntime == nil {
		return ResolvedRuntime{}, fmt.Errorf("%w: %s", ErrRuntimeNotFound, deployment.Spec.Runtime.Ref)
	}

	resolved, err := resolveAgainstRuntime(deployment, modelRuntime)
	if err != nil {
		return ResolvedRuntime{}, fmt.Errorf(
			"%w for ModelRuntime %q: %w",
			ErrInvalidRuntimeConfiguration,
			modelRuntime.Name,
			err,
		)
	}
	return resolved, nil
}

func resolveAgainstRuntime(deployment *v1alpha1.ModelDeployment, runtime *v1alpha1.ModelRuntime) (ResolvedRuntime, error) {
	if runtime == nil {
		return ResolvedRuntime{}, errors.New("model runtime is required")
	}
	if runtime.Spec.Engine == "" {
		return ResolvedRuntime{}, errors.New("runtime engine is required")
	}
	if runtime.Spec.Protocol == "" {
		return ResolvedRuntime{}, errors.New("runtime protocol is required")
	}
	if runtime.Spec.Protocol != v1alpha1.ModelRuntimeProtocolOpenAI {
		return ResolvedRuntime{}, fmt.Errorf(
			"runtime protocol %q is unsupported; expected %q",
			runtime.Spec.Protocol,
			v1alpha1.ModelRuntimeProtocolOpenAI,
		)
	}

	image := deployment.Spec.Runtime.Image
	if image == "" {
		image = runtime.Spec.DefaultImage
	}
	if image == "" {
		return ResolvedRuntime{}, errors.New("runtime image is required")
	}
	if err := resources.ValidatePinnedImage(image); err != nil {
		return ResolvedRuntime{}, fmt.Errorf("runtime image: %w", err)
	}

	port := runtime.Spec.Port
	if port == 0 {
		port = templates.RuntimeHTTPPort
	}
	if port < 1 || port > 65535 {
		return ResolvedRuntime{}, fmt.Errorf("runtime port %d must be between 1 and 65535", port)
	}

	healthPath := runtime.Spec.HealthPath
	if healthPath == "" {
		healthPath = templates.RuntimeHealthPath
	}
	if !strings.HasPrefix(healthPath, "/") {
		return ResolvedRuntime{}, fmt.Errorf("runtime health path %q must start with /", healthPath)
	}

	readinessPath := runtime.Spec.ReadinessPath
	if readinessPath == "" {
		readinessPath = healthPath
	}
	if !strings.HasPrefix(readinessPath, "/") {
		return ResolvedRuntime{}, fmt.Errorf("runtime readiness path %q must start with /", readinessPath)
	}

	metricsPath := runtime.Spec.MetricsPath
	if metricsPath == "" {
		metricsPath = templates.RuntimeMetricsPath
	}
	if !strings.HasPrefix(metricsPath, "/") {
		return ResolvedRuntime{}, fmt.Errorf("runtime metrics path %q must start with /", metricsPath)
	}

	if deployment.Spec.Resources.GPU == nil || deployment.Spec.Resources.GPU.Count == 0 {
		if deployment.Spec.Runtime.TensorParallelSize != 0 {
			return ResolvedRuntime{}, errors.New("tensorParallelSize requires a GPU request")
		}
		if deployment.Spec.Runtime.GPUMemoryUtilization != 0 {
			return ResolvedRuntime{}, errors.New("gpuMemoryUtilization requires a GPU request")
		}
	} else {
		if deployment.Spec.Runtime.TensorParallelSize > deployment.Spec.Resources.GPU.Count {
			return ResolvedRuntime{}, fmt.Errorf(
				"tensorParallelSize (%d) must not exceed GPU count (%d)",
				deployment.Spec.Runtime.TensorParallelSize, deployment.Spec.Resources.GPU.Count,
			)
		}
	}

	env := make(map[string]string, len(runtime.Spec.Env))
	for k, v := range runtime.Spec.Env {
		env[k] = v
	}

	spec := ResolvedSpec{
		Engine:        runtime.Spec.Engine,
		Protocol:      runtime.Spec.Protocol,
		Image:         image,
		Port:          port,
		HealthPath:    healthPath,
		ReadinessPath: readinessPath,
		MetricsPath:   metricsPath,
		Command:       append([]string(nil), runtime.Spec.Command...),
		Args:          append([]string(nil), runtime.Spec.Args...),
		Env:           env,
	}

	return ResolvedRuntime{spec: spec}, nil
}
