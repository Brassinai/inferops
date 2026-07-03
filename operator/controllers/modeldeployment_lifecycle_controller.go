package controllers

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	"github.com/brassinai/inferops/operator/internal/cachecontract"
	"github.com/brassinai/inferops/operator/internal/events"
	controllermetrics "github.com/brassinai/inferops/operator/internal/metrics"
	"github.com/brassinai/inferops/operator/internal/resources"
	inferopsruntime "github.com/brassinai/inferops/operator/internal/runtime"
	"github.com/brassinai/inferops/operator/internal/scheduler"
	"github.com/brassinai/inferops/operator/internal/status"
	"github.com/brassinai/inferops/operator/internal/templates"
	"github.com/brassinai/inferops/operator/internal/validation"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	deploymentRequeueAfter = 10 * time.Second
	waitingRequeueAfter    = 30 * time.Second
)

var errPermanentLifecycle = errors.New("permanent lifecycle configuration error")

// ModelDeploymentControllerConfig contains trusted lifecycle configuration.
type ModelDeploymentControllerConfig struct {
	CacheRoot        string
	DefaultCacheSize string
	DownloaderImage  string
	GPUNodeSelector  map[string]string
	GPUTypeLabel     string
}

// ModelDeploymentController reconciles ModelDeployment resources into stable
// Services, persistent cache declarations, and activation-gated Deployments.
type ModelDeploymentController struct {
	client           client.Client
	builder          resources.Builder
	validator        *ModelDeploymentReconciler
	gpuPlanner       *scheduler.GPUPlanner
	cacheRoot        string
	defaultCacheSize string
	eventRecorder    record.EventRecorder
	metrics          controllermetrics.Recorder
	// queueMetricDirty is safe without a mutex because SetupWithManager
	// serializes this controller's reconciliations.
	queueMetricDirty bool
}

// NewModelDeploymentController creates the Kubernetes lifecycle controller.
func NewModelDeploymentController(
	c client.Client,
	scheme *runtime.Scheme,
	config ModelDeploymentControllerConfig,
	eventRecorder record.EventRecorder,
	metricsRecorder controllermetrics.Recorder,
) (*ModelDeploymentController, error) {
	if c == nil {
		return nil, errors.New("client is required")
	}
	if scheme == nil {
		return nil, errors.New("scheme is required")
	}
	if eventRecorder == nil {
		return nil, errors.New("event recorder is required")
	}
	if config.DefaultCacheSize == "" {
		config.DefaultCacheSize = "100Gi"
	}
	builder, err := resources.NewBuilder(resources.BuilderOptions{
		CacheRoot:            config.CacheRoot,
		CacheDownloaderImage: config.DownloaderImage,
	})
	if err != nil {
		return nil, fmt.Errorf("create resource builder: %w", err)
	}
	reconciliationValidator, err := validation.NewReconciliationValidator(config.CacheRoot)
	if err != nil {
		return nil, fmt.Errorf("create reconciliation validator: %w", err)
	}
	gpuPlanner, err := scheduler.NewGPUPlanner(scheduler.GPUPlannerConfig{
		NodeSelector: config.GPUNodeSelector,
		GPUTypeLabel: config.GPUTypeLabel,
	})
	if err != nil {
		return nil, fmt.Errorf("create GPU planner: %w", err)
	}
	if metricsRecorder == nil {
		metricsRecorder = controllermetrics.NoOpRecorder{}
	}
	getter := kubernetesRuntimeGetter{client: c}
	return &ModelDeploymentController{
		client:  c,
		builder: builder,
		validator: NewModelDeploymentReconciler(
			inferopsruntime.NewResolver(getter),
			reconciliationValidator,
			events.NoOpRecorder{},
		),
		gpuPlanner:       gpuPlanner,
		cacheRoot:        config.CacheRoot,
		defaultCacheSize: config.DefaultCacheSize,
		eventRecorder:    eventRecorder,
		metrics:          metricsRecorder,
	}, nil
}

// Reconcile implements controller-runtime reconciliation.
func (r *ModelDeploymentController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("modeldeployment", req.NamespacedName)
	if r.queueMetricDirty {
		if err := r.refreshQueueDepth(ctx); err != nil {
			return ctrl.Result{}, err
		}
	}
	var deployment v1alpha1.ModelDeployment
	if err := r.client.Get(ctx, req.NamespacedName, &deployment); err != nil {
		if apierrors.IsNotFound(err) {
			r.queueMetricDirty = true
			if err := r.refreshQueueDepth(ctx); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get ModelDeployment: %w", err)
	}
	if !deployment.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	result, err := r.reconcile(ctx, &deployment)
	if err != nil {
		logger.Error(err, "lifecycle reconciliation failed")
		return result, err
	}
	return result, nil
}

func (r *ModelDeploymentController) reconcile(
	ctx context.Context,
	deployment *v1alpha1.ModelDeployment,
) (ctrl.Result, error) {
	original := deployment.DeepCopy()
	validated, err := r.validator.Reconcile(ctx, deployment)
	if err != nil {
		return ctrl.Result{}, err
	}
	deployment.Status = validated.Status
	if deployment.Status.Phase == v1alpha1.ModelDeploymentPhaseFailed {
		if _, err := r.deleteRuntimeWorkload(ctx, deployment); err != nil {
			return ctrl.Result{}, err
		}
		reason := v1alpha1.ReasonSpecInvalid
		if ready, found := status.FindCondition(deployment.Status.Conditions, v1alpha1.ConditionReady); found {
			reason = ready.Reason
		}
		deployment.Status.AssignedNode = ""
		deployment.Status.AssignedGPUs = nil
		deployment.Status.Replicas = v1alpha1.ReplicaStatus{}
		deployment.Status.Model.Loaded = false
		setDeploymentCondition(deployment, v1alpha1.ConditionCacheReady, metav1.ConditionUnknown,
			reason, "Cache lifecycle is blocked by deployment validation")
		setRuntimeWaitingConditions(deployment, reason, "Runtime lifecycle is blocked by deployment validation")
		deployment.Status.Phase = v1alpha1.ModelDeploymentPhaseFailed
		return r.patchDeploymentStatus(ctx, deployment, original, reason)
	}

	modelRuntime, err := r.getModelRuntime(ctx, deployment)
	if err != nil {
		return ctrl.Result{}, err
	}
	if _, err := r.ensureService(ctx, deployment, modelRuntime); err != nil {
		if errors.Is(err, errPermanentLifecycle) {
			if _, deleteErr := r.deleteRuntimeWorkload(ctx, deployment); deleteErr != nil {
				return ctrl.Result{}, deleteErr
			}
			return r.failDeployment(ctx, deployment, original, v1alpha1.ReasonRuntimeUnavailable, err.Error())
		}
		return ctrl.Result{}, err
	}
	if _, err := r.ensureConfigMap(ctx, deployment, modelRuntime); err != nil {
		if errors.Is(err, errPermanentLifecycle) {
			if _, deleteErr := r.deleteRuntimeWorkload(ctx, deployment); deleteErr != nil {
				return ctrl.Result{}, deleteErr
			}
			return r.failDeployment(ctx, deployment, original, v1alpha1.ReasonRuntimeUnavailable, err.Error())
		}
		return ctrl.Result{}, err
	}
	r.setStableIdentity(deployment)

	cache, created, err := r.ensureModelCache(ctx, deployment)
	if err != nil {
		if errors.Is(err, errPermanentLifecycle) {
			if _, deleteErr := r.deleteRuntimeWorkload(ctx, deployment); deleteErr != nil {
				return ctrl.Result{}, deleteErr
			}
			return r.failDeployment(ctx, deployment, original, v1alpha1.ReasonCacheFailed, err.Error())
		}
		return ctrl.Result{}, err
	}
	if created {
		r.projectCachePending(deployment, cache)
		return r.patchDeploymentStatus(ctx, deployment, original, v1alpha1.ReasonCachePending)
	}

	cacheResult := cachecontract.Lookup(cache)
	if !cacheResult.Ready {
		if _, err := r.deleteRuntimeWorkload(ctx, deployment); err != nil {
			return ctrl.Result{}, err
		}
		deployment.Status.AssignedNode = ""
		deployment.Status.AssignedGPUs = nil
		deployment.Status.Replicas = v1alpha1.ReplicaStatus{}
		deployment.Status.Model.Loaded = false
		deployment.Status.Cache = v1alpha1.ModelCacheSummary{
			State:    string(cache.Status.Phase),
			NodeName: cache.Status.NodeName,
			Path:     cache.Status.Path,
		}
		switch cache.Status.Phase {
		case v1alpha1.ModelCachePhaseFailed:
			message := fmt.Sprintf("ModelCache %q failed", cache.Name)
			if condition, found := status.FindCondition(cache.Status.Conditions, v1alpha1.CacheConditionReady); found &&
				condition.Message != "" {
				message = condition.Message
			}
			setDeploymentCondition(deployment, v1alpha1.ConditionCacheReady, metav1.ConditionFalse,
				v1alpha1.ReasonCacheFailed, message)
			setRuntimeWaitingConditions(deployment, v1alpha1.ReasonCacheFailed,
				"Runtime is blocked by the failed model cache")
			return r.failDeployment(
				ctx,
				deployment,
				original,
				v1alpha1.ReasonCacheFailed,
				message,
			)
		case v1alpha1.ModelCachePhaseDownloading:
			deployment.Status.Phase = v1alpha1.ModelDeploymentPhaseDownloading
			setDeploymentCondition(deployment, v1alpha1.ConditionCacheReady, metav1.ConditionFalse,
				v1alpha1.ReasonCacheDownloading, fmt.Sprintf("ModelCache %q is downloading", cache.Name))
			setRuntimeWaitingConditions(deployment, v1alpha1.ReasonCacheDownloading, "Runtime is waiting for the model cache")
			return r.patchDeploymentStatus(ctx, deployment, original, "")
		default:
			r.projectCachePending(deployment, cache)
			return r.patchDeploymentStatus(ctx, deployment, original, "")
		}
	}
	r.projectReadyCache(deployment, cacheResult)

	if effectiveDesiredState(deployment) == v1alpha1.ActivationDesiredStateInactive {
		deleted, err := r.deleteRuntimeWorkload(ctx, deployment)
		if err != nil {
			return ctrl.Result{}, err
		}
		if original.Status.Phase == v1alpha1.ModelDeploymentPhaseActive ||
			original.Status.Phase == v1alpha1.ModelDeploymentPhaseActivating {
			if err := r.touchCacheLastUsed(ctx, cache); err != nil {
				return ctrl.Result{}, err
			}
		}
		deployment.Status.Phase = v1alpha1.ModelDeploymentPhaseCached
		deployment.Status.AssignedNode = ""
		deployment.Status.AssignedGPUs = nil
		deployment.Status.Replicas = v1alpha1.ReplicaStatus{}
		deployment.Status.Model.Loaded = false
		if deployment.Spec.Resources.GPU != nil {
			setDeploymentCondition(deployment, v1alpha1.ConditionGPUAssigned, metav1.ConditionFalse,
				v1alpha1.ReasonInactive, "Inactive deployment does not reserve GPU capacity")
		} else {
			status.RemoveCondition(&deployment.Status.Conditions, v1alpha1.ConditionGPUAssigned)
		}
		setDeploymentCondition(deployment, v1alpha1.ConditionRuntimeReady, metav1.ConditionFalse,
			v1alpha1.ReasonInactive, "Runtime is not created while the deployment is inactive")
		setDeploymentCondition(deployment, v1alpha1.ConditionModelLoaded, metav1.ConditionFalse,
			v1alpha1.ReasonInactive, "Model is cached and inactive")
		setDeploymentCondition(deployment, v1alpha1.ConditionRoutingReady, metav1.ConditionFalse,
			v1alpha1.ReasonRouteDisabled, "Gateway routing remains disabled while inactive")
		setReadyFalse(&deployment.Status, v1alpha1.ReasonInactive, "Model is cached and inactive")
		reason := ""
		if deleted || original.Status.Phase != v1alpha1.ModelDeploymentPhaseCached {
			reason = v1alpha1.ReasonInactive
		}
		return r.patchDeploymentStatus(ctx, deployment, original, reason)
	}

	assignedNode := cacheResult.NodeName
	var runtimeNode corev1.Node
	if deployment.Spec.Resources.GPU != nil {
		placement, err := r.reserveGPU(ctx, deployment, cacheResult.NodeName)
		if err != nil {
			return r.handleGPUCapacityError(ctx, deployment, original, err)
		}
		assignedNode = placement.NodeName
		if err := r.client.Get(ctx, types.NamespacedName{Name: assignedNode}, &runtimeNode); err != nil {
			return ctrl.Result{}, fmt.Errorf("get selected GPU node for preflight: %w", err)
		}
		deployment.Status.AssignedNode = assignedNode
		deployment.Status.AssignedGPUs = nil
		setDeploymentCondition(deployment, v1alpha1.ConditionGPUAssigned, metav1.ConditionTrue,
			v1alpha1.ReasonGPUCapacityReserved,
			fmt.Sprintf("Reserved %d %s slot(s) on node %s; Kubernetes assigns physical devices",
				effectiveGPUReservation(deployment), gpuResourceName(deployment), assignedNode))
	} else {
		if err := r.client.Get(ctx, types.NamespacedName{Name: assignedNode}, &runtimeNode); err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("get cache node for CPU activation: %w", err)
			}
		}
		if runtimeNode.Name == "" || !scheduler.IsNodeEligibleForCache(runtimeNode) {
			if _, err := r.deleteRuntimeWorkload(ctx, deployment); err != nil {
				return ctrl.Result{}, err
			}
			deployment.Status.Phase = v1alpha1.ModelDeploymentPhaseWaitingForCapacity
			deployment.Status.AssignedNode = ""
			deployment.Status.Replicas = v1alpha1.ReplicaStatus{
				Desired: effectiveRuntimeReplicas(deployment),
			}
			setRuntimeWaitingConditions(deployment, v1alpha1.ReasonWaitingForCapacity,
				fmt.Sprintf("Cache node %q is not Ready and schedulable", assignedNode))
			return r.patchDeploymentStatus(ctx, deployment, original, v1alpha1.ReasonWaitingForCapacity)
		}
		deployment.Status.AssignedNode = assignedNode
		deployment.Status.AssignedGPUs = nil
		status.RemoveCondition(&deployment.Status.Conditions, v1alpha1.ConditionGPUAssigned)
	}

	if err := scheduler.ValidateRuntimeNode(deployment, &runtimeNode); err != nil {
		return r.handleRuntimePreflightError(ctx, deployment, original, err)
	}
	if _, err := r.ensurePodDisruptionBudget(ctx, deployment); err != nil {
		if errors.Is(err, errPermanentLifecycle) {
			if _, deleteErr := r.deleteRuntimeWorkload(ctx, deployment); deleteErr != nil {
				return ctrl.Result{}, deleteErr
			}
			return r.failDeployment(ctx, deployment, original, v1alpha1.ReasonRuntimeUnavailable, err.Error())
		}
		return ctrl.Result{}, err
	}
	runtimeDeployment, created, err := r.ensureRuntimeDeployment(
		ctx,
		deployment,
		modelRuntime,
		assignedNode,
		cacheResult.Path,
	)
	if err != nil {
		if !errors.Is(err, errPermanentLifecycle) {
			return ctrl.Result{}, err
		}
		if _, deleteErr := r.deleteRuntimeWorkload(ctx, deployment); deleteErr != nil {
			return ctrl.Result{}, deleteErr
		}
		return r.failDeployment(ctx, deployment, original, v1alpha1.ReasonRuntimeUnavailable, err.Error())
	}
	if (created ||
		(original.Status.Phase != v1alpha1.ModelDeploymentPhaseActivating &&
			original.Status.Phase != v1alpha1.ModelDeploymentPhaseActive)) &&
		(cache.Status.LastUsedTime.IsZero() ||
			(!runtimeDeployment.CreationTimestamp.IsZero() &&
				cache.Status.LastUsedTime.Before(&runtimeDeployment.CreationTimestamp))) {
		if err := r.touchCacheLastUsed(ctx, cache); err != nil {
			return ctrl.Result{}, err
		}
	}
	return r.projectRuntimeDeployment(ctx, deployment, original, runtimeDeployment, created)
}

func (r *ModelDeploymentController) handleRuntimePreflightError(
	ctx context.Context,
	deployment, original *v1alpha1.ModelDeployment,
	preflightErr error,
) (ctrl.Result, error) {
	if _, err := r.deleteRuntimeWorkload(ctx, deployment); err != nil {
		return ctrl.Result{}, err
	}
	deployment.Status.AssignedNode = ""
	deployment.Status.AssignedGPUs = nil
	deployment.Status.Replicas = v1alpha1.ReplicaStatus{
		Desired: effectiveRuntimeReplicas(deployment),
	}
	deployment.Status.Model.Loaded = false

	if errors.Is(preflightErr, scheduler.ErrSchedulingConstraints) {
		setRuntimeWaitingConditions(
			deployment,
			v1alpha1.ReasonSchedulingBlocked,
			preflightErr.Error(),
		)
		return r.failDeployment(
			ctx,
			deployment,
			original,
			v1alpha1.ReasonSchedulingBlocked,
			preflightErr.Error(),
		)
	}
	if !errors.Is(preflightErr, scheduler.ErrInsufficientComputeCapacity) {
		return ctrl.Result{}, preflightErr
	}

	setRuntimeWaitingConditions(
		deployment,
		v1alpha1.ReasonInsufficientCompute,
		preflightErr.Error(),
	)
	if effectiveWhenFull(deployment) == v1alpha1.ActivationWhenFullQueue {
		deployment.Status.Phase = v1alpha1.ModelDeploymentPhaseWaitingForCapacity
		return r.patchDeploymentStatus(
			ctx,
			deployment,
			original,
			v1alpha1.ReasonInsufficientCompute,
		)
	}
	return r.failDeployment(
		ctx,
		deployment,
		original,
		v1alpha1.ReasonInsufficientCompute,
		preflightErr.Error(),
	)
}

func (r *ModelDeploymentController) setStableIdentity(
	deployment *v1alpha1.ModelDeployment,
) {
	deployment.Status.ServiceName = templates.RuntimeServiceName(deployment.Name)
	deployment.Status.Endpoint = templates.GatewayOpenAIBasePath(deployment.Name)
	if !deployment.Spec.Routing.OpenAICompatible {
		deployment.Status.Endpoint = templates.GatewayModelPath(deployment.Name)
	}
	if deployment.Spec.Routing.Path != "" {
		deployment.Status.Endpoint = strings.TrimSuffix(deployment.Spec.Routing.Path, "/")
		if deployment.Spec.Routing.OpenAICompatible {
			deployment.Status.Endpoint += templates.OpenAIPathPrefix
		}
	}
	deployment.Status.Model.Repo = deployment.Spec.Model.Repo
}

func (r *ModelDeploymentController) ensureModelCache(
	ctx context.Context,
	deployment *v1alpha1.ModelDeployment,
) (*v1alpha1.ModelCache, bool, error) {
	name, err := cachecontract.Name(deployment)
	if err != nil {
		return nil, false, permanentLifecycleError(err)
	}
	cachePath, err := cachecontract.Path(deployment, r.cacheRoot)
	if err != nil {
		return nil, false, permanentLifecycleError(err)
	}
	size := deployment.Spec.Cache.Size
	if size == "" {
		size = r.defaultCacheSize
	}
	desiredSpec := v1alpha1.ModelCacheSpec{
		ModelRepo: deployment.Spec.Model.Repo,
		Revision:  deployment.Spec.Model.Revision,
		Storage: v1alpha1.ModelCacheStorage{
			Type:         "nodeLocal",
			Size:         size,
			Path:         cachePath,
			NodeSelector: copyStringMap(deployment.Spec.Scheduling.NodeSelector),
			Tolerations:  copyTolerations(deployment.Spec.Scheduling.Tolerations),
		},
		SecretRef: deployment.Spec.Secrets.HuggingFaceTokenSecretName,
	}
	key := types.NamespacedName{Namespace: deployment.Namespace, Name: name}
	var cache v1alpha1.ModelCache
	if err := r.client.Get(ctx, key, &cache); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, false, fmt.Errorf("get ModelCache %q: %w", name, err)
		}
		cache = v1alpha1.ModelCache{
			TypeMeta: metav1.TypeMeta{
				APIVersion: v1alpha1.GroupVersion.String(),
				Kind:       "ModelCache",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: deployment.Namespace,
				Labels: map[string]string{
					resources.LabelPartOf:          resources.ValuePartOf,
					resources.LabelManagedBy:       resources.ValueManagedBy,
					resources.LabelModelDeployment: deployment.Name,
				},
			},
			Spec: desiredSpec,
		}
		if err := r.client.Create(ctx, &cache); err != nil {
			return nil, false, fmt.Errorf("create ModelCache %q: %w", name, err)
		}
		r.eventRecorder.Eventf(deployment, corev1.EventTypeNormal, v1alpha1.ReasonCachePending,
			"Created ModelCache %q", name)
		return &cache, true, nil
	}

	if !reflect.DeepEqual(cache.Spec, desiredSpec) {
		if cache.Status.Phase == v1alpha1.ModelCachePhaseReady &&
			!readyCacheSpecsCompatible(cache.Spec, desiredSpec) {
			return nil, false, permanentLifecycleError(fmt.Errorf(
				"ModelCache %q is ready with a different immutable cache contract",
				cache.Name,
			))
		}
		before := cache.DeepCopy()
		cache.Spec = desiredSpec
		ensureStringMapValue(&cache.Labels, resources.LabelModelDeployment, deployment.Name)
		ensureStringMapValue(&cache.Labels, resources.LabelManagedBy, resources.ValueManagedBy)
		if err := r.client.Patch(ctx, &cache, client.MergeFrom(before)); err != nil {
			return nil, false, fmt.Errorf("patch ModelCache %q: %w", name, err)
		}
	} else if cache.Labels[resources.LabelModelDeployment] != deployment.Name {
		before := cache.DeepCopy()
		ensureStringMapValue(&cache.Labels, resources.LabelModelDeployment, deployment.Name)
		if err := r.client.Patch(ctx, &cache, client.MergeFrom(before)); err != nil {
			return nil, false, fmt.Errorf("label ModelCache %q: %w", name, err)
		}
	}
	return &cache, false, nil
}

func (r *ModelDeploymentController) ensureService(
	ctx context.Context,
	deployment *v1alpha1.ModelDeployment,
	modelRuntime *v1alpha1.ModelRuntime,
) (*corev1.Service, error) {
	desired, err := resources.BuildRuntimeService(deployment, modelRuntime)
	if err != nil {
		return nil, permanentLifecycleError(fmt.Errorf("build runtime Service: %w", err))
	}
	var existing corev1.Service
	key := types.NamespacedName{Namespace: desired.Namespace, Name: desired.Name}
	if err := r.client.Get(ctx, key, &existing); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("get runtime Service: %w", err)
		}
		if err := r.client.Create(ctx, desired); err != nil {
			return nil, fmt.Errorf("create runtime Service: %w", err)
		}
		return desired, nil
	}
	if err := assertControlledBy(&existing, deployment); err != nil {
		return nil, permanentLifecycleError(err)
	}
	before := existing.DeepCopy()
	mergeLabels(&existing.Labels, desired.Labels)
	existing.OwnerReferences = desired.OwnerReferences
	existing.Spec.Type = desired.Spec.Type
	existing.Spec.Selector = desired.Spec.Selector
	existing.Spec.Ports = desired.Spec.Ports
	if !reflect.DeepEqual(before, &existing) {
		if err := r.client.Patch(ctx, &existing, client.MergeFrom(before)); err != nil {
			return nil, fmt.Errorf("patch runtime Service: %w", err)
		}
	}
	return &existing, nil
}

func (r *ModelDeploymentController) ensureConfigMap(
	ctx context.Context,
	deployment *v1alpha1.ModelDeployment,
	modelRuntime *v1alpha1.ModelRuntime,
) (*corev1.ConfigMap, error) {
	desired, err := resources.BuildRuntimeConfigMap(deployment, modelRuntime)
	if err != nil {
		return nil, permanentLifecycleError(fmt.Errorf("build runtime ConfigMap: %w", err))
	}
	resolvedCachePath, err := cachecontract.Path(deployment, r.cacheRoot)
	if err != nil {
		return nil, permanentLifecycleError(fmt.Errorf("resolve runtime ConfigMap cache path: %w", err))
	}
	desired.Data["cache.path"] = resolvedCachePath
	var existing corev1.ConfigMap
	key := types.NamespacedName{Namespace: desired.Namespace, Name: desired.Name}
	if err := r.client.Get(ctx, key, &existing); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("get runtime ConfigMap: %w", err)
		}
		if err := r.client.Create(ctx, desired); err != nil {
			return nil, fmt.Errorf("create runtime ConfigMap: %w", err)
		}
		return desired, nil
	}
	if err := assertControlledBy(&existing, deployment); err != nil {
		return nil, permanentLifecycleError(err)
	}
	before := existing.DeepCopy()
	mergeLabels(&existing.Labels, desired.Labels)
	existing.OwnerReferences = desired.OwnerReferences
	existing.Data = desired.Data
	if !reflect.DeepEqual(before, &existing) {
		if err := r.client.Patch(ctx, &existing, client.MergeFrom(before)); err != nil {
			return nil, fmt.Errorf("patch runtime ConfigMap: %w", err)
		}
	}
	return &existing, nil
}

func (r *ModelDeploymentController) ensureRuntimeDeployment(
	ctx context.Context,
	deployment *v1alpha1.ModelDeployment,
	modelRuntime *v1alpha1.ModelRuntime,
	nodeName, cachePath string,
) (*appsv1.Deployment, bool, error) {
	desired, err := r.builder.BuildRuntimeDeployment(deployment, modelRuntime, nodeName, cachePath)
	if err != nil {
		return nil, false, permanentLifecycleError(fmt.Errorf("build runtime Deployment: %w", err))
	}
	var existing appsv1.Deployment
	key := types.NamespacedName{Namespace: desired.Namespace, Name: desired.Name}
	if err := r.client.Get(ctx, key, &existing); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, false, fmt.Errorf("get runtime Deployment: %w", err)
		}
		if err := r.client.Create(ctx, desired); err != nil {
			return nil, false, fmt.Errorf("create runtime Deployment: %w", err)
		}
		r.eventRecorder.Eventf(deployment, corev1.EventTypeNormal, v1alpha1.ReasonRuntimeCreating,
			"Created runtime Deployment %q on node %q", desired.Name, nodeName)
		return desired, true, nil
	}
	if err := assertControlledBy(&existing, deployment); err != nil {
		return nil, false, permanentLifecycleError(err)
	}
	changed := !reflect.DeepEqual(existing.Spec, desired.Spec) ||
		!reflect.DeepEqual(existing.OwnerReferences, desired.OwnerReferences) ||
		!containsLabels(existing.Labels, desired.Labels)
	if !changed {
		return &existing, false, nil
	}
	before := existing.DeepCopy()
	mergeLabels(&existing.Labels, desired.Labels)
	existing.OwnerReferences = desired.OwnerReferences
	existing.Spec = desired.Spec
	patch := client.MergeFrom(before)
	data, err := patch.Data(&existing)
	if err != nil {
		return nil, false, fmt.Errorf("build runtime Deployment patch: %w", err)
	}
	if string(data) == "{}" {
		return &existing, false, nil
	}
	if err := r.client.Patch(ctx, &existing, patch); err != nil {
		return nil, false, fmt.Errorf("patch runtime Deployment: %w", err)
	}
	return &existing, false, nil
}

func (r *ModelDeploymentController) ensurePodDisruptionBudget(
	ctx context.Context,
	deployment *v1alpha1.ModelDeployment,
) (*policyv1.PodDisruptionBudget, error) {
	desired, err := resources.BuildRuntimePodDisruptionBudget(deployment)
	if err != nil {
		return nil, permanentLifecycleError(fmt.Errorf("build runtime PodDisruptionBudget: %w", err))
	}
	key := types.NamespacedName{
		Namespace: deployment.Namespace,
		Name:      templates.RuntimeServiceName(deployment.Name),
	}
	var existing policyv1.PodDisruptionBudget
	if err := r.client.Get(ctx, key, &existing); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("get runtime PodDisruptionBudget: %w", err)
		}
		if desired == nil {
			return nil, nil
		}
		if err := r.client.Create(ctx, desired); err != nil {
			return nil, fmt.Errorf("create runtime PodDisruptionBudget: %w", err)
		}
		return desired, nil
	}
	if err := assertControlledBy(&existing, deployment); err != nil {
		return nil, permanentLifecycleError(err)
	}
	if desired == nil {
		if err := r.client.Delete(ctx, &existing); err != nil && !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("delete disabled runtime PodDisruptionBudget: %w", err)
		}
		return nil, nil
	}
	before := existing.DeepCopy()
	mergeLabels(&existing.Labels, desired.Labels)
	existing.OwnerReferences = desired.OwnerReferences
	existing.Spec = desired.Spec
	if !reflect.DeepEqual(before, &existing) {
		if err := r.client.Patch(ctx, &existing, client.MergeFrom(before)); err != nil {
			return nil, fmt.Errorf("patch runtime PodDisruptionBudget: %w", err)
		}
	}
	return &existing, nil
}

func (r *ModelDeploymentController) deleteRuntimeWorkload(
	ctx context.Context,
	deployment *v1alpha1.ModelDeployment,
) (bool, error) {
	deleted := false
	pdb := &policyv1.PodDisruptionBudget{}
	key := types.NamespacedName{
		Namespace: deployment.Namespace,
		Name:      templates.RuntimeServiceName(deployment.Name),
	}
	if err := r.client.Get(ctx, key, pdb); err != nil {
		if !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("get runtime PodDisruptionBudget for deletion: %w", err)
		}
	} else {
		if err := assertControlledBy(pdb, deployment); err != nil {
			return false, err
		}
		if err := r.client.Delete(ctx, pdb); err != nil && !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("delete runtime PodDisruptionBudget: %w", err)
		}
		deleted = true
	}

	existing := &appsv1.Deployment{}
	if err := r.client.Get(ctx, key, existing); err != nil {
		if apierrors.IsNotFound(err) {
			return deleted, nil
		}
		return false, fmt.Errorf("get runtime Deployment for deletion: %w", err)
	}
	if err := assertControlledBy(existing, deployment); err != nil {
		return false, err
	}
	if err := r.client.Delete(ctx, existing, client.PropagationPolicy(metav1.DeletePropagationForeground)); err != nil {
		if !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("delete runtime Deployment: %w", err)
		}
	}
	return true, nil
}

func (r *ModelDeploymentController) reserveGPU(
	ctx context.Context,
	deployment *v1alpha1.ModelDeployment,
	cacheNode string,
) (scheduler.GPUPlacement, error) {
	var nodeList corev1.NodeList
	if err := r.client.List(ctx, &nodeList); err != nil {
		return scheduler.GPUPlacement{}, fmt.Errorf("list Nodes: %w", err)
	}
	nodes := nodeList.Items
	if cacheNode != "" {
		filtered := make([]corev1.Node, 0, 1)
		for i := range nodes {
			if nodes[i].Name == cacheNode {
				filtered = append(filtered, nodes[i])
			}
		}
		nodes = filtered
	}
	allocations, err := r.gpuAllocations(ctx, deployment)
	if err != nil {
		return scheduler.GPUPlacement{}, err
	}
	resourceName := gpuResourceName(deployment)
	total, occupied, available := scheduler.Snapshot(resourceName, nodeList.Items, allocations)
	r.metrics.SetGPUSlots(string(resourceName), float64(total), float64(occupied), float64(available))

	return r.gpuPlanner.Plan(scheduler.GPURequest{
		ResourceName:  resourceName,
		Count:         effectiveGPUReservation(deployment),
		Type:          deployment.Spec.Resources.GPU.Type,
		PreferredNode: cacheNode,
	}, nodes, allocations)
}

func (r *ModelDeploymentController) gpuAllocations(
	ctx context.Context,
	self *v1alpha1.ModelDeployment,
) ([]scheduler.GPUAllocation, error) {
	var deployments v1alpha1.ModelDeploymentList
	if err := r.client.List(ctx, &deployments); err != nil {
		return nil, fmt.Errorf("list ModelDeployments for GPU reservations: %w", err)
	}
	allocations := make([]scheduler.GPUAllocation, 0)
	reservedDeployments := make(map[types.NamespacedName]struct{})
	for i := range deployments.Items {
		deployment := &deployments.Items[i]
		if deployment.Namespace == self.Namespace && deployment.Name == self.Name {
			continue
		}
		if deployment.Spec.Resources.GPU == nil ||
			deployment.Status.AssignedNode == "" ||
			(deployment.Status.Phase != v1alpha1.ModelDeploymentPhaseActivating &&
				deployment.Status.Phase != v1alpha1.ModelDeploymentPhaseActive) {
			continue
		}
		allocations = append(allocations, scheduler.GPUAllocation{
			NodeName:     deployment.Status.AssignedNode,
			ResourceName: gpuResourceName(deployment),
			Count:        effectiveGPUReservation(deployment),
		})
		reservedDeployments[types.NamespacedName{
			Namespace: deployment.Namespace,
			Name:      deployment.Name,
		}] = struct{}{}
	}
	var runtimeDeployments appsv1.DeploymentList
	if err := r.client.List(ctx, &runtimeDeployments, client.MatchingLabels{
		resources.LabelManagedBy: resources.ValueManagedBy,
	}); err != nil {
		return nil, fmt.Errorf("list runtime Deployments for GPU reservations: %w", err)
	}
	for i := range runtimeDeployments.Items {
		runtimeDeployment := &runtimeDeployments.Items[i]
		modelName := runtimeDeployment.Labels[resources.LabelModelDeployment]
		key := types.NamespacedName{Namespace: runtimeDeployment.Namespace, Name: modelName}
		if modelName == "" ||
			key == (types.NamespacedName{Namespace: self.Namespace, Name: self.Name}) {
			continue
		}
		if _, alreadyReserved := reservedDeployments[key]; alreadyReserved {
			continue
		}
		replicas := int64(0)
		if runtimeDeployment.Spec.Replicas != nil {
			replicas = int64(*runtimeDeployment.Spec.Replicas)
		}
		if replicas <= 0 {
			continue
		}
		nodeName := requiredHostname(runtimeDeployment.Spec.Template.Spec.Affinity)
		if nodeName == "" {
			continue
		}
		for _, container := range runtimeDeployment.Spec.Template.Spec.Containers {
			for resourceName, quantity := range container.Resources.Requests {
				if !strings.HasSuffix(string(resourceName), ".com/gpu") || quantity.Sign() <= 0 {
					continue
				}
				allocations = append(allocations, scheduler.GPUAllocation{
					NodeName:     nodeName,
					ResourceName: resourceName,
					Count:        quantity.Value() * replicas,
				})
			}
		}
	}
	return allocations, nil
}

func requiredHostname(affinity *corev1.Affinity) string {
	if affinity == nil ||
		affinity.NodeAffinity == nil ||
		affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		return ""
	}
	terms := affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	for _, term := range terms {
		for _, expression := range term.MatchExpressions {
			if expression.Key == corev1.LabelHostname &&
				expression.Operator == corev1.NodeSelectorOpIn &&
				len(expression.Values) == 1 {
				return expression.Values[0]
			}
		}
	}
	return ""
}

func (r *ModelDeploymentController) handleGPUCapacityError(
	ctx context.Context,
	deployment, original *v1alpha1.ModelDeployment,
	placementErr error,
) (ctrl.Result, error) {
	if !errors.Is(placementErr, scheduler.ErrNoCompatibleGPUNode) &&
		!errors.Is(placementErr, scheduler.ErrInsufficientGPUCapacity) {
		return ctrl.Result{}, placementErr
	}
	if _, err := r.deleteRuntimeWorkload(ctx, deployment); err != nil {
		return ctrl.Result{}, err
	}
	deployment.Status.AssignedNode = ""
	deployment.Status.AssignedGPUs = nil
	deployment.Status.Replicas = v1alpha1.ReplicaStatus{
		Desired: effectiveRuntimeReplicas(deployment),
	}
	deployment.Status.Model.Loaded = false

	switch effectiveWhenFull(deployment) {
	case v1alpha1.ActivationWhenFullQueue:
		deployment.Status.Phase = v1alpha1.ModelDeploymentPhaseWaitingForGPU
		setRuntimeWaitingConditions(deployment, v1alpha1.ReasonWaitingForGPU, "Runtime is queued for compatible GPU capacity")
		setDeploymentCondition(deployment, v1alpha1.ConditionGPUAssigned, metav1.ConditionFalse,
			v1alpha1.ReasonWaitingForGPU, placementErr.Error())
		return r.patchDeploymentStatus(ctx, deployment, original, v1alpha1.ReasonWaitingForGPU)
	case v1alpha1.ActivationWhenFullReject:
		setDeploymentCondition(deployment, v1alpha1.ConditionGPUAssigned, metav1.ConditionFalse,
			v1alpha1.ReasonInsufficientGPU, placementErr.Error())
		return r.failDeployment(ctx, deployment, original, v1alpha1.ReasonInsufficientGPU, placementErr.Error())
	default:
		setDeploymentCondition(deployment, v1alpha1.ConditionGPUAssigned, metav1.ConditionFalse,
			v1alpha1.ReasonReplacementPending, placementErr.Error())
		return r.failDeployment(
			ctx,
			deployment,
			original,
			v1alpha1.ReasonReplacementPending,
			"GPU replacement policies require the explicit replacement workflow",
		)
	}
}

func (r *ModelDeploymentController) projectRuntimeDeployment(
	ctx context.Context,
	deployment, original *v1alpha1.ModelDeployment,
	runtimeDeployment *appsv1.Deployment,
	created bool,
) (ctrl.Result, error) {
	desired := int32(0)
	if runtimeDeployment.Spec.Replicas != nil {
		desired = *runtimeDeployment.Spec.Replicas
	}
	deployment.Status.Replicas = v1alpha1.ReplicaStatus{
		Desired: desired,
		Ready:   runtimeDeployment.Status.ReadyReplicas,
	}
	if desired > 0 &&
		runtimeDeployment.Status.ObservedGeneration >= runtimeDeployment.Generation &&
		runtimeDeployment.Status.ReadyReplicas >= desired &&
		runtimeDeployment.Status.AvailableReplicas >= desired {
		deployment.Status.Phase = v1alpha1.ModelDeploymentPhaseActive
		deployment.Status.Model.Loaded = true
		setDeploymentCondition(deployment, v1alpha1.ConditionRuntimeReady, metav1.ConditionTrue,
			v1alpha1.ReasonRuntimeReady, "Runtime has the desired ready replicas")
		setDeploymentCondition(deployment, v1alpha1.ConditionModelLoaded, metav1.ConditionTrue,
			v1alpha1.ReasonRuntimeReady, "Model runtime is accepting traffic")
		setDeploymentCondition(deployment, v1alpha1.ConditionRoutingReady, metav1.ConditionTrue,
			v1alpha1.ReasonRouteEnabled, "Stable runtime Service is ready for gateway routing")
		status.SetCondition(&deployment.Status.Conditions, deployment.Generation, v1alpha1.Condition{
			Type:    v1alpha1.ConditionReady,
			Status:  metav1.ConditionTrue,
			Reason:  v1alpha1.ReasonRuntimeReady,
			Message: "Runtime is accepting traffic",
		})
		observeDuration := original.Status.Phase != v1alpha1.ModelDeploymentPhaseActive &&
			!runtimeDeployment.CreationTimestamp.IsZero()
		result, err := r.patchDeploymentStatus(ctx, deployment, original, v1alpha1.ReasonRuntimeReady)
		if err == nil && !result.Requeue && observeDuration {
			duration := time.Since(runtimeDeployment.CreationTimestamp.Time)
			if duration >= 0 {
				r.metrics.ObserveActivationDuration(duration)
			}
		}
		return result, err
	}

	deployment.Status.Phase = v1alpha1.ModelDeploymentPhaseActivating
	deployment.Status.Model.Loaded = false
	message := fmt.Sprintf("Runtime has %d/%d ready replicas", runtimeDeployment.Status.ReadyReplicas, desired)
	setDeploymentCondition(deployment, v1alpha1.ConditionRuntimeReady, metav1.ConditionFalse,
		v1alpha1.ReasonRuntimeCreating, message)
	setDeploymentCondition(deployment, v1alpha1.ConditionModelLoaded, metav1.ConditionFalse,
		v1alpha1.ReasonRuntimeCreating, "Model is loading")
	setDeploymentCondition(deployment, v1alpha1.ConditionRoutingReady, metav1.ConditionFalse,
		v1alpha1.ReasonRouteDisabled, "Gateway routing waits for runtime readiness")
	setReadyFalse(&deployment.Status, v1alpha1.ReasonRuntimeCreating, message)
	reason := ""
	if created || original.Status.Phase != v1alpha1.ModelDeploymentPhaseActivating {
		reason = v1alpha1.ReasonRuntimeCreating
	}
	return r.patchDeploymentStatus(ctx, deployment, original, reason)
}

func (r *ModelDeploymentController) projectReadyCache(
	deployment *v1alpha1.ModelDeployment,
	result cachecontract.Result,
) {
	deployment.Status.Cache = v1alpha1.ModelCacheSummary{
		State:    string(v1alpha1.ModelCachePhaseReady),
		NodeName: result.NodeName,
		Path:     result.Path,
	}
	setDeploymentCondition(deployment, v1alpha1.ConditionCacheReady, metav1.ConditionTrue,
		v1alpha1.ReasonCacheVerified,
		fmt.Sprintf("Verified revision %q is ready on node %s", result.Revision, result.NodeName))
}

func (r *ModelDeploymentController) projectCachePending(
	deployment *v1alpha1.ModelDeployment,
	cache *v1alpha1.ModelCache,
) {
	deployment.Status.Phase = v1alpha1.ModelDeploymentPhasePending
	deployment.Status.Cache = v1alpha1.ModelCacheSummary{State: string(v1alpha1.ModelCachePhasePending)}
	if cache != nil {
		deployment.Status.Cache.State = string(cache.Status.Phase)
		deployment.Status.Cache.NodeName = cache.Status.NodeName
		deployment.Status.Cache.Path = cache.Status.Path
	}
	setDeploymentCondition(deployment, v1alpha1.ConditionCacheReady, metav1.ConditionFalse,
		v1alpha1.ReasonCachePending, "Waiting for the ModelCache controller")
	setRuntimeWaitingConditions(deployment, v1alpha1.ReasonCachePending, "Runtime is waiting for the model cache")
}

func setRuntimeWaitingConditions(deployment *v1alpha1.ModelDeployment, reason, message string) {
	if deployment.Spec.Resources.GPU != nil {
		setDeploymentCondition(deployment, v1alpha1.ConditionGPUAssigned, metav1.ConditionFalse, reason, message)
	} else {
		status.RemoveCondition(&deployment.Status.Conditions, v1alpha1.ConditionGPUAssigned)
	}
	setDeploymentCondition(deployment, v1alpha1.ConditionRuntimeReady, metav1.ConditionFalse, reason, message)
	setDeploymentCondition(deployment, v1alpha1.ConditionModelLoaded, metav1.ConditionFalse, reason, message)
	setDeploymentCondition(deployment, v1alpha1.ConditionRoutingReady, metav1.ConditionFalse, v1alpha1.ReasonRouteDisabled, message)
	setReadyFalse(&deployment.Status, reason, message)
}

func (r *ModelDeploymentController) failDeployment(
	ctx context.Context,
	deployment, original *v1alpha1.ModelDeployment,
	reason, message string,
) (ctrl.Result, error) {
	deployment.Status.Phase = v1alpha1.ModelDeploymentPhaseFailed
	deployment.Status.Model.Loaded = false
	setDeploymentCondition(deployment, v1alpha1.ConditionReady, metav1.ConditionFalse, reason, message)
	setDeploymentCondition(deployment, v1alpha1.ConditionRuntimeReady, metav1.ConditionFalse, reason, message)
	setDeploymentCondition(deployment, v1alpha1.ConditionModelLoaded, metav1.ConditionFalse, reason, message)
	setDeploymentCondition(deployment, v1alpha1.ConditionRoutingReady, metav1.ConditionFalse, v1alpha1.ReasonRouteDisabled, message)
	return r.patchDeploymentStatus(ctx, deployment, original, reason)
}

func (r *ModelDeploymentController) patchDeploymentStatus(
	ctx context.Context,
	deployment, original *v1alpha1.ModelDeployment,
	eventReason string,
) (ctrl.Result, error) {
	deployment.Status.ObservedGeneration = deployment.Generation
	if reflect.DeepEqual(original.Status, deployment.Status) {
		if deployment.Status.Phase == v1alpha1.ModelDeploymentPhaseWaitingForGPU {
			r.queueMetricDirty = true
			if err := r.refreshQueueDepth(ctx); err != nil {
				return ctrl.Result{}, err
			}
		}
		return deploymentRequeueResult(deployment.Status.Phase), nil
	}
	if err := r.client.Status().Patch(ctx, deployment, client.MergeFrom(original)); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, fmt.Errorf("patch ModelDeployment status: %w", err)
	}
	if eventReason != "" && lifecycleTransitioned(original.Status, deployment.Status, eventReason) {
		message := string(deployment.Status.Phase)
		if ready, found := status.FindCondition(deployment.Status.Conditions, v1alpha1.ConditionReady); found {
			message = ready.Message
		}
		eventType := corev1.EventTypeNormal
		if deployment.Status.Phase == v1alpha1.ModelDeploymentPhaseFailed {
			eventType = corev1.EventTypeWarning
			r.metrics.IncFailure("modeldeployment", eventReason)
		}
		r.eventRecorder.Eventf(deployment, eventType, eventReason, "%s", message)
	}
	if original.Status.Phase == v1alpha1.ModelDeploymentPhaseWaitingForGPU ||
		deployment.Status.Phase == v1alpha1.ModelDeploymentPhaseWaitingForGPU {
		r.queueMetricDirty = true
		if err := r.refreshQueueDepth(ctx); err != nil {
			return ctrl.Result{}, err
		}
	}
	return deploymentRequeueResult(deployment.Status.Phase), nil
}

func (r *ModelDeploymentController) refreshQueueDepth(ctx context.Context) error {
	var deployments v1alpha1.ModelDeploymentList
	if err := r.client.List(ctx, &deployments); err != nil {
		return fmt.Errorf("list ModelDeployments for activation queue metric: %w", err)
	}
	depth := 0
	for i := range deployments.Items {
		if deployments.Items[i].Status.Phase == v1alpha1.ModelDeploymentPhaseWaitingForGPU {
			depth++
		}
	}
	r.metrics.SetActivationQueueDepth(float64(depth))
	r.queueMetricDirty = false
	return nil
}

func lifecycleTransitioned(
	oldStatus, newStatus v1alpha1.ModelDeploymentStatus,
	reason string,
) bool {
	if oldStatus.Phase != newStatus.Phase {
		return true
	}
	oldReady, oldFound := status.FindCondition(oldStatus.Conditions, v1alpha1.ConditionReady)
	newReady, newFound := status.FindCondition(newStatus.Conditions, v1alpha1.ConditionReady)
	return oldFound != newFound || newFound && (oldReady.Status != newReady.Status || oldReady.Reason != reason)
}

func deploymentRequeueResult(phase v1alpha1.ModelDeploymentPhase) ctrl.Result {
	switch phase {
	case v1alpha1.ModelDeploymentPhasePending,
		v1alpha1.ModelDeploymentPhaseWaitingForCapacity,
		v1alpha1.ModelDeploymentPhaseWaitingForGPU:
		return ctrl.Result{RequeueAfter: waitingRequeueAfter}
	case v1alpha1.ModelDeploymentPhaseDownloading,
		v1alpha1.ModelDeploymentPhaseActivating:
		return ctrl.Result{RequeueAfter: deploymentRequeueAfter}
	default:
		return ctrl.Result{}
	}
}

func (r *ModelDeploymentController) touchCacheLastUsed(ctx context.Context, cache *v1alpha1.ModelCache) error {
	before := cache.DeepCopy()
	cache.Status.LastUsedTime = metav1.Now()
	if err := r.client.Status().Patch(ctx, cache, client.MergeFrom(before)); err != nil {
		if apierrors.IsConflict(err) {
			return fmt.Errorf("update ModelCache lastUsedTime conflict: %w", err)
		}
		return fmt.Errorf("update ModelCache lastUsedTime: %w", err)
	}
	return nil
}

func (r *ModelDeploymentController) getModelRuntime(
	ctx context.Context,
	deployment *v1alpha1.ModelDeployment,
) (*v1alpha1.ModelRuntime, error) {
	var modelRuntime v1alpha1.ModelRuntime
	key := types.NamespacedName{Namespace: deployment.Namespace, Name: deployment.Spec.Runtime.Ref}
	if err := r.client.Get(ctx, key, &modelRuntime); err != nil {
		return nil, fmt.Errorf("get resolved ModelRuntime: %w", err)
	}
	return &modelRuntime, nil
}

func effectiveDesiredState(deployment *v1alpha1.ModelDeployment) v1alpha1.ActivationDesiredState {
	if deployment.Spec.Activation.DesiredState == "" {
		return v1alpha1.ActivationDesiredStateInactive
	}
	return deployment.Spec.Activation.DesiredState
}

func effectiveWhenFull(deployment *v1alpha1.ModelDeployment) v1alpha1.ActivationWhenFull {
	if deployment.Spec.Activation.WhenFull == "" {
		return v1alpha1.ActivationWhenFullQueue
	}
	return deployment.Spec.Activation.WhenFull
}

func effectiveRuntimeReplicas(deployment *v1alpha1.ModelDeployment) int32 {
	replicas := deployment.Spec.Scaling.MinReplicas
	if replicas < 1 {
		replicas = 1
	}
	return replicas
}

func effectiveGPUReservation(deployment *v1alpha1.ModelDeployment) int64 {
	if deployment.Spec.Resources.GPU == nil {
		return 0
	}
	return int64(deployment.Spec.Resources.GPU.Count) * int64(effectiveRuntimeReplicas(deployment))
}

func gpuResourceName(deployment *v1alpha1.ModelDeployment) corev1.ResourceName {
	vendor := templates.DefaultGPUVendor
	if deployment.Spec.Resources.GPU != nil && deployment.Spec.Resources.GPU.Vendor != "" {
		vendor = deployment.Spec.Resources.GPU.Vendor
	}
	return corev1.ResourceName(vendor + ".com/gpu")
}

func setDeploymentCondition(
	deployment *v1alpha1.ModelDeployment,
	conditionType string,
	conditionStatus metav1.ConditionStatus,
	reason, message string,
) {
	status.SetCondition(&deployment.Status.Conditions, deployment.Generation, v1alpha1.Condition{
		Type:    conditionType,
		Status:  conditionStatus,
		Reason:  reason,
		Message: message,
	})
}

func assertControlledBy(object metav1.Object, deployment *v1alpha1.ModelDeployment) error {
	controller := metav1.GetControllerOf(object)
	if controller == nil ||
		controller.APIVersion != v1alpha1.GroupVersion.String() ||
		controller.Kind != "ModelDeployment" ||
		controller.Name != deployment.Name ||
		(deployment.UID != "" && controller.UID != deployment.UID) {
		return fmt.Errorf("%s %q is not controlled by ModelDeployment %q",
			reflect.TypeOf(object).String(), object.GetName(), deployment.Name)
	}
	return nil
}

func mergeLabels(existing *map[string]string, desired map[string]string) {
	if *existing == nil {
		*existing = make(map[string]string, len(desired))
	}
	for key, value := range desired {
		(*existing)[key] = value
	}
}

func containsLabels(existing, desired map[string]string) bool {
	for key, value := range desired {
		if existing[key] != value {
			return false
		}
	}
	return true
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

func copyTolerations(input []v1alpha1.Toleration) []v1alpha1.Toleration {
	if input == nil {
		return nil
	}
	result := make([]v1alpha1.Toleration, len(input))
	for i := range input {
		result[i] = input[i]
		if input[i].TolerationSeconds != nil {
			value := *input[i].TolerationSeconds
			result[i].TolerationSeconds = &value
		}
	}
	return result
}

func ensureStringMapValue(values *map[string]string, key, value string) {
	if *values == nil {
		*values = make(map[string]string)
	}
	(*values)[key] = value
}

func readyCacheSpecsCompatible(existing, desired v1alpha1.ModelCacheSpec) bool {
	existing.SecretRef = ""
	desired.SecretRef = ""
	return reflect.DeepEqual(existing, desired)
}

func permanentLifecycleError(err error) error {
	return fmt.Errorf("%w: %w", errPermanentLifecycle, err)
}

type kubernetesRuntimeGetter struct {
	client client.Client
}

func (g kubernetesRuntimeGetter) GetRuntime(
	ctx context.Context,
	namespace, name string,
) (*v1alpha1.ModelRuntime, error) {
	var modelRuntime v1alpha1.ModelRuntime
	if err := g.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &modelRuntime); err != nil {
		return nil, err
	}
	return &modelRuntime, nil
}

// SetupWithManager registers lifecycle and placement watches. Reconciliation is
// serialized so status reservations cannot oversubscribe a GPU between two
// concurrent activation decisions.
func (r *ModelDeploymentController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ModelDeployment{}).
		Owns(&appsv1.Deployment{}).
		Owns(&policyv1.PodDisruptionBudget{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Watches(&v1alpha1.ModelCache{}, handler.EnqueueRequestsFromMapFunc(r.requestsForCache)).
		Watches(&v1alpha1.ModelRuntime{}, handler.EnqueueRequestsFromMapFunc(r.requestsForRuntime)).
		Watches(&corev1.Node{}, handler.EnqueueRequestsFromMapFunc(r.requestsForNode)).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(r)
}

func (r *ModelDeploymentController) requestsForCache(
	_ context.Context,
	object client.Object,
) []reconcile.Request {
	name := object.GetLabels()[resources.LabelModelDeployment]
	if name == "" {
		return nil
	}
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{Namespace: object.GetNamespace(), Name: name},
	}}
}

func (r *ModelDeploymentController) requestsForRuntime(
	ctx context.Context,
	object client.Object,
) []reconcile.Request {
	var deployments v1alpha1.ModelDeploymentList
	if err := r.client.List(ctx, &deployments, client.InNamespace(object.GetNamespace())); err != nil {
		log.FromContext(ctx).Error(err, "could not list ModelDeployments after ModelRuntime change")
		return nil
	}
	var requests []reconcile.Request
	for i := range deployments.Items {
		if deployments.Items[i].Spec.Runtime.Ref == object.GetName() {
			requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
				Namespace: deployments.Items[i].Namespace,
				Name:      deployments.Items[i].Name,
			}})
		}
	}
	return requests
}

func (r *ModelDeploymentController) requestsForNode(
	ctx context.Context,
	object client.Object,
) []reconcile.Request {
	var deployments v1alpha1.ModelDeploymentList
	if err := r.client.List(ctx, &deployments); err != nil {
		log.FromContext(ctx).Error(err, "could not list ModelDeployments after Node change")
		return nil
	}
	var requests []reconcile.Request
	for i := range deployments.Items {
		deployment := &deployments.Items[i]
		if deployment.Spec.Resources.GPU == nil ||
			(effectiveDesiredState(deployment) != v1alpha1.ActivationDesiredStateActive &&
				deployment.Status.AssignedNode != object.GetName()) {
			continue
		}
		requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
			Namespace: deployment.Namespace,
			Name:      deployment.Name,
		}})
	}
	return requests
}
