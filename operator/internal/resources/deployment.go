package resources

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	"github.com/brassinai/inferops/operator/internal/templates"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
)

const (
	defaultDrainTimeout = 5 * time.Minute
	shutdownGraceBuffer = 30 * time.Second
)

// BuildRuntimeDeployment returns a deterministic runtime Deployment. Active
// deployments require a ready node-local cache placement. The lifecycle
// controller does not persist the zero-replica form for inactive models.
func (b Builder) BuildRuntimeDeployment(
	md *v1alpha1.ModelDeployment,
	runtime *v1alpha1.ModelRuntime,
	cacheNode string,
	cacheHostPath string,
) (*appsv1.Deployment, error) {
	if md == nil {
		return nil, errors.New("model deployment is required")
	}
	if runtime == nil {
		return nil, errors.New("model runtime is required")
	}
	protocol := strings.TrimSpace(runtime.Spec.Protocol)
	if protocol == "" {
		return nil, errors.New("runtime protocol is required")
	}
	if protocol != v1alpha1.ModelRuntimeProtocolOpenAI {
		return nil, fmt.Errorf(
			"runtime protocol %q is unsupported; expected %q",
			protocol,
			v1alpha1.ModelRuntimeProtocolOpenAI,
		)
	}
	if err := validateModelDeploymentName(md.Name); err != nil {
		return nil, err
	}

	active := md.Spec.Activation.DesiredState == v1alpha1.ActivationDesiredStateActive
	if (cacheNode == "") != (cacheHostPath == "") {
		return nil, errors.New("cache node and cache path must be provided together")
	}
	if active && cacheNode == "" {
		return nil, errors.New("active deployment requires a ready cache node and path")
	}
	if md.Spec.Scaling.MinReplicas < 0 {
		return nil, errors.New("minimum replicas must not be negative")
	}
	if md.Spec.Scaling.MaxReplicas < md.Spec.Scaling.MinReplicas {
		return nil, errors.New("maximum replicas must be greater than or equal to minimum replicas")
	}
	if active && md.Spec.Scaling.MaxReplicas < 1 {
		return nil, errors.New("active deployment requires maximum replicas of at least 1")
	}
	if cacheHostPath != "" {
		if err := b.validateCachePath(cacheHostPath); err != nil {
			return nil, err
		}
	}

	image := md.Spec.Runtime.Image
	if image == "" {
		image = runtime.Spec.DefaultImage
	}
	if image == "" {
		return nil, errors.New("runtime image is required")
	}

	port := runtime.Spec.Port
	if port == 0 {
		port = templates.RuntimeHTTPPort
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("runtime port %d must be between 1 and 65535", port)
	}

	healthPath := runtime.Spec.HealthPath
	if healthPath == "" {
		healthPath = templates.RuntimeHealthPath
	}
	readinessPath := runtime.Spec.ReadinessPath
	if readinessPath == "" {
		readinessPath = healthPath
	}
	metricsPath := runtime.Spec.MetricsPath
	if metricsPath == "" {
		metricsPath = templates.RuntimeMetricsPath
	}
	if !strings.HasPrefix(readinessPath, "/") {
		return nil, fmt.Errorf("runtime readiness path %q must start with /", readinessPath)
	}
	if !strings.HasPrefix(healthPath, "/") {
		return nil, fmt.Errorf("runtime health path %q must start with /", healthPath)
	}
	if !strings.HasPrefix(metricsPath, "/") {
		return nil, fmt.Errorf("runtime metrics path %q must start with /", metricsPath)
	}

	containerResources, err := buildRuntimeResources(md.Spec.Resources)
	if err != nil {
		return nil, err
	}
	environment, err := buildRuntimeEnvironment(md, runtime, port, b.runtimeModelPath, cacheHostPath != "")
	if err != nil {
		return nil, err
	}
	terminationGracePeriodSeconds, err := terminationGracePeriod(md.Spec.Activation.DrainTimeout)
	if err != nil {
		return nil, err
	}

	container := corev1.Container{
		Name:            templates.RuntimeContainerName,
		Image:           image,
		ImagePullPolicy: imagePullPolicy(image),
		Command:         append([]string(nil), runtime.Spec.Command...),
		Args:            append([]string(nil), runtime.Spec.Args...),
		Ports: []corev1.ContainerPort{
			{
				Name:          templates.HTTPPortName,
				ContainerPort: port,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		Env:       environment,
		Resources: containerResources,
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: boolPointer(false),
			Capabilities: &corev1.Capabilities{
				Drop: []corev1.Capability{"ALL"},
			},
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			},
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: readinessPath,
					Port: intstr.FromString(templates.HTTPPortName),
				},
			},
			PeriodSeconds:    templates.ProbePeriodSeconds,
			TimeoutSeconds:   templates.ProbeTimeoutSeconds,
			FailureThreshold: templates.ProbeFailureThreshold,
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: healthPath,
					Port: intstr.FromString(templates.HTTPPortName),
				},
			},
			PeriodSeconds:    templates.ProbePeriodSeconds,
			TimeoutSeconds:   templates.ProbeTimeoutSeconds,
			FailureThreshold: templates.ProbeFailureThreshold,
		},
		StartupProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: readinessPath,
					Port: intstr.FromString(templates.HTTPPortName),
				},
			},
			PeriodSeconds:    templates.ProbePeriodSeconds,
			TimeoutSeconds:   templates.ProbeTimeoutSeconds,
			FailureThreshold: templates.StartupProbeFailureThreshold,
		},
	}

	var volumes []corev1.Volume
	if cacheHostPath != "" {
		volume, mount := b.cacheVolumeAndMount(cacheHostPath, true)
		volumes = []corev1.Volume{volume}
		container.VolumeMounts = []corev1.VolumeMount{mount}
	}

	replicas := int32(0)
	if active {
		replicas = md.Spec.Scaling.MinReplicas
		if replicas == 0 {
			replicas = 1
		}
	}
	automountServiceAccountToken := false
	enableServiceLinks := false
	podSpec := corev1.PodSpec{
		AutomountServiceAccountToken:  &automountServiceAccountToken,
		EnableServiceLinks:            &enableServiceLinks,
		TerminationGracePeriodSeconds: &terminationGracePeriodSeconds,
		Containers:                    []corev1.Container{container},
		Volumes:                       volumes,
		NodeSelector:                  copyStringMap(md.Spec.Scheduling.NodeSelector),
		Tolerations:                   buildTolerations(md.Spec.Scheduling.Tolerations),
		TopologySpreadConstraints:     buildTopologySpreadConstraints(md),
	}
	if cacheNode != "" {
		if hostname, found := podSpec.NodeSelector[corev1.LabelHostname]; found && hostname != cacheNode {
			return nil, fmt.Errorf(
				"spec.scheduling.nodeSelector requires node %q but the model cache is ready on node %q",
				hostname,
				cacheNode,
			)
		}
		podSpec.Affinity = &corev1.Affinity{
			NodeAffinity: NodeAffinityForCacheNode(cacheNode),
		}
	}

	labels := BaseLabels(md.Name)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:            templates.RuntimeServiceName(md.Name),
			Namespace:       md.Namespace,
			Labels:          labels,
			OwnerReferences: []metav1.OwnerReference{OwnerReferenceForModelDeployment(md)},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RecreateDeploymentStrategyType,
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: SelectorLabels(md.Name),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
					Annotations: map[string]string{
						"inferops.dev/runtime-protocol": protocol,
						"prometheus.io/scrape":          "true",
						"prometheus.io/path":            metricsPath,
						"prometheus.io/port":            strconv.FormatInt(int64(port), 10),
					},
				},
				Spec: podSpec,
			},
		},
	}, nil
}

// BuildRuntimePodDisruptionBudget returns disruption protection for an active
// runtime. A nil result means protection was explicitly disabled.
func BuildRuntimePodDisruptionBudget(
	md *v1alpha1.ModelDeployment,
) (*policyv1.PodDisruptionBudget, error) {
	if md == nil {
		return nil, errors.New("model deployment is required")
	}
	if err := validateModelDeploymentName(md.Name); err != nil {
		return nil, err
	}
	if !podDisruptionBudgetEnabled(md) {
		return nil, nil
	}

	replicas := md.Spec.Scaling.MinReplicas
	if replicas < 1 {
		replicas = 1
	}
	minAvailable := replicas - 1
	if minAvailable < 1 {
		minAvailable = 1
	}
	if configured := md.Spec.Availability.PodDisruptionBudget.MinAvailable; configured != nil {
		if *configured < 0 {
			return nil, errors.New("PodDisruptionBudget minAvailable must not be negative")
		}
		if *configured > replicas {
			return nil, fmt.Errorf(
				"PodDisruptionBudget minAvailable %d must not exceed runtime replicas %d",
				*configured,
				replicas,
			)
		}
		minAvailable = *configured
	}
	minAvailableValue := intstr.FromInt32(minAvailable)
	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:            templates.RuntimeServiceName(md.Name),
			Namespace:       md.Namespace,
			Labels:          BaseLabels(md.Name),
			OwnerReferences: []metav1.OwnerReference{OwnerReferenceForModelDeployment(md)},
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MinAvailable: &minAvailableValue,
			Selector: &metav1.LabelSelector{
				MatchLabels: SelectorLabels(md.Name),
			},
			UnhealthyPodEvictionPolicy: unhealthyPodEvictionPolicyPointer(
				policyv1.AlwaysAllow,
			),
		},
	}, nil
}

func podDisruptionBudgetEnabled(md *v1alpha1.ModelDeployment) bool {
	enabled := md.Spec.Availability.PodDisruptionBudget.Enabled
	return enabled == nil || *enabled
}

func buildTolerations(input []v1alpha1.Toleration) []corev1.Toleration {
	if len(input) == 0 {
		return nil
	}
	result := make([]corev1.Toleration, len(input))
	for i := range input {
		operator := corev1.TolerationOperator(input[i].Operator)
		if operator == "" {
			operator = corev1.TolerationOpEqual
		}
		result[i] = corev1.Toleration{
			Key:               input[i].Key,
			Operator:          operator,
			Value:             input[i].Value,
			Effect:            corev1.TaintEffect(input[i].Effect),
			TolerationSeconds: copyInt64Pointer(input[i].TolerationSeconds),
		}
	}
	return result
}

func buildTopologySpreadConstraints(md *v1alpha1.ModelDeployment) []corev1.TopologySpreadConstraint {
	input := md.Spec.Scheduling.TopologySpreadConstraints
	if len(input) == 0 {
		return nil
	}
	result := make([]corev1.TopologySpreadConstraint, len(input))
	for i := range input {
		result[i] = corev1.TopologySpreadConstraint{
			MaxSkew:           input[i].MaxSkew,
			TopologyKey:       input[i].TopologyKey,
			WhenUnsatisfiable: corev1.UnsatisfiableConstraintAction(input[i].WhenUnsatisfiable),
			LabelSelector: &metav1.LabelSelector{
				MatchLabels: SelectorLabels(md.Name),
			},
		}
	}
	return result
}

func copyStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func copyInt64Pointer(input *int64) *int64 {
	if input == nil {
		return nil
	}
	value := *input
	return &value
}

func unhealthyPodEvictionPolicyPointer(
	policy policyv1.UnhealthyPodEvictionPolicyType,
) *policyv1.UnhealthyPodEvictionPolicyType {
	return &policy
}

func imagePullPolicy(image string) corev1.PullPolicy {
	imageName := image
	if slash := strings.LastIndex(imageName, "/"); slash >= 0 {
		imageName = imageName[slash+1:]
	}
	if !strings.Contains(imageName, "@") &&
		(!strings.Contains(imageName, ":") || strings.HasSuffix(imageName, ":latest")) {
		return corev1.PullAlways
	}
	return corev1.PullIfNotPresent
}

func buildRuntimeResources(input v1alpha1.ResourceRequirements) (corev1.ResourceRequirements, error) {
	requests := corev1.ResourceList{}
	limits := corev1.ResourceList{}

	if input.GPU != nil {
		if input.GPU.Count < 1 {
			return corev1.ResourceRequirements{}, errors.New("GPU count must be at least 1")
		}
		vendor := input.GPU.Vendor
		if vendor == "" {
			vendor = templates.DefaultGPUVendor
		}
		gpuResourceName := corev1.ResourceName(fmt.Sprintf("%s.com/gpu", vendor))
		if messages := utilvalidation.IsQualifiedName(string(gpuResourceName)); len(messages) != 0 {
			return corev1.ResourceRequirements{}, fmt.Errorf(
				"GPU resource name %q is invalid: %s",
				gpuResourceName,
				strings.Join(messages, ", "),
			)
		}
		gpuQuantity := *resource.NewQuantity(int64(input.GPU.Count), resource.DecimalSI)
		requests[gpuResourceName] = gpuQuantity
		limits[gpuResourceName] = gpuQuantity
	}

	if input.CPU != "" {
		quantity, err := resource.ParseQuantity(input.CPU)
		if err != nil {
			return corev1.ResourceRequirements{}, fmt.Errorf("parse CPU quantity %q: %w", input.CPU, err)
		}
		if quantity.Sign() <= 0 {
			return corev1.ResourceRequirements{}, fmt.Errorf("CPU quantity %q must be greater than zero", input.CPU)
		}
		requests[corev1.ResourceCPU] = quantity
		limits[corev1.ResourceCPU] = quantity
	}
	if input.Memory != "" {
		quantity, err := resource.ParseQuantity(input.Memory)
		if err != nil {
			return corev1.ResourceRequirements{}, fmt.Errorf("parse memory quantity %q: %w", input.Memory, err)
		}
		if quantity.Sign() <= 0 {
			return corev1.ResourceRequirements{}, fmt.Errorf("memory quantity %q must be greater than zero", input.Memory)
		}
		requests[corev1.ResourceMemory] = quantity
		limits[corev1.ResourceMemory] = quantity
	}

	if len(requests) == 0 {
		requests = nil
	}
	if len(limits) == 0 {
		limits = nil
	}
	return corev1.ResourceRequirements{
		Requests: requests,
		Limits:   limits,
	}, nil
}

func buildRuntimeEnvironment(
	md *v1alpha1.ModelDeployment,
	runtime *v1alpha1.ModelRuntime,
	port int32,
	modelPath string,
	hasCache bool,
) ([]corev1.EnvVar, error) {
	environment := []corev1.EnvVar{
		{Name: templates.EnvModelRepo, Value: md.Spec.Model.Repo},
		{Name: templates.EnvPort, Value: strconv.FormatInt(int64(port), 10)},
	}
	if hasCache {
		environment = append(environment, corev1.EnvVar{Name: templates.EnvModelPath, Value: modelPath})
	}
	if md.Spec.Runtime.MaxModelLen > 0 {
		environment = append(environment, corev1.EnvVar{
			Name:  templates.EnvMaxModelLen,
			Value: strconv.FormatInt(int64(md.Spec.Runtime.MaxModelLen), 10),
		})
	}
	if md.Spec.Runtime.TensorParallelSize > 0 {
		environment = append(environment, corev1.EnvVar{
			Name:  templates.EnvTensorParallelSize,
			Value: strconv.FormatInt(int64(md.Spec.Runtime.TensorParallelSize), 10),
		})
	}
	if md.Spec.Runtime.GPUMemoryUtilization > 0 {
		environment = append(environment, corev1.EnvVar{
			Name: templates.EnvGPUMemoryUtilization,
			Value: strconv.FormatFloat(
				md.Spec.Runtime.GPUMemoryUtilization,
				'f',
				-1,
				64,
			),
		})
	}
	if md.Spec.Runtime.DType != "" {
		environment = append(environment, corev1.EnvVar{
			Name:  templates.EnvModelDType,
			Value: md.Spec.Runtime.DType,
		})
	}

	names := make([]string, 0, len(runtime.Spec.Env))
	for name := range runtime.Spec.Env {
		if isManagedRuntimeEnvironment(name) {
			return nil, fmt.Errorf("runtime environment variable %q is managed by InferOps", name)
		}
		if messages := utilvalidation.IsEnvVarName(name); len(messages) != 0 {
			return nil, fmt.Errorf(
				"runtime environment variable name %q is invalid: %s",
				name,
				strings.Join(messages, ", "),
			)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		environment = append(environment, corev1.EnvVar{
			Name:  name,
			Value: runtime.Spec.Env[name],
		})
	}
	return environment, nil
}

func isManagedRuntimeEnvironment(name string) bool {
	switch name {
	case templates.EnvModelRepo,
		templates.EnvModelPath,
		templates.EnvMaxModelLen,
		templates.EnvTensorParallelSize,
		templates.EnvGPUMemoryUtilization,
		templates.EnvModelDType,
		templates.EnvPort:
		return true
	default:
		return false
	}
}

func terminationGracePeriod(drainTimeout string) (int64, error) {
	timeout := defaultDrainTimeout
	if drainTimeout != "" {
		parsed, err := time.ParseDuration(drainTimeout)
		if err != nil {
			return 0, fmt.Errorf("parse drain timeout %q: %w", drainTimeout, err)
		}
		timeout = parsed
	}
	if timeout <= 0 {
		return 0, errors.New("drain timeout must be greater than zero")
	}
	if timeout > time.Duration(1<<63-1)-shutdownGraceBuffer {
		return 0, errors.New("drain timeout is too large")
	}
	return int64((timeout + shutdownGraceBuffer) / time.Second), nil
}
