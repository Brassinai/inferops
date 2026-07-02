package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	"github.com/brassinai/inferops/operator/internal/resources"
	"github.com/brassinai/inferops/operator/internal/scheduler"
	"github.com/brassinai/inferops/operator/internal/status"
	"github.com/brassinai/inferops/operator/internal/templates"
	"github.com/brassinai/inferops/operator/internal/validation"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	// RetryAnnotation may be set to a new non-empty token to force one new
	// download attempt. The token is retained to make reconciliation
	// idempotent.
	RetryAnnotation = resources.CacheRetryAnnotation

	pendingRequeueAfter = 30 * time.Second
)

// ModelCacheReconcilerConfig carries trusted operator configuration for cache
// reconciliation.
type ModelCacheReconcilerConfig struct {
	// CacheRoot is the configured hostPath root for node-local caches.
	CacheRoot string
	// DownloaderImage is the pinned cache downloader image.
	DownloaderImage string
	// CacheNodeSelector restricts which nodes may host a cache.
	CacheNodeSelector map[string]string
	// CacheCapacityAnnotation is the node annotation key that holds the
	// configured cache capacity in bytes.
	CacheCapacityAnnotation string
	// CacheRequiredResources restricts placement to nodes advertising these
	// allocatable resources.
	CacheRequiredResources []corev1.ResourceName
}

// ModelCacheReconciler owns reconciliation for ModelCache resources.
type ModelCacheReconciler struct {
	client        client.Client
	builder       resources.Builder
	planner       *scheduler.CachePlanner
	eventRecorder record.EventRecorder
}

// NewModelCacheReconciler creates a reconciler with validated dependencies.
func NewModelCacheReconciler(
	c client.Client,
	config ModelCacheReconcilerConfig,
	eventRecorder record.EventRecorder,
) (*ModelCacheReconciler, error) {
	if c == nil {
		return nil, errors.New("client is required")
	}
	if eventRecorder == nil {
		return nil, errors.New("event recorder is required")
	}

	builder, err := resources.NewBuilder(resources.BuilderOptions{
		CacheRoot:            config.CacheRoot,
		CacheDownloaderImage: config.DownloaderImage,
	})
	if err != nil {
		return nil, fmt.Errorf("create resource builder: %w", err)
	}

	planner, err := scheduler.NewCachePlanner(scheduler.PlannerConfig{
		CacheRoot:          config.CacheRoot,
		NodeSelector:       config.CacheNodeSelector,
		CapacityAnnotation: config.CacheCapacityAnnotation,
		RequiredResources:  config.CacheRequiredResources,
	})
	if err != nil {
		return nil, fmt.Errorf("create cache planner: %w", err)
	}

	return &ModelCacheReconciler{
		client:        c,
		builder:       builder,
		planner:       planner,
		eventRecorder: eventRecorder,
	}, nil
}

// Reconcile implements the controller-runtime Reconcile interface.
func (r *ModelCacheReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("modelcache", req.NamespacedName)

	var cache v1alpha1.ModelCache
	if err := r.client.Get(ctx, req.NamespacedName, &cache); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get ModelCache: %w", err)
	}

	if !cache.DeletionTimestamp.IsZero() {
		// Month one: no data cleanup finalizer. The object deletes immediately
		// and leaves hostPath data in place.
		return ctrl.Result{}, nil
	}

	result, err := r.reconcile(ctx, &cache)
	if err != nil {
		logger.Error(err, "reconciliation failed")
		return result, err
	}
	return result, nil
}

func (r *ModelCacheReconciler) reconcile(ctx context.Context, cache *v1alpha1.ModelCache) (ctrl.Result, error) {
	original := cache.DeepCopy()

	// Validate early and deterministically.
	if err := validation.ValidateModelCache(*cache); err != nil {
		return r.fail(ctx, cache, original, v1alpha1.CacheReasonSpecInvalid, err.Error())
	}
	if _, err := r.planner.PlanPath(cache); err != nil {
		return r.fail(ctx, cache, original, v1alpha1.CacheReasonSpecInvalid, err.Error())
	}
	// Check whether the cache is already Ready and still valid.
	ready, nodeLost, readyErr := r.readyPlacementMatches(ctx, cache)
	if readyErr != nil {
		return ctrl.Result{}, fmt.Errorf("check ready cache placement: %w", readyErr)
	}
	if ready {
		return r.setReady(ctx, cache, original)
	}
	if nodeLost {
		return r.setPending(ctx, cache, original, v1alpha1.CacheReasonNodeLost,
			fmt.Sprintf("Previously selected node %q no longer exists; cache location is retained", cache.Status.NodeName))
	}
	if hasCompletedArtifact(cache) {
		return r.fail(
			ctx,
			cache,
			original,
			v1alpha1.CacheReasonIdentityChanged,
			"Ready cache identity is immutable; create a new ModelCache for a different repo, revision, path, or node",
		)
	}
	if cache.Status.Phase == v1alpha1.ModelCachePhaseDownloading && cache.Status.InputHash != "" {
		identityMatches, err := r.inFlightIdentityMatches(cache)
		if err != nil {
			return r.fail(ctx, cache, original, v1alpha1.CacheReasonSpecInvalid, err.Error())
		}
		if !identityMatches {
			stopping, err := r.deleteDownloaderJob(ctx, cache)
			if err != nil {
				return ctrl.Result{}, err
			}
			if stopping {
				return ctrl.Result{RequeueAfter: time.Second}, nil
			}
			cache.Status.ObservedGeneration = cache.Generation
			cache.Status.Phase = v1alpha1.ModelCachePhasePending
			cache.Status.InputHash = ""
			setCondition(cache, v1alpha1.CacheConditionPlaced, metav1.ConditionFalse, v1alpha1.CacheReasonSpecValidated, "Cache identity changed; placement must be recalculated")
			setCondition(cache, v1alpha1.CacheConditionDownloaded, metav1.ConditionFalse, v1alpha1.CacheReasonSpecValidated, "Downloader Job has not started for the new identity")
			setCondition(cache, v1alpha1.CacheConditionVerified, metav1.ConditionUnknown, v1alpha1.CacheReasonSpecValidated, "Cache has not been downloaded for the new identity")
			setCondition(cache, v1alpha1.CacheConditionReady, metav1.ConditionFalse, v1alpha1.CacheReasonSpecValidated, "Cache identity change is pending")
			result, err := r.patchStatus(ctx, cache, original, "", "")
			if err != nil {
				return result, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
		placement, available, err := r.inFlightPlacement(ctx, cache)
		if err != nil {
			return ctrl.Result{}, err
		}
		if available {
			job, replacing, err := r.reconcileDownloaderJob(ctx, cache, placement)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("reconcile in-flight downloader Job: %w", err)
			}
			if replacing {
				return ctrl.Result{RequeueAfter: time.Second}, nil
			}
			return r.projectJobStatus(ctx, cache, original, placement, job)
		}
		stopping, err := r.deleteDownloaderJob(ctx, cache)
		if err != nil {
			return ctrl.Result{}, err
		}
		if stopping {
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
	}
	if cache.Spec.SecretRef != "" {
		var secret corev1.Secret
		secretKey := types.NamespacedName{Namespace: cache.Namespace, Name: cache.Spec.SecretRef}
		if err := r.client.Get(ctx, secretKey, &secret); err != nil {
			if apierrors.IsNotFound(err) {
				return r.setPending(
					ctx,
					cache,
					original,
					v1alpha1.CacheReasonSecretNotFound,
					fmt.Sprintf("Referenced Secret %q does not exist", cache.Spec.SecretRef),
				)
			}
			return ctrl.Result{}, fmt.Errorf("get referenced Secret %q: %w", cache.Spec.SecretRef, err)
		}
		if len(secret.Data["token"]) == 0 {
			return r.setPending(
				ctx,
				cache,
				original,
				v1alpha1.CacheReasonSecretKeyMissing,
				fmt.Sprintf("Referenced Secret %q does not contain a non-empty token key", cache.Spec.SecretRef),
			)
		}
	}

	// Resolve placement on a suitable node.
	placement, placementErr := r.resolvePlacement(ctx, cache)
	if placementErr != nil {
		switch {
		case errors.Is(placementErr, scheduler.ErrPinnedNodeUnavailable):
			return r.setPending(ctx, cache, original, v1alpha1.CacheReasonPinnedNodeUnavailable, placementErr.Error())
		case errors.Is(placementErr, scheduler.ErrInsufficientCacheCapacity):
			return r.setPending(ctx, cache, original, v1alpha1.CacheReasonInsufficientCapacity, placementErr.Error())
		case errors.Is(placementErr, scheduler.ErrCachePathConflict):
			return r.setPending(ctx, cache, original, v1alpha1.CacheReasonPathConflict, placementErr.Error())
		case errors.Is(placementErr, scheduler.ErrNoEligibleNode):
			return r.setPending(ctx, cache, original, v1alpha1.CacheReasonNoEligibleNode, placementErr.Error())
		default:
			return ctrl.Result{}, fmt.Errorf("resolve cache placement: %w", placementErr)
		}
	}
	if !placementRecorded(cache, placement) {
		return r.recordPlacement(ctx, cache, original, placement)
	}

	// Build the desired Job and ensure it exists.
	job, replacing, err := r.reconcileDownloaderJob(ctx, cache, placement)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reconcile downloader Job: %w", err)
	}
	if replacing {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	// Observe Job result and project it into status.
	return r.projectJobStatus(ctx, cache, original, placement, job)
}

func (r *ModelCacheReconciler) inFlightIdentityMatches(cache *v1alpha1.ModelCache) (bool, error) {
	if cache.Spec.Storage.Path != cache.Status.Path {
		return false, nil
	}
	if cache.Spec.Storage.NodeName != "" && cache.Spec.Storage.NodeName != cache.Status.NodeName {
		return false, nil
	}
	reservedSize, err := resource.ParseQuantity(cache.Spec.Storage.Size)
	if err != nil {
		return false, fmt.Errorf("parse storage size: %w", err)
	}
	expectedHash, err := resources.CacheInputHash(cache, resources.CachePlacement{
		NodeName:     cache.Status.NodeName,
		NodeUID:      cache.Status.NodeUID,
		Path:         cache.Status.Path,
		ReservedSize: reservedSize,
	})
	if err != nil {
		return false, err
	}
	return expectedHash == cache.Status.InputHash, nil
}

func (r *ModelCacheReconciler) inFlightPlacement(
	ctx context.Context,
	cache *v1alpha1.ModelCache,
) (resources.CachePlacement, bool, error) {
	node := &corev1.Node{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: cache.Status.NodeName}, node); err != nil {
		if apierrors.IsNotFound(err) {
			return resources.CachePlacement{}, false, nil
		}
		return resources.CachePlacement{}, false, fmt.Errorf("get in-flight cache node: %w", err)
	}
	if cache.Status.NodeUID != "" && cache.Status.NodeUID != string(node.UID) {
		return resources.CachePlacement{}, false, nil
	}
	reservedSize, err := resource.ParseQuantity(cache.Status.ReservedSize)
	if err != nil || reservedSize.Sign() <= 0 {
		return resources.CachePlacement{}, false, fmt.Errorf(
			"in-flight cache has invalid reserved size %q",
			cache.Status.ReservedSize,
		)
	}
	return resources.CachePlacement{
		NodeName:     cache.Status.NodeName,
		NodeUID:      string(node.UID),
		Path:         cache.Status.Path,
		ReservedSize: reservedSize,
	}, true, nil
}

func (r *ModelCacheReconciler) deleteDownloaderJob(
	ctx context.Context,
	cache *v1alpha1.ModelCache,
) (bool, error) {
	job := &batchv1.Job{}
	key := types.NamespacedName{
		Namespace: cache.Namespace,
		Name:      cache.Name + templates.CacheDownloaderJobSuffix,
	}
	if err := r.client.Get(ctx, key, job); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("get downloader Job for lost node: %w", err)
	}
	if !job.DeletionTimestamp.IsZero() {
		return true, nil
	}
	if err := r.client.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationForeground)); err != nil {
		if !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("delete downloader Job for lost node: %w", err)
		}
	}
	return true, nil
}

func placementRecorded(cache *v1alpha1.ModelCache, placement resources.CachePlacement) bool {
	return cache.Status.NodeName == placement.NodeName &&
		cache.Status.NodeUID == placement.NodeUID &&
		cache.Status.Path == placement.Path &&
		cache.Status.ReservedSize == placement.ReservedSize.String()
}

func (r *ModelCacheReconciler) recordPlacement(
	ctx context.Context,
	cache *v1alpha1.ModelCache,
	original *v1alpha1.ModelCache,
	placement resources.CachePlacement,
) (ctrl.Result, error) {
	cache.Status.ObservedGeneration = cache.Generation
	cache.Status.Phase = v1alpha1.ModelCachePhasePending
	cache.Status.NodeName = placement.NodeName
	cache.Status.NodeUID = placement.NodeUID
	cache.Status.Path = placement.Path
	cache.Status.Size = placement.ReservedSize.String()
	cache.Status.ReservedSize = placement.ReservedSize.String()
	cache.Status.Revision = scheduler.EffectiveRevision(cache)
	cache.Status.InputHash = ""
	setCondition(cache, v1alpha1.CacheConditionSpecValid, metav1.ConditionTrue, v1alpha1.CacheReasonSpecValidated, "Cache spec is valid")
	setCondition(cache, v1alpha1.CacheConditionPlaced, metav1.ConditionTrue, v1alpha1.CacheReasonPlaced,
		fmt.Sprintf("Cache placed on node %s at %s", placement.NodeName, placement.Path))
	setCondition(cache, v1alpha1.CacheConditionDownloaded, metav1.ConditionFalse, v1alpha1.CacheReasonPlaced, "Downloader Job has not started")
	setCondition(cache, v1alpha1.CacheConditionVerified, metav1.ConditionUnknown, v1alpha1.CacheReasonPlaced, "Cache has not been downloaded")
	setCondition(cache, v1alpha1.CacheConditionReady, metav1.ConditionFalse, v1alpha1.CacheReasonPlaced, "Cache placement is reserved")

	result, err := r.patchStatus(ctx, cache, original, v1alpha1.CacheReasonPlaced, "Cache placement is reserved")
	if err != nil {
		return result, err
	}
	return ctrl.Result{Requeue: true}, nil
}

// readyPlacementMatches returns true when the cache status reports Ready and
// the recorded placement still matches the spec and operator configuration.
func (r *ModelCacheReconciler) readyPlacementMatches(
	ctx context.Context,
	cache *v1alpha1.ModelCache,
) (ready bool, nodeLost bool, err error) {
	if cache.Status.Phase != v1alpha1.ModelCachePhaseReady && !hasCompletedArtifact(cache) {
		return false, false, nil
	}
	if cache.Status.NodeName == "" || cache.Status.Path == "" {
		return false, false, nil
	}
	if cache.Spec.Storage.Path != cache.Status.Path {
		return false, false, nil
	}
	if cache.Spec.Storage.NodeName != "" && cache.Spec.Storage.NodeName != cache.Status.NodeName {
		return false, false, nil
	}

	// A cordon or temporary NotReady condition does not erase node-local data.
	// Preserve the recorded placement while the Node object still exists.
	node := &corev1.Node{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: cache.Status.NodeName}, node); err != nil {
		if apierrors.IsNotFound(err) {
			return false, true, nil
		}
		return false, false, err
	}
	if cache.Status.NodeUID != "" && cache.Status.NodeUID != string(node.UID) {
		return false, true, nil
	}

	reservedSize, err := resource.ParseQuantity(cache.Spec.Storage.Size)
	if err != nil {
		return false, false, nil
	}
	expectedHash, err := resources.CacheInputHash(cache, resources.CachePlacement{
		NodeName:     cache.Status.NodeName,
		NodeUID:      string(node.UID),
		Path:         cache.Status.Path,
		ReservedSize: reservedSize,
	})
	if err != nil {
		return false, false, err
	}

	// Status is authoritative after a completed Job has been observed. This
	// preserves readiness after the Job TTL controller removes old Jobs.
	if cache.Status.InputHash == expectedHash {
		return true, false, nil
	}

	jobName := cache.Name + templates.CacheDownloaderJobSuffix
	job := &batchv1.Job{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: jobName, Namespace: cache.Namespace}, job); err != nil {
		if apierrors.IsNotFound(err) {
			return false, false, nil
		}
		return false, false, err
	}
	if resources.CacheInputHashFromJob(job) == expectedHash {
		return resources.ObserveDownloaderJob(job).Complete, false, nil
	}

	return false, false, nil
}

func hasCompletedArtifact(cache *v1alpha1.ModelCache) bool {
	if cache == nil {
		return false
	}
	downloaded, found := status.FindCondition(cache.Status.Conditions, v1alpha1.CacheConditionDownloaded)
	return found && downloaded.Status == metav1.ConditionTrue
}

func (r *ModelCacheReconciler) resolvePlacement(ctx context.Context, cache *v1alpha1.ModelCache) (resources.CachePlacement, error) {
	var nodeList corev1.NodeList
	if err := r.client.List(ctx, &nodeList); err != nil {
		return resources.CachePlacement{}, fmt.Errorf("list nodes: %w", err)
	}

	var cacheList v1alpha1.ModelCacheList
	if err := r.client.List(ctx, &cacheList); err != nil {
		return resources.CachePlacement{}, fmt.Errorf("list caches: %w", err)
	}

	placement, err := r.planner.Plan(cache, nodeList.Items, cacheList.Items)
	if err != nil {
		return resources.CachePlacement{}, err
	}
	return resources.CachePlacement{
		NodeName:     placement.NodeName,
		NodeUID:      placement.NodeUID,
		Path:         placement.Path,
		ReservedSize: placement.ReservedSize,
	}, nil
}

func (r *ModelCacheReconciler) reconcileDownloaderJob(
	ctx context.Context,
	cache *v1alpha1.ModelCache,
	placement resources.CachePlacement,
) (*batchv1.Job, bool, error) {
	desired, err := r.builder.BuildCacheDownloaderJob(cache, placement)
	if err != nil {
		return nil, false, err
	}

	jobName := desired.Name
	var existing batchv1.Job
	if err := r.client.Get(ctx, types.NamespacedName{Name: jobName, Namespace: cache.Namespace}, &existing); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, false, fmt.Errorf("get Job: %w", err)
		}
		if err := r.client.Create(ctx, desired); err != nil {
			return nil, false, fmt.Errorf("create Job: %w", err)
		}
		return desired, false, nil
	}

	if !existing.DeletionTimestamp.IsZero() {
		return nil, true, nil
	}

	retryToken := cache.Annotations[RetryAnnotation]
	handledRetryToken := existing.Annotations[resources.CacheRetryTokenAnnotation]
	retryRequested := cache.Status.Phase != v1alpha1.ModelCachePhaseReady &&
		retryToken != "" &&
		retryToken != handledRetryToken
	needsReplace := retryRequested ||
		resources.CacheJobHashFromJob(&existing) != resources.CacheJobHashFromJob(desired)
	if !needsReplace {
		return &existing, false, nil
	}

	// Delete and recreate. Propagation policy ensures pods are removed.
	if err := r.client.Delete(ctx, &existing, client.PropagationPolicy(metav1.DeletePropagationForeground)); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, false, fmt.Errorf("delete stale Job: %w", err)
		}
	}
	return nil, true, nil
}

func (r *ModelCacheReconciler) projectJobStatus(
	ctx context.Context,
	cache *v1alpha1.ModelCache,
	original *v1alpha1.ModelCache,
	placement resources.CachePlacement,
	job *batchv1.Job,
) (ctrl.Result, error) {
	observed := resources.ObserveDownloaderJob(job)
	inputHash := resources.CacheInputHashFromJob(job)

	cache.Status.ObservedGeneration = cache.Generation
	cache.Status.NodeName = placement.NodeName
	cache.Status.NodeUID = placement.NodeUID
	cache.Status.Path = placement.Path
	cache.Status.ReservedSize = placement.ReservedSize.String()
	cache.Status.Size = placement.ReservedSize.String()
	cache.Status.Revision = scheduler.EffectiveRevision(cache)
	cache.Status.InputHash = inputHash
	if cache.Status.NodeUID == "" {
		node := &corev1.Node{}
		if err := r.client.Get(ctx, types.NamespacedName{Name: cache.Status.NodeName}, node); err != nil {
			return ctrl.Result{}, fmt.Errorf("get ready cache node: %w", err)
		}
		cache.Status.NodeUID = string(node.UID)
	}

	setCondition(cache, v1alpha1.CacheConditionSpecValid, metav1.ConditionTrue, v1alpha1.CacheReasonSpecValidated, "Cache spec is valid")
	setCondition(cache, v1alpha1.CacheConditionPlaced, metav1.ConditionTrue, v1alpha1.CacheReasonPlaced,
		fmt.Sprintf("Cache placed on node %s at %s", placement.NodeName, placement.Path))

	switch {
	case observed.Complete:
		cache.Status.Phase = v1alpha1.ModelCachePhaseReady
		setCondition(cache, v1alpha1.CacheConditionDownloaded, metav1.ConditionTrue, v1alpha1.CacheReasonDownloadSucceeded, "Download completed")
		setCondition(cache, v1alpha1.CacheConditionVerified, metav1.ConditionTrue, v1alpha1.CacheReasonVerified, "Cache verified")
		setCondition(cache, v1alpha1.CacheConditionReady, metav1.ConditionTrue, v1alpha1.CacheReasonCacheReady, "Cache is ready")
		return r.patchStatus(ctx, cache, original, v1alpha1.CacheReasonCacheReady, "Cache is ready")

	case observed.FailedTerminal:
		cache.Status.Phase = v1alpha1.ModelCachePhaseFailed
		setCondition(cache, v1alpha1.CacheConditionDownloaded, metav1.ConditionFalse, v1alpha1.CacheReasonDownloadFailed,
			fmt.Sprintf("Download failed: %s", observed.Message))
		setCondition(cache, v1alpha1.CacheConditionReady, metav1.ConditionFalse, v1alpha1.CacheReasonCacheFailed,
			fmt.Sprintf("Download failed: %s", observed.Message))
		return r.fail(ctx, cache, original, v1alpha1.CacheReasonDownloadFailed, observed.Message)

	default:
		cache.Status.Phase = v1alpha1.ModelCachePhaseDownloading
		setCondition(cache, v1alpha1.CacheConditionDownloaded, metav1.ConditionFalse, v1alpha1.CacheReasonDownloadRunning,
			fmt.Sprintf("Download is running (%d active, %d succeeded, %d failed)", observed.Active, observed.Succeeded, observed.Failed))
		setCondition(cache, v1alpha1.CacheConditionReady, metav1.ConditionFalse, v1alpha1.CacheReasonDownloadRunning, "Download is running")
		return r.patchStatus(ctx, cache, original, "", "")
	}
}

func (r *ModelCacheReconciler) setReady(
	ctx context.Context,
	cache *v1alpha1.ModelCache,
	original *v1alpha1.ModelCache,
) (ctrl.Result, error) {
	reservedSize, err := resource.ParseQuantity(cache.Spec.Storage.Size)
	if err != nil {
		return r.fail(ctx, cache, original, v1alpha1.CacheReasonSpecInvalid,
			fmt.Sprintf("parse storage size: %v", err))
	}
	if cache.Status.NodeUID == "" {
		node := &corev1.Node{}
		if err := r.client.Get(ctx, types.NamespacedName{Name: cache.Status.NodeName}, node); err != nil {
			return ctrl.Result{}, fmt.Errorf("get ready cache node: %w", err)
		}
		cache.Status.NodeUID = string(node.UID)
	}
	inputHash, err := resources.CacheInputHash(cache, resources.CachePlacement{
		NodeName:     cache.Status.NodeName,
		NodeUID:      cache.Status.NodeUID,
		Path:         cache.Status.Path,
		ReservedSize: reservedSize,
	})
	if err != nil {
		return r.fail(ctx, cache, original, v1alpha1.CacheReasonCacheFailed, err.Error())
	}

	cache.Status.ObservedGeneration = cache.Generation
	cache.Status.Phase = v1alpha1.ModelCachePhaseReady
	cache.Status.Revision = scheduler.EffectiveRevision(cache)
	cache.Status.InputHash = inputHash
	cache.Status.ReservedSize = reservedSize.String()
	cache.Status.Size = reservedSize.String()
	setCondition(cache, v1alpha1.CacheConditionSpecValid, metav1.ConditionTrue, v1alpha1.CacheReasonSpecValidated, "Cache spec is valid")
	setCondition(cache, v1alpha1.CacheConditionPlaced, metav1.ConditionTrue, v1alpha1.CacheReasonPlaced,
		fmt.Sprintf("Cache placed on node %s at %s", cache.Status.NodeName, cache.Status.Path))
	setCondition(cache, v1alpha1.CacheConditionDownloaded, metav1.ConditionTrue, v1alpha1.CacheReasonDownloadSucceeded, "Download completed")
	setCondition(cache, v1alpha1.CacheConditionVerified, metav1.ConditionTrue, v1alpha1.CacheReasonVerified, "Cache verified")
	setCondition(cache, v1alpha1.CacheConditionReady, metav1.ConditionTrue, v1alpha1.CacheReasonCacheReady, "Cache is ready")
	return r.patchStatus(ctx, cache, original, v1alpha1.CacheReasonCacheReady, "Cache is ready")
}

func (r *ModelCacheReconciler) setPending(
	ctx context.Context,
	cache *v1alpha1.ModelCache,
	original *v1alpha1.ModelCache,
	reason, message string,
) (ctrl.Result, error) {
	cache.Status.ObservedGeneration = cache.Generation
	cache.Status.Phase = v1alpha1.ModelCachePhasePending
	setCondition(cache, v1alpha1.CacheConditionSpecValid, metav1.ConditionTrue, v1alpha1.CacheReasonSpecValidated, "Cache spec is valid")
	if reason == v1alpha1.CacheReasonNodeLost && original.Status.Phase == v1alpha1.ModelCachePhaseReady {
		setCondition(cache, v1alpha1.CacheConditionDownloaded, metav1.ConditionTrue, v1alpha1.CacheReasonDownloadSucceeded, "Download completed before node loss")
		setCondition(cache, v1alpha1.CacheConditionVerified, metav1.ConditionTrue, v1alpha1.CacheReasonVerified, "Cache was verified before node loss")
	}
	setCondition(cache, v1alpha1.CacheConditionPlaced, metav1.ConditionFalse, reason, message)
	setCondition(cache, v1alpha1.CacheConditionReady, metav1.ConditionFalse, reason, message)
	return r.patchStatus(ctx, cache, original, reason, message)
}

func (r *ModelCacheReconciler) fail(
	ctx context.Context,
	cache *v1alpha1.ModelCache,
	original *v1alpha1.ModelCache,
	reason, message string,
) (ctrl.Result, error) {
	cache.Status.ObservedGeneration = cache.Generation
	cache.Status.Phase = v1alpha1.ModelCachePhaseFailed
	if reason == v1alpha1.CacheReasonSpecInvalid {
		setCondition(cache, v1alpha1.CacheConditionSpecValid, metav1.ConditionFalse, reason, message)
		setCondition(cache, v1alpha1.CacheConditionPlaced, metav1.ConditionUnknown, reason, "Placement was not evaluated")
		setCondition(cache, v1alpha1.CacheConditionDownloaded, metav1.ConditionUnknown, reason, "Download was not evaluated")
		setCondition(cache, v1alpha1.CacheConditionVerified, metav1.ConditionUnknown, reason, "Verification was not evaluated")
	}
	setCondition(cache, v1alpha1.CacheConditionReady, metav1.ConditionFalse, reason, message)
	return r.patchStatus(ctx, cache, original, reason, message)
}

func (r *ModelCacheReconciler) patchStatus(
	ctx context.Context,
	cache *v1alpha1.ModelCache,
	original *v1alpha1.ModelCache,
	eventReason, eventMessage string,
) (ctrl.Result, error) {
	changed := statusChanged(original.Status, cache.Status)
	if !changed {
		return requeueForCachePhase(cache.Status.Phase), nil
	}

	if err := r.client.Status().Patch(ctx, cache, client.MergeFrom(original)); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, fmt.Errorf("patch status: %w", err)
	}

	if eventReason != "" {
		if cache.Status.Phase == v1alpha1.ModelCachePhaseFailed {
			r.eventRecorder.Eventf(cache, corev1.EventTypeWarning, eventReason, "%s", eventMessage)
		} else {
			r.eventRecorder.Eventf(cache, corev1.EventTypeNormal, eventReason, "%s", eventMessage)
		}
	}

	return requeueForCachePhase(cache.Status.Phase), nil
}

func requeueForCachePhase(phase v1alpha1.ModelCachePhase) ctrl.Result {
	switch phase {
	case v1alpha1.ModelCachePhaseDownloading:
		return ctrl.Result{RequeueAfter: 10 * time.Second}
	case v1alpha1.ModelCachePhasePending:
		return ctrl.Result{RequeueAfter: pendingRequeueAfter}
	default:
		return ctrl.Result{}
	}
}

func setCondition(
	cache *v1alpha1.ModelCache,
	conditionType string,
	conditionStatus metav1.ConditionStatus,
	reason, message string,
) {
	status.SetCondition(&cache.Status.Conditions, cache.Status.ObservedGeneration, v1alpha1.Condition{
		Type:    conditionType,
		Status:  conditionStatus,
		Reason:  reason,
		Message: message,
	})
}

func statusChanged(a, b v1alpha1.ModelCacheStatus) bool {
	return a.ObservedGeneration != b.ObservedGeneration ||
		a.Phase != b.Phase ||
		a.Revision != b.Revision ||
		a.NodeName != b.NodeName ||
		a.NodeUID != b.NodeUID ||
		a.Path != b.Path ||
		a.Size != b.Size ||
		a.ReservedSize != b.ReservedSize ||
		a.InputHash != b.InputHash ||
		!a.LastUsedTime.Equal(&b.LastUsedTime) ||
		!conditionsEqual(a.Conditions, b.Conditions)
}

func conditionsEqual(a, b []v1alpha1.Condition) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Type != b[i].Type ||
			a[i].Status != b[i].Status ||
			a[i].ObservedGeneration != b[i].ObservedGeneration ||
			!a[i].LastTransitionTime.Equal(&b[i].LastTransitionTime) ||
			a[i].Reason != b[i].Reason ||
			a[i].Message != b[i].Message {
			return false
		}
	}
	return true
}

// SetupWithManager registers the reconciler with the manager.
func (r *ModelCacheReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Placement reservations live in ModelCache status. Serialize reconciles
	// so two caches cannot select the same node/path from the same stale list.
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ModelCache{}).
		Owns(&batchv1.Job{}).
		Watches(&corev1.Node{}, handler.EnqueueRequestsFromMapFunc(r.requestsForNodeChange)).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(r)
}

func (r *ModelCacheReconciler) requestsForNodeChange(
	ctx context.Context,
	node client.Object,
) []reconcile.Request {
	var caches v1alpha1.ModelCacheList
	if err := r.client.List(ctx, &caches); err != nil {
		log.FromContext(ctx).Error(err, "could not list ModelCaches after Node change", "node", node.GetName())
		return nil
	}

	requests := make([]reconcile.Request, 0)
	for i := range caches.Items {
		cache := &caches.Items[i]
		if cache.Status.Phase != v1alpha1.ModelCachePhasePending &&
			cache.Status.NodeName != node.GetName() &&
			cache.Spec.Storage.NodeName != node.GetName() {
			continue
		}
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: cache.Namespace,
				Name:      cache.Name,
			},
		})
	}
	return requests
}
