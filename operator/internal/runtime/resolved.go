package runtime

import (
	"github.com/brassinai/inferops/operator/internal/templates"
)

// ResolvedRuntime holds the effective runtime configuration for a
// ModelDeployment after defaults from the referenced ModelRuntime have been
// applied and validated.
type ResolvedRuntime struct {
	spec ResolvedSpec
}

// ResolvedSpec is a fully resolved copy of the effective runtime configuration.
// It contains only the fields needed to construct runtime pods; the original
// ModelRuntime defaults are not carried forward.
type ResolvedSpec struct {
	Engine        string
	Protocol      string
	Image         string
	Port          int32
	HealthPath    string
	ReadinessPath string
	MetricsPath   string
	Command       []string
	Args          []string
	Env           map[string]string
}

// Spec returns a copy of the effective runtime configuration.
func (r ResolvedRuntime) Spec() ResolvedSpec {
	spec := r.spec
	spec.Command = append([]string(nil), r.spec.Command...)
	spec.Args = append([]string(nil), r.spec.Args...)
	if r.spec.Env != nil {
		spec.Env = make(map[string]string, len(r.spec.Env))
		for name, value := range r.spec.Env {
			spec.Env[name] = value
		}
	}
	return spec
}

// Image returns the runtime image that should be used for pods.
func (r ResolvedRuntime) Image() string { return r.spec.Image }

// Port returns the effective HTTP port.
func (r ResolvedRuntime) Port() int32 {
	if r.spec.Port != 0 {
		return r.spec.Port
	}
	return templates.RuntimeHTTPPort
}

// HealthPath returns the effective health probe path.
func (r ResolvedRuntime) HealthPath() string {
	if r.spec.HealthPath != "" {
		return r.spec.HealthPath
	}
	return templates.RuntimeHealthPath
}

// ReadinessPath returns the effective readiness probe path.
func (r ResolvedRuntime) ReadinessPath() string {
	if r.spec.ReadinessPath != "" {
		return r.spec.ReadinessPath
	}
	return r.HealthPath()
}

// MetricsPath returns the effective metrics path.
func (r ResolvedRuntime) MetricsPath() string {
	if r.spec.MetricsPath != "" {
		return r.spec.MetricsPath
	}
	return templates.RuntimeMetricsPath
}
