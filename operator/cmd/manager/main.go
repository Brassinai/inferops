package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	"github.com/brassinai/inferops/operator/controllers"
	controllermetrics "github.com/brassinai/inferops/operator/internal/metrics"
	"github.com/brassinai/inferops/operator/internal/resources"
	"github.com/brassinai/inferops/operator/internal/validation"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ctrl.SetLogger(zap.New(zap.UseDevMode(false)))

	if err := run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "operator failed: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	config, err := operatorConfigFromEnv()
	if err != nil {
		return err
	}
	if err := config.validate(); err != nil {
		return err
	}

	restConfig, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("load Kubernetes client configuration: %w", err)
	}
	leaderNamespace := leaderElectionNamespace()
	if messages := utilvalidation.IsDNS1123Label(leaderNamespace); len(messages) != 0 {
		return fmt.Errorf("leader election namespace %q is invalid: %s", leaderNamespace, strings.Join(messages, ", "))
	}

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme: scheme,
		Client: crclient.Options{
			Cache: &crclient.CacheOptions{
				DisableFor: []crclient.Object{&corev1.Secret{}},
			},
		},
		Metrics: server.Options{
			BindAddress: config.metricsAddr,
		},
		HealthProbeBindAddress:        config.healthAddr,
		LeaderElection:                true,
		LeaderElectionID:              "inferops-operator.inferops.dev",
		LeaderElectionNamespace:       leaderNamespace,
		LeaderElectionResourceLock:    resourcelock.LeasesResourceLock,
		LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		return fmt.Errorf("create manager: %w", err)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("add healthz check: %w", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return fmt.Errorf("add readyz check: %w", err)
	}

	eventRecorder := mgr.GetEventRecorderFor("inferops-operator")
	metricsRecorder, err := controllermetrics.NewControllerMetrics(crmetrics.Registry)
	if err != nil {
		return fmt.Errorf("register controller metrics: %w", err)
	}
	cacheReconciler, err := controllers.NewModelCacheReconciler(
		mgr.GetClient(),
		controllers.ModelCacheReconcilerConfig{
			CacheRoot:               config.cacheRoot,
			DownloaderImage:         config.downloaderImage,
			CacheNodeSelector:       config.cacheNodeSelector,
			CacheCapacityAnnotation: config.cacheCapacityAnnotation,
			CacheRequiredResources:  config.cacheRequiredResources,
			PendingRequeueAfter:     config.cachePendingRequeue,
			DownloadRequeueAfter:    config.cacheDownloadRequeue,
			Metrics:                 metricsRecorder,
		},
		eventRecorder,
	)
	if err != nil {
		return fmt.Errorf("create cache reconciler: %w", err)
	}
	if err := cacheReconciler.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("setup cache controller: %w", err)
	}
	deploymentReconciler, err := controllers.NewModelDeploymentController(
		mgr.GetClient(),
		mgr.GetScheme(),
		controllers.ModelDeploymentControllerConfig{
			CacheRoot:        config.cacheRoot,
			DefaultCacheSize: config.defaultCacheSize,
			DownloaderImage:  config.downloaderImage,
			GPUNodeSelector:  config.gpuNodeSelector,
			GPUTypeLabel:     config.gpuTypeLabel,
		},
		eventRecorder,
		metricsRecorder,
	)
	if err != nil {
		return fmt.Errorf("create ModelDeployment controller: %w", err)
	}
	if err := deploymentReconciler.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("setup ModelDeployment controller: %w", err)
	}
	runtimeReconciler, err := controllers.NewModelRuntimeController(
		mgr.GetClient(),
		eventRecorder,
		metricsRecorder,
	)
	if err != nil {
		return fmt.Errorf("create ModelRuntime controller: %w", err)
	}
	if err := runtimeReconciler.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("setup ModelRuntime controller: %w", err)
	}

	mgr.GetLogger().Info("starting operator manager")
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("start manager: %w", err)
	}
	return nil
}

type operatorConfig struct {
	healthAddr              string
	metricsAddr             string
	cacheRoot               string
	defaultCacheSize        string
	downloaderImage         string
	cacheNodeSelector       map[string]string
	cacheCapacityAnnotation string
	cacheRequiredResources  []corev1.ResourceName
	cachePendingRequeue     time.Duration
	cacheDownloadRequeue    time.Duration
	gpuNodeSelector         map[string]string
	gpuTypeLabel            string
}

func operatorConfigFromEnv() (operatorConfig, error) {
	nodeSelector, err := parseNodeSelector(os.Getenv("INFEROPS_CACHE_NODE_SELECTOR"))
	if err != nil {
		return operatorConfig{}, fmt.Errorf("parse INFEROPS_CACHE_NODE_SELECTOR: %w", err)
	}
	requiredResources, err := parseResourceNames(os.Getenv("INFEROPS_CACHE_REQUIRED_RESOURCES"))
	if err != nil {
		return operatorConfig{}, fmt.Errorf("parse INFEROPS_CACHE_REQUIRED_RESOURCES: %w", err)
	}
	gpuNodeSelector, err := parseNodeSelector(os.Getenv("INFEROPS_GPU_NODE_SELECTOR"))
	if err != nil {
		return operatorConfig{}, fmt.Errorf("parse INFEROPS_GPU_NODE_SELECTOR: %w", err)
	}
	cachePendingRequeue, err := durationFromEnv("INFEROPS_CACHE_PENDING_REQUEUE", 30*time.Second)
	if err != nil {
		return operatorConfig{}, err
	}
	cacheDownloadRequeue, err := durationFromEnv("INFEROPS_CACHE_DOWNLOAD_REQUEUE", 10*time.Second)
	if err != nil {
		return operatorConfig{}, err
	}
	return operatorConfig{
		healthAddr:              envOrDefault("INFEROPS_HEALTH_ADDR", ":8081"),
		metricsAddr:             envOrDefault("INFEROPS_METRICS_ADDR", ":8080"),
		cacheRoot:               os.Getenv("INFEROPS_CACHE_ROOT"),
		defaultCacheSize:        envOrDefault("INFEROPS_DEFAULT_CACHE_SIZE", "100Gi"),
		downloaderImage:         os.Getenv("INFEROPS_CACHE_DOWNLOADER_IMAGE"),
		cacheNodeSelector:       nodeSelector,
		cacheCapacityAnnotation: envOrDefault("INFEROPS_CACHE_CAPACITY_ANNOTATION", "inferops.dev/cache-capacity"),
		cacheRequiredResources:  requiredResources,
		cachePendingRequeue:     cachePendingRequeue,
		cacheDownloadRequeue:    cacheDownloadRequeue,
		gpuNodeSelector:         gpuNodeSelector,
		gpuTypeLabel:            envOrDefault("INFEROPS_GPU_TYPE_LABEL", "inferops.dev/gpu-type"),
	}, nil
}

func (c operatorConfig) validate() error {
	if _, err := validation.NewReconciliationValidator(c.cacheRoot); err != nil {
		return fmt.Errorf("validate cache root: %w", err)
	}
	if c.downloaderImage == "" {
		return errors.New("INFEROPS_CACHE_DOWNLOADER_IMAGE is required")
	}
	if err := resources.ValidatePinnedImage(c.downloaderImage); err != nil {
		return fmt.Errorf("validate INFEROPS_CACHE_DOWNLOADER_IMAGE: %w", err)
	}
	if messages := utilvalidation.IsQualifiedName(c.cacheCapacityAnnotation); len(messages) != 0 {
		return fmt.Errorf(
			"INFEROPS_CACHE_CAPACITY_ANNOTATION %q is invalid: %s",
			c.cacheCapacityAnnotation,
			strings.Join(messages, ", "),
		)
	}
	defaultCacheSizeValue := c.defaultCacheSize
	if defaultCacheSizeValue == "" {
		defaultCacheSizeValue = "100Gi"
	}
	defaultCacheSize, err := resource.ParseQuantity(defaultCacheSizeValue)
	if err != nil || defaultCacheSize.Sign() <= 0 {
		return fmt.Errorf("INFEROPS_DEFAULT_CACHE_SIZE %q must be a positive quantity", defaultCacheSizeValue)
	}
	gpuTypeLabel := c.gpuTypeLabel
	if gpuTypeLabel == "" {
		gpuTypeLabel = "inferops.dev/gpu-type"
	}
	if messages := utilvalidation.IsQualifiedName(gpuTypeLabel); len(messages) != 0 {
		return fmt.Errorf(
			"INFEROPS_GPU_TYPE_LABEL %q is invalid: %s",
			gpuTypeLabel,
			strings.Join(messages, ", "),
		)
	}
	return nil
}

func envOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func durationFromEnv(key string, defaultValue time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", key)
	}
	return duration, nil
}

func parseNodeSelector(value string) (map[string]string, error) {
	if value == "" {
		return nil, nil
	}
	result := make(map[string]string)
	for _, pair := range strings.Split(value, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			return nil, errors.New("selector contains an empty entry")
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("selector entry %q must use key=value syntax", pair)
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if messages := utilvalidation.IsQualifiedName(key); len(messages) != 0 {
			return nil, fmt.Errorf("selector key %q is invalid: %s", key, strings.Join(messages, ", "))
		}
		if messages := utilvalidation.IsValidLabelValue(val); len(messages) != 0 {
			return nil, fmt.Errorf("selector value %q for key %q is invalid: %s", val, key, strings.Join(messages, ", "))
		}
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("selector key %q is repeated", key)
		}
		result[key] = val
	}
	return result, nil
}

func parseResourceNames(value string) ([]corev1.ResourceName, error) {
	if value == "" {
		return nil, nil
	}
	seen := make(map[corev1.ResourceName]struct{})
	result := make([]corev1.ResourceName, 0)
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			return nil, errors.New("resource list contains an empty entry")
		}
		if messages := utilvalidation.IsQualifiedName(item); len(messages) != 0 {
			return nil, fmt.Errorf("resource name %q is invalid: %s", item, strings.Join(messages, ", "))
		}
		name := corev1.ResourceName(item)
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("resource name %q is repeated", name)
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result, nil
}

func leaderElectionNamespace() string {
	if ns := os.Getenv("INFEROPS_OPERATOR_NAMESPACE"); ns != "" {
		return strings.TrimSpace(ns)
	}
	if data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
		if namespace := strings.TrimSpace(string(data)); namespace != "" {
			return namespace
		}
	}
	return "inferops-system"
}
