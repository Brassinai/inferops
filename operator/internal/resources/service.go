package resources

import (
	"errors"
	"fmt"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	"github.com/brassinai/inferops/operator/internal/templates"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// BuildRuntimeService returns a deterministic ClusterIP Service for a ModelDeployment runtime.
func BuildRuntimeService(
	md *v1alpha1.ModelDeployment,
	runtime *v1alpha1.ModelRuntime,
) (*corev1.Service, error) {
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
	selector := SelectorLabels(md.Name)

	port := runtime.Spec.Port
	if port == 0 {
		port = templates.RuntimeHTTPPort
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("runtime port %d must be between 1 and 65535", port)
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            templates.RuntimeServiceName(md.Name),
			Namespace:       md.Namespace,
			Labels:          labels,
			OwnerReferences: []metav1.OwnerReference{OwnerReferenceForModelDeployment(md)},
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: selector,
			Ports: []corev1.ServicePort{
				{
					Name:       templates.HTTPPortName,
					Port:       port,
					TargetPort: intstr.FromString(templates.HTTPPortName),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}, nil
}
