package resources

import (
	"errors"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	"github.com/brassinai/inferops/operator/internal/templates"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BuildRuntimeConfigMap returns a deterministic ConfigMap holding non-sensitive
// runtime metadata. It does not contain tokens, credentials, or auth headers.
func BuildRuntimeConfigMap(
	md *v1alpha1.ModelDeployment,
	runtime *v1alpha1.ModelRuntime,
) (*corev1.ConfigMap, error) {
	if md == nil {
		return nil, errors.New("model deployment is required")
	}
	if runtime == nil {
		return nil, errors.New("model runtime is required")
	}
	if err := validateModelDeploymentName(md.Name); err != nil {
		return nil, err
	}
	labels := BaseLabels(md.Name)
	labels[LabelComponent] = "runtime-config"

	routePath := md.Spec.Routing.Path
	if routePath == "" {
		routePath = templates.GatewayModelPath(md.Name)
	}
	data := map[string]string{
		"model.repo":       md.Spec.Model.Repo,
		"model.revision":   md.Spec.Model.Revision,
		"runtime.ref":      md.Spec.Runtime.Ref,
		"runtime.engine":   runtime.Spec.Engine,
		"runtime.protocol": runtime.Spec.Protocol,
		"service.name":     templates.RuntimeServiceName(md.Name),
		"route.path":       routePath,
	}

	if md.Spec.Model.Name != "" {
		data["model.name"] = md.Spec.Model.Name
	}
	if md.Spec.Cache.Path != "" {
		data["cache.path"] = md.Spec.Cache.Path
	}
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            md.Name + "-runtime-config",
			Namespace:       md.Namespace,
			Labels:          labels,
			OwnerReferences: []metav1.OwnerReference{OwnerReferenceForModelDeployment(md)},
		},
		Data: data,
	}, nil
}
