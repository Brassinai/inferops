package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	dashboardserver "github.com/brassinai/inferops/dashboard/internal/server"
	"github.com/brassinai/inferops/internal/health"
	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

const (
	defaultAddress = ":8080"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "dashboard failed: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	address := envOrDefault("INFEROPS_DASHBOARD_ADDRESS", defaultAddress)
	namespace := envOrDefault("INFEROPS_DASHBOARD_NAMESPACE", os.Getenv("POD_NAMESPACE"))
	timeout, err := durationFromEnv("INFEROPS_DASHBOARD_REQUEST_TIMEOUT", 5*time.Second)
	if err != nil {
		return err
	}
	maxEvents, err := intFromEnv("INFEROPS_DASHBOARD_MAX_EVENTS", 25)
	if err != nil {
		return err
	}
	kubernetesClient, err := newKubernetesClient()
	if err != nil {
		return err
	}
	server, err := dashboardserver.New(kubernetesClient, dashboardserver.Options{
		Namespace:      namespace,
		PrometheusURL:  os.Getenv("INFEROPS_DASHBOARD_PROMETHEUS_URL"),
		GatewayBaseURL: os.Getenv("INFEROPS_DASHBOARD_GATEWAY_BASE_URL"),
		RequestTimeout: timeout,
		MaxEvents:      maxEvents,
	})
	if err != nil {
		return fmt.Errorf("configure dashboard server: %w", err)
	}
	return health.RunWithHandler(ctx, address, server.Handler())
}

func newKubernetesClient() (client.Client, error) {
	restConfig, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("load Kubernetes REST configuration: %w", err)
	}
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register InferOps API: %w", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register core Kubernetes API: %w", err)
	}
	kubernetesClient, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes client: %w", err)
	}
	return kubernetesClient, nil
}

func envOrDefault(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func durationFromEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	if parsed < time.Second {
		return 0, fmt.Errorf("%s must be at least one second", name)
	}
	return parsed, nil
}

func intFromEnv(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	if parsed < 0 {
		return 0, fmt.Errorf("%s must be non-negative", name)
	}
	return parsed, nil
}
