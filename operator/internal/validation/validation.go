package validation

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/brassinai/inferops/internal/routingpath"
	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	"github.com/brassinai/inferops/operator/internal/paths"
	"github.com/brassinai/inferops/operator/internal/resources"
	"github.com/brassinai/inferops/operator/internal/templates"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
)

// ValidationError carries a stable reason code together with a human-readable
// message.  Callers can use errors.As to recover the reason for status
// conditions and Events.
type ValidationError struct {
	Reason  string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Reason + ": " + e.Message
}

// ValidateModelDeployment validates the minimal fields needed for a model deployment.
func ValidateModelDeployment(deployment v1alpha1.ModelDeployment) error {
	if deployment.Name == "" {
		return errors.New("metadata.name is required")
	}
	runtimeName := templates.RuntimeServiceName(deployment.Name)
	if messages := utilvalidation.IsDNS1035Label(runtimeName); len(messages) != 0 {
		return fmt.Errorf("generated runtime resource name %q is invalid: %s", runtimeName, strings.Join(messages, ", "))
	}
	if messages := utilvalidation.IsValidLabelValue(deployment.Name); len(messages) != 0 {
		return fmt.Errorf("metadata.name %q cannot be used as a managed-resource label: %s",
			deployment.Name, strings.Join(messages, ", "))
	}
	if deployment.Spec.Model.Repo == "" {
		return errors.New("spec.model.repo is required")
	}
	switch deployment.Spec.Model.Source {
	case "", "huggingface":
	default:
		return fmt.Errorf("spec.model.source %q is unsupported; expected huggingface", deployment.Spec.Model.Source)
	}
	if deployment.Spec.Runtime.Ref == "" {
		return errors.New("spec.runtime.ref is required")
	}
	if messages := utilvalidation.IsDNS1123Subdomain(deployment.Spec.Runtime.Ref); len(messages) != 0 {
		return fmt.Errorf("spec.runtime.ref %q is invalid: %s", deployment.Spec.Runtime.Ref, strings.Join(messages, ", "))
	}
	if deployment.Spec.Runtime.Image != "" {
		if err := resources.ValidatePinnedImage(deployment.Spec.Runtime.Image); err != nil {
			return fmt.Errorf("spec.runtime.image: %w", err)
		}
	}
	if err := validatePositiveQuantity(deployment.Spec.Resources.CPU, "spec.resources.cpu"); err != nil {
		return err
	}
	if err := validatePositiveQuantity(deployment.Spec.Resources.Memory, "spec.resources.memory"); err != nil {
		return err
	}
	if deployment.Spec.Resources.GPU == nil {
		if deployment.Spec.Resources.CPU == "" {
			return errors.New("spec.resources.cpu is required for CPU-only deployments")
		}
		if deployment.Spec.Resources.Memory == "" {
			return errors.New("spec.resources.memory is required for CPU-only deployments")
		}
		if deployment.Spec.Runtime.TensorParallelSize != 0 {
			return errors.New("spec.runtime.tensorParallelSize requires spec.resources.gpu")
		}
		if deployment.Spec.Runtime.GPUMemoryUtilization != 0 {
			return errors.New("spec.runtime.gpuMemoryUtilization requires spec.resources.gpu")
		}
	} else {
		if deployment.Spec.Resources.GPU.Count < 1 {
			return errors.New("spec.resources.gpu.count must be at least 1")
		}
		vendor := deployment.Spec.Resources.GPU.Vendor
		if vendor == "" {
			vendor = templates.DefaultGPUVendor
		}
		resourceName := vendor + ".com/gpu"
		if messages := utilvalidation.IsQualifiedName(resourceName); len(messages) != 0 {
			return fmt.Errorf("spec.resources.gpu.vendor %q produces invalid resource %q: %s",
				vendor, resourceName, strings.Join(messages, ", "))
		}
		if deployment.Spec.Runtime.TensorParallelSize > deployment.Spec.Resources.GPU.Count {
			return fmt.Errorf("spec.runtime.tensorParallelSize (%d) must not exceed spec.resources.gpu.count (%d)",
				deployment.Spec.Runtime.TensorParallelSize, deployment.Spec.Resources.GPU.Count)
		}
	}
	switch deployment.Spec.Activation.DesiredState {
	case "", v1alpha1.ActivationDesiredStateInactive, v1alpha1.ActivationDesiredStateActive:
	default:
		return fmt.Errorf("spec.activation.desiredState %q is invalid", deployment.Spec.Activation.DesiredState)
	}
	switch deployment.Spec.Activation.WhenFull {
	case "", v1alpha1.ActivationWhenFullQueue, v1alpha1.ActivationWhenFullReject,
		v1alpha1.ActivationWhenFullReplaceOldest, v1alpha1.ActivationWhenFullReplaceLowestPriority:
	default:
		return fmt.Errorf("spec.activation.whenFull %q is invalid", deployment.Spec.Activation.WhenFull)
	}
	if deployment.Spec.Activation.DrainTimeout != "" {
		drainTimeout, err := time.ParseDuration(deployment.Spec.Activation.DrainTimeout)
		if err != nil {
			return fmt.Errorf(
				"spec.activation.drainTimeout %q is invalid: %w",
				deployment.Spec.Activation.DrainTimeout,
				err,
			)
		}
		if drainTimeout <= 0 {
			return errors.New("spec.activation.drainTimeout must be greater than zero")
		}
	}
	if deployment.Spec.Scaling.MinReplicas < 0 {
		return errors.New("spec.scaling.minReplicas must not be negative")
	}
	if deployment.Spec.Scaling.MaxReplicas < deployment.Spec.Scaling.MinReplicas {
		return errors.New("spec.scaling.maxReplicas must be greater than or equal to minReplicas")
	}
	if deployment.Spec.Scaling.TargetPendingRequests < 0 {
		return errors.New("spec.scaling.targetPendingRequests must not be negative")
	}
	if deployment.Spec.Scaling.IdleTimeout != "" {
		idleTimeout, err := time.ParseDuration(deployment.Spec.Scaling.IdleTimeout)
		if err != nil {
			return fmt.Errorf(
				"spec.scaling.idleTimeout %q is invalid: %w",
				deployment.Spec.Scaling.IdleTimeout,
				err,
			)
		}
		if idleTimeout <= 0 {
			return errors.New("spec.scaling.idleTimeout must be greater than zero")
		}
	}
	if deployment.Spec.Activation.DesiredState == v1alpha1.ActivationDesiredStateActive &&
		deployment.Spec.Scaling.MaxReplicas < 1 {
		return errors.New("spec.scaling.maxReplicas must be at least 1 for active deployments")
	}
	if deployment.Spec.Cache.Type != "" && deployment.Spec.Cache.Type != "nodeLocal" {
		return fmt.Errorf("spec.cache.type %q is unsupported; expected nodeLocal", deployment.Spec.Cache.Type)
	}
	if err := validatePositiveQuantity(deployment.Spec.Cache.Size, "spec.cache.size"); err != nil {
		return err
	}
	if deployment.Spec.Routing.Path != "" {
		if _, err := routingpath.Normalize(deployment.Spec.Routing.Path); err != nil {
			return fmt.Errorf("spec.routing.path: %w", err)
		}
	}
	if err := validateScheduling(deployment.Spec.Scheduling); err != nil {
		return err
	}
	if err := validateAvailability(deployment); err != nil {
		return err
	}
	if err := validateRollout(deployment.Spec.Rollout); err != nil {
		return err
	}
	return nil
}

func validateRollout(rollout v1alpha1.RolloutSpec) error {
	switch rollout.Strategy {
	case "", v1alpha1.RolloutStrategyRecreate, v1alpha1.RolloutStrategyRolling,
		v1alpha1.RolloutStrategyBlueGreen, v1alpha1.RolloutStrategyCanary:
	default:
		return fmt.Errorf("spec.rollout.strategy %q is invalid", rollout.Strategy)
	}
	switch rollout.WhenCapacityUnavailable {
	case "", v1alpha1.RolloutWhenCapacityUnavailableQueue, v1alpha1.RolloutWhenCapacityUnavailableReject:
	default:
		return fmt.Errorf(
			"spec.rollout.whenCapacityUnavailable %q is invalid",
			rollout.WhenCapacityUnavailable,
		)
	}
	if rollout.CanaryWeightPercent != nil {
		if *rollout.CanaryWeightPercent < 1 || *rollout.CanaryWeightPercent > 99 {
			return errors.New("spec.rollout.canaryWeightPercent must be between 1 and 99")
		}
		if rollout.Strategy != v1alpha1.RolloutStrategyCanary {
			return errors.New("spec.rollout.canaryWeightPercent requires strategy Canary")
		}
	}
	return nil
}

func validateScheduling(scheduling v1alpha1.SchedulingSpec) error {
	if err := validateNodeScheduling(
		scheduling.NodeSelector,
		scheduling.Tolerations,
		"spec.scheduling",
	); err != nil {
		return err
	}

	seenTopologyKeys := make(map[string]struct{}, len(scheduling.TopologySpreadConstraints))
	for i := range scheduling.TopologySpreadConstraints {
		constraint := scheduling.TopologySpreadConstraints[i]
		field := fmt.Sprintf("spec.scheduling.topologySpreadConstraints[%d]", i)
		if constraint.MaxSkew < 1 {
			return fmt.Errorf("%s.maxSkew must be at least 1", field)
		}
		if messages := utilvalidation.IsQualifiedName(constraint.TopologyKey); len(messages) != 0 {
			return fmt.Errorf(
				"%s.topologyKey %q is invalid: %s",
				field,
				constraint.TopologyKey,
				strings.Join(messages, ", "),
			)
		}
		if _, found := seenTopologyKeys[constraint.TopologyKey]; found {
			return fmt.Errorf("%s.topologyKey %q is repeated", field, constraint.TopologyKey)
		}
		seenTopologyKeys[constraint.TopologyKey] = struct{}{}
		switch corev1.UnsatisfiableConstraintAction(constraint.WhenUnsatisfiable) {
		case corev1.DoNotSchedule, corev1.ScheduleAnyway:
		default:
			return fmt.Errorf(
				"%s.whenUnsatisfiable %q is invalid; expected DoNotSchedule or ScheduleAnyway",
				field,
				constraint.WhenUnsatisfiable,
			)
		}
	}
	return nil
}

func validateNodeScheduling(
	nodeSelector map[string]string,
	tolerations []v1alpha1.Toleration,
	fieldPrefix string,
) error {
	for key, value := range nodeSelector {
		if messages := utilvalidation.IsQualifiedName(key); len(messages) != 0 {
			return fmt.Errorf("%s.nodeSelector key %q is invalid: %s", fieldPrefix, key, strings.Join(messages, ", "))
		}
		if messages := utilvalidation.IsValidLabelValue(value); len(messages) != 0 {
			return fmt.Errorf(
				"%s.nodeSelector value %q for key %q is invalid: %s",
				fieldPrefix,
				value,
				key,
				strings.Join(messages, ", "),
			)
		}
	}
	for i := range tolerations {
		toleration := tolerations[i]
		field := fmt.Sprintf("%s.tolerations[%d]", fieldPrefix, i)
		if toleration.Key != "" {
			if messages := utilvalidation.IsQualifiedName(toleration.Key); len(messages) != 0 {
				return fmt.Errorf("%s.key %q is invalid: %s", field, toleration.Key, strings.Join(messages, ", "))
			}
		}
		operator := toleration.Operator
		if operator == "" {
			operator = string(corev1.TolerationOpEqual)
		}
		switch corev1.TolerationOperator(operator) {
		case corev1.TolerationOpEqual:
			if toleration.Key == "" {
				return fmt.Errorf("%s.key is required when operator is Equal", field)
			}
		case corev1.TolerationOpExists:
			if toleration.Value != "" {
				return fmt.Errorf("%s.value must be empty when operator is Exists", field)
			}
		default:
			return fmt.Errorf("%s.operator %q is invalid; expected Equal or Exists", field, toleration.Operator)
		}
		if toleration.Value != "" {
			if messages := utilvalidation.IsValidLabelValue(toleration.Value); len(messages) != 0 {
				return fmt.Errorf("%s.value %q is invalid: %s", field, toleration.Value, strings.Join(messages, ", "))
			}
		}
		switch corev1.TaintEffect(toleration.Effect) {
		case "", corev1.TaintEffectNoSchedule, corev1.TaintEffectPreferNoSchedule, corev1.TaintEffectNoExecute:
		default:
			return fmt.Errorf(
				"%s.effect %q is invalid; expected NoSchedule, PreferNoSchedule, or NoExecute",
				field,
				toleration.Effect,
			)
		}
		if toleration.TolerationSeconds != nil {
			if *toleration.TolerationSeconds < 0 {
				return fmt.Errorf("%s.tolerationSeconds must not be negative", field)
			}
			if corev1.TaintEffect(toleration.Effect) != corev1.TaintEffectNoExecute {
				return fmt.Errorf("%s.tolerationSeconds requires effect NoExecute", field)
			}
		}
	}
	return nil
}

func validateAvailability(deployment v1alpha1.ModelDeployment) error {
	minAvailable := deployment.Spec.Availability.PodDisruptionBudget.MinAvailable
	if minAvailable == nil {
		return nil
	}
	if *minAvailable < 0 {
		return errors.New("spec.availability.podDisruptionBudget.minAvailable must not be negative")
	}
	replicas := deployment.Spec.Scaling.MinReplicas
	if replicas < 1 {
		replicas = 1
	}
	if *minAvailable > replicas {
		return fmt.Errorf(
			"spec.availability.podDisruptionBudget.minAvailable (%d) must not exceed runtime replicas (%d)",
			*minAvailable,
			replicas,
		)
	}
	return nil
}

func validatePositiveQuantity(value, field string) error {
	if value == "" {
		return nil
	}
	quantity, err := resource.ParseQuantity(value)
	if err != nil {
		return fmt.Errorf("%s %q is invalid: %w", field, value, err)
	}
	if quantity.Sign() <= 0 {
		return fmt.Errorf("%s must be greater than zero", field)
	}
	return nil
}

// ReconciliationValidator validates ModelDeployment objects at reconciliation
// time using operator configuration that is not available to CRD OpenAPI
// validation.
type ReconciliationValidator struct {
	cacheRoot string
}

// NewReconciliationValidator creates a validator with the configured cache root.
func NewReconciliationValidator(cacheRoot string) (*ReconciliationValidator, error) {
	cleanRoot, err := paths.CleanAbsolutePath(cacheRoot, "cache root")
	if err != nil {
		return nil, err
	}
	if cleanRoot == "/" {
		return nil, errors.New("cache root must not be the filesystem root")
	}
	return &ReconciliationValidator{cacheRoot: cleanRoot}, nil
}

// ValidateForReconciliation checks cross-object and configuration-dependent
// rules.  It assumes static validation has already passed.
func (v *ReconciliationValidator) ValidateForReconciliation(deployment *v1alpha1.ModelDeployment) error {
	if v == nil {
		return errors.New("reconciliation validator is required")
	}
	if deployment == nil {
		return errors.New("model deployment is required")
	}

	if err := v.validateDrainTimeout(deployment.Spec.Activation.DrainTimeout); err != nil {
		return err
	}

	if err := v.validateCachePath(deployment.Spec.Cache.Path); err != nil {
		return err
	}

	if err := v.validateSecrets(deployment); err != nil {
		return err
	}

	return nil
}

func (v *ReconciliationValidator) validateDrainTimeout(drainTimeout string) error {
	if drainTimeout == "" {
		return nil
	}
	parsed, err := time.ParseDuration(drainTimeout)
	if err != nil {
		return &ValidationError{
			Reason:  v1alpha1.ReasonInvalidDrainTimeout,
			Message: fmt.Sprintf("spec.activation.drainTimeout %q is invalid: %v", drainTimeout, err),
		}
	}
	if parsed <= 0 {
		return &ValidationError{
			Reason:  v1alpha1.ReasonInvalidDrainTimeout,
			Message: "spec.activation.drainTimeout must be greater than zero",
		}
	}
	return nil
}

func (v *ReconciliationValidator) validateCachePath(cachePath string) error {
	if cachePath == "" {
		return nil
	}
	cleanPath, err := paths.CleanAbsolutePath(cachePath, "spec.cache.path")
	if err != nil {
		return &ValidationError{
			Reason:  v1alpha1.ReasonInvalidCachePath,
			Message: err.Error(),
		}
	}
	if err := paths.UnderRoot(cleanPath, v.cacheRoot, "spec.cache.path"); err != nil {
		return &ValidationError{
			Reason:  v1alpha1.ReasonInvalidCachePath,
			Message: err.Error(),
		}
	}
	return nil
}

func (v *ReconciliationValidator) validateSecrets(deployment *v1alpha1.ModelDeployment) error {
	if deployment.Spec.Model.Source != "huggingface" {
		return nil
	}
	secretName := deployment.Spec.Secrets.HuggingFaceTokenSecretName
	if secretName == "" {
		// Public Hugging Face repositories do not require credentials.
		return nil
	}
	if messages := utilvalidation.IsDNS1123Subdomain(secretName); len(messages) != 0 {
		return &ValidationError{
			Reason: v1alpha1.ReasonSecretRequired,
			Message: fmt.Sprintf(
				"spec.secrets.huggingFaceTokenSecretName %q is invalid: %s",
				secretName,
				strings.Join(messages, ", "),
			),
		}
	}
	return nil
}

// ValidateModelRuntime validates a runtime definition.
func ValidateModelRuntime(runtime v1alpha1.ModelRuntime) error {
	if runtime.Name == "" {
		return errors.New("metadata.name is required")
	}
	if runtime.Spec.Engine == "" {
		return errors.New("spec.engine is required")
	}
	if runtime.Spec.Protocol == "" {
		return errors.New("spec.protocol is required")
	}
	if runtime.Spec.Protocol != v1alpha1.ModelRuntimeProtocolOpenAI {
		return fmt.Errorf(
			"spec.protocol %q is unsupported; expected %q",
			runtime.Spec.Protocol,
			v1alpha1.ModelRuntimeProtocolOpenAI,
		)
	}
	if runtime.Spec.DefaultImage == "" {
		return errors.New("spec.defaultImage is required")
	}
	if err := resources.ValidatePinnedImage(runtime.Spec.DefaultImage); err != nil {
		return fmt.Errorf("spec.defaultImage: %w", err)
	}
	if runtime.Spec.Port < 1 || runtime.Spec.Port > 65535 {
		return errors.New("spec.port must be between 1 and 65535")
	}
	if runtime.Spec.HealthPath == "" {
		return errors.New("spec.healthPath is required")
	}
	for field, path := range map[string]string{
		"spec.healthPath":    runtime.Spec.HealthPath,
		"spec.readinessPath": runtime.Spec.ReadinessPath,
		"spec.metricsPath":   runtime.Spec.MetricsPath,
	} {
		if path != "" && !strings.HasPrefix(path, "/") {
			return fmt.Errorf("%s must start with /", field)
		}
	}
	for name := range runtime.Spec.Env {
		if messages := utilvalidation.IsEnvVarName(name); len(messages) != 0 {
			return fmt.Errorf("spec.env key %q is invalid: %s", name, strings.Join(messages, ", "))
		}
	}
	return nil
}

// ValidateModelCache validates a cache definition.
func ValidateModelCache(cache v1alpha1.ModelCache) error {
	if cache.Name == "" {
		return errors.New("metadata.name is required")
	}
	if cache.Spec.ModelRepo == "" {
		return errors.New("spec.modelRepo is required")
	}
	if cache.Spec.Storage.Type != "nodeLocal" {
		return fmt.Errorf("spec.storage.type %q is unsupported; expected nodeLocal", cache.Spec.Storage.Type)
	}
	if cache.Spec.Storage.Size == "" {
		return errors.New("spec.storage.size is required")
	}
	size, err := resource.ParseQuantity(cache.Spec.Storage.Size)
	if err != nil {
		return fmt.Errorf("spec.storage.size %q is invalid: %w", cache.Spec.Storage.Size, err)
	}
	if size.Sign() <= 0 {
		return errors.New("spec.storage.size must be greater than zero")
	}
	if cache.Spec.Storage.Path == "" {
		return errors.New("spec.storage.path is required")
	}
	if _, err := paths.CleanAbsolutePath(cache.Spec.Storage.Path, "spec.storage.path"); err != nil {
		return err
	}
	if err := validateNodeScheduling(
		cache.Spec.Storage.NodeSelector,
		cache.Spec.Storage.Tolerations,
		"spec.storage",
	); err != nil {
		return err
	}
	if cache.Spec.SecretRef != "" {
		if messages := utilvalidation.IsDNS1123Subdomain(cache.Spec.SecretRef); len(messages) != 0 {
			return fmt.Errorf("spec.secretRef %q is invalid: %s", cache.Spec.SecretRef, strings.Join(messages, ", "))
		}
	}
	return nil
}
