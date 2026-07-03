package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/brassinai/inferops/gateway/internal/auth"
	"github.com/brassinai/inferops/gateway/internal/discovery"
	gatewaymetrics "github.com/brassinai/inferops/gateway/internal/metrics"
	"github.com/brassinai/inferops/gateway/internal/proxy"
	"github.com/brassinai/inferops/gateway/internal/routing"
	"github.com/brassinai/inferops/internal/health"
	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

const (
	defaultAddress      = ":8080"
	defaultRegistryMode = "kubernetes"
	defaultSyncInterval = 5 * time.Second
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "gateway failed: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	address := strings.TrimSpace(os.Getenv("INFEROPS_GATEWAY_ADDRESS"))
	if address == "" {
		address = defaultAddress
	}
	registryMode := strings.ToLower(strings.TrimSpace(os.Getenv("INFEROPS_GATEWAY_REGISTRY")))
	if registryMode == "" {
		registryMode = defaultRegistryMode
	}

	registry := routing.NewMemoryRegistry()
	ready := func() bool { return true }
	switch registryMode {
	case "fake":
		// The empty in-memory registry deliberately requires no operator or
		// Kubernetes API. Gateway package tests populate this registry directly.
	case "kubernetes":
		namespace := strings.TrimSpace(os.Getenv("POD_NAMESPACE"))
		if namespace == "" {
			return errors.New("POD_NAMESPACE is required for Kubernetes discovery")
		}
		syncInterval, err := syncIntervalFromEnvironment()
		if err != nil {
			return err
		}
		kubernetesClient, err := newKubernetesClient()
		if err != nil {
			return fmt.Errorf("create Kubernetes discovery client: %w", err)
		}
		modelDiscovery, err := discovery.New(
			kubernetesClient,
			registry,
			namespace,
			syncInterval,
			log.Default(),
		)
		if err != nil {
			return fmt.Errorf("configure Kubernetes discovery: %w", err)
		}
		ready = modelDiscovery.Ready
		go modelDiscovery.Run(ctx)
	default:
		return fmt.Errorf("unsupported INFEROPS_GATEWAY_REGISTRY %q", registryMode)
	}

	metricsRegistry := prometheus.NewRegistry()
	metricsRecorder, err := gatewaymetrics.NewRecorder(metricsRegistry)
	if err != nil {
		return fmt.Errorf("configure gateway metrics: %w", err)
	}
	gatewayProxy, err := proxy.NewWithMetrics(registry, metricsRecorder)
	if err != nil {
		return fmt.Errorf("create gateway proxy: %w", err)
	}
	var proxyHandler http.Handler = gatewayProxy
	tokenFile := strings.TrimSpace(os.Getenv("INFEROPS_GATEWAY_AUTH_TOKEN_FILE"))
	if tokenFile != "" {
		tokenSource, err := auth.NewFileTokenSource(tokenFile)
		if err != nil {
			return fmt.Errorf("configure gateway token source: %w", err)
		}
		authenticator, err := auth.New(tokenSource)
		if err != nil {
			return fmt.Errorf("configure gateway authentication: %w", err)
		}
		proxyHandler = authenticator.Wrap(proxyHandler)
		ready = readinessWithTokenSource(ready, tokenSource)
	}
	proxyHandler = metricsRecorder.Middleware(proxyHandler)

	healthHandler := health.HandlerWithReadiness(ready)
	metricsHandler := promhttp.HandlerFor(metricsRegistry, promhttp.HandlerOpts{})
	return health.RunWithHandler(
		ctx,
		address,
		gatewayHandler(healthHandler, metricsHandler, proxyHandler),
	)
}

func readinessWithTokenSource(
	upstreamReady func() bool,
	source auth.TokenSource,
) func() bool {
	return func() bool {
		if upstreamReady == nil || !upstreamReady() || source == nil {
			return false
		}
		_, err := source.Tokens()
		return err == nil
	}
}

func gatewayHandler(healthHandler, metricsHandler, proxyHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL != nil && request.URL.RawPath == "" {
			switch request.URL.Path {
			case "/healthz", "/readyz":
				healthHandler.ServeHTTP(response, request)
				return
			case "/metrics":
				metricsHandler.ServeHTTP(response, request)
				return
			}
		}
		proxyHandler.ServeHTTP(response, request)
	})
}

func syncIntervalFromEnvironment() (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv("INFEROPS_GATEWAY_SYNC_INTERVAL"))
	if value == "" {
		return defaultSyncInterval, nil
	}
	interval, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse INFEROPS_GATEWAY_SYNC_INTERVAL: %w", err)
	}
	if interval <= 0 {
		return 0, errors.New("INFEROPS_GATEWAY_SYNC_INTERVAL must be positive")
	}
	return interval, nil
}

func newKubernetesClient() (client.Client, error) {
	restConfig, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("load Kubernetes REST configuration: %w", err)
	}
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register core Kubernetes API: %w", err)
	}
	if err := discoveryv1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register Kubernetes discovery API: %w", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register InferOps API: %w", err)
	}
	kubernetesClient, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("build Kubernetes client: %w", err)
	}
	return kubernetesClient, nil
}
