package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

const (
	defaultAddress           = ":8080"
	defaultHeartbeatInterval = 30 * time.Second
)

type config struct {
	address           string
	cloudAPIURL       string
	providerAccountID string
	providerClusterID string
	namespace         string
	region            string
	inferopsVersion   string
	agentVersion      string
	meshProvider      string
	meshHostname      string
	meshIP            string
	meshEndpointURL   string
	credentialFile    string
	gatewayTokenFile  string
	heartbeatInterval time.Duration
	discoveryRequired bool
}

type agent struct {
	config          config
	client          *http.Client
	discovery       clusterDiscovery
	lastHeartbeatOK atomic.Bool
}

type clusterDiscovery interface {
	Snapshot(context.Context) (clusterSnapshot, error)
}

type clusterSnapshot struct {
	ModelDeployments int                `json:"modelDeployments"`
	ModelRuntimes    int                `json:"modelRuntimes"`
	ModelCaches      int                `json:"modelCaches"`
	Version          string             `json:"version,omitempty"`
	AgentVersion     string             `json:"agentVersion,omitempty"`
	Region           string             `json:"region,omitempty"`
	Offerings        []offeringSnapshot `json:"offerings,omitempty"`
	Nodes            []nodeSnapshot     `json:"nodes,omitempty"`
	DiscoveryError   string             `json:"discoveryError,omitempty"`
}

type offeringSnapshot struct {
	Name                   string            `json:"name"`
	Namespace              string            `json:"namespace"`
	Phase                  string            `json:"phase,omitempty"`
	Endpoint               string            `json:"endpoint,omitempty"`
	Model                  string            `json:"model"`
	Runtime                string            `json:"runtime"`
	EndpointPath           string            `json:"endpointPath"`
	CustomEndpointsPresent bool              `json:"customEndpointsPresent"`
	GPU                    gpuSnapshot       `json:"gpu"`
	Region                 string            `json:"region"`
	Pricing                pricingSnapshot   `json:"pricing"`
	ObservedCapacity       capacitySnapshot  `json:"observedCapacity"`
	RateLimits             rateLimitSnapshot `json:"rateLimits"`
}

type gpuSnapshot struct {
	Vendor   string `json:"vendor"`
	Type     string `json:"type"`
	Count    int    `json:"count"`
	MemoryGB int    `json:"memoryGB"`
}

type pricingSnapshot struct {
	Unit     string `json:"unit"`
	PriceUSD string `json:"priceUSD"`
}

type capacitySnapshot struct {
	ActiveRequests             int     `json:"activeRequests"`
	QueueDepth                 int     `json:"queueDepth"`
	MaxConcurrentRequests      int     `json:"maxConcurrentRequests"`
	TokensPerSecondEstimate    float64 `json:"tokensPerSecondEstimate"`
	P95FirstTokenLatencyMillis int     `json:"p95FirstTokenLatencyMillis"`
	P95TotalLatencyMillis      int     `json:"p95TotalLatencyMillis"`
	ErrorRatePercent           float64 `json:"errorRatePercent"`
	GPUUtilizationPercent      float64 `json:"gpuUtilizationPercent"`
	GPUMemoryPressurePercent   float64 `json:"gpuMemoryPressurePercent"`
	EstimatedQueueDelayMillis  int     `json:"estimatedQueueDelayMillis"`
}

type rateLimitSnapshot struct {
	MaxConcurrentRequests int `json:"maxConcurrentRequests"`
	MaxQueueDepth         int `json:"maxQueueDepth"`
	MaxRequestsPerMinute  int `json:"maxRequestsPerMinute"`
	MaxTokensPerMinute    int `json:"maxTokensPerMinute"`
}

type nodeSnapshot struct {
	Name        string            `json:"name"`
	Allocatable map[string]string `json:"allocatable,omitempty"`
}

type heartbeatPayload struct {
	ProviderAccountID  string          `json:"providerAccountId"`
	ProviderClusterID  string          `json:"providerClusterId"`
	MeshProvider       string          `json:"meshProvider"`
	MeshHostname       string          `json:"meshHostname"`
	MeshIP             string          `json:"meshIp,omitempty"`
	MeshEndpointURL    string          `json:"meshEndpointUrl"`
	GatewayBearerToken string          `json:"gatewayBearerToken"`
	InferOps           clusterSnapshot `json:"inferops"`
}

type heartbeatResponse struct {
	OfferingsCount int                `json:"offeringsCount"`
	OfferingSync   offeringSyncResult `json:"offeringSync"`
	State          string             `json:"state"`
}

type offeringSyncResult struct {
	Discovered int      `json:"discovered"`
	Registered int      `json:"registered"`
	Rejected   int      `json:"rejected"`
	Errors     []string `json:"errors"`
}

type kubernetesDiscovery struct {
	client    dynamic.Interface
	namespace string
	region    string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "cloud agent failed: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := configFromEnvironment()
	if err != nil {
		return err
	}
	discovery, err := newKubernetesDiscovery(cfg.namespace, cfg.region)
	if err != nil {
		if cfg.discoveryRequired {
			return err
		}
		log.Printf("Kubernetes discovery disabled: %v", err)
	}
	agent := &agent{
		config:    cfg,
		client:    &http.Client{Timeout: 15 * time.Second},
		discovery: discovery,
	}

	metricsRegistry := prometheus.NewRegistry()
	metricsRegistry.MustRegister(
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !agent.lastHeartbeatOK.Load() {
			http.Error(w, "cloud heartbeat has not succeeded", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready\n"))
	})
	mux.Handle("/metrics", promhttp.HandlerFor(metricsRegistry, promhttp.HandlerOpts{}))

	server := &http.Server{
		Addr:              cfg.address,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()
	go agent.heartbeatLoop(ctx)

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown HTTP server: %w", err)
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("run HTTP server: %w", err)
	}
}

func (a *agent) heartbeatLoop(ctx context.Context) {
	if err := a.sendHeartbeat(ctx); err != nil {
		log.Printf("heartbeat failed: %v", err)
	}
	ticker := time.NewTicker(a.config.heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.sendHeartbeat(ctx); err != nil {
				a.lastHeartbeatOK.Store(false)
				log.Printf("heartbeat failed: %v", err)
				continue
			}
		}
	}
}

func (a *agent) sendHeartbeat(ctx context.Context) error {
	heartbeatCtx, cancel := context.WithTimeout(ctx, heartbeatTimeout(a.config.heartbeatInterval))
	defer cancel()

	credential, err := readSecretFile(a.config.credentialFile)
	if err != nil {
		return err
	}
	gatewayToken, err := readSecretFile(a.config.gatewayTokenFile)
	if err != nil {
		return err
	}
	inferOpsSnapshot, err := a.discoverySnapshot(heartbeatCtx)
	if err != nil {
		return err
	}
	log.Printf(
		"heartbeat snapshot: modelDeployments=%d modelRuntimes=%d modelCaches=%d offerings=%d nodes=%d",
		inferOpsSnapshot.ModelDeployments,
		inferOpsSnapshot.ModelRuntimes,
		inferOpsSnapshot.ModelCaches,
		len(inferOpsSnapshot.Offerings),
		len(inferOpsSnapshot.Nodes),
	)
	body := heartbeatPayload{
		ProviderAccountID:  a.config.providerAccountID,
		ProviderClusterID:  a.config.providerClusterID,
		MeshProvider:       a.config.meshProvider,
		MeshHostname:       a.config.meshHostname,
		MeshIP:             a.config.meshIP,
		MeshEndpointURL:    a.config.meshEndpointURL,
		GatewayBearerToken: gatewayToken,
		InferOps:           inferOpsSnapshot,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode heartbeat: %w", err)
	}
	request, err := http.NewRequestWithContext(
		heartbeatCtx,
		http.MethodPost,
		a.config.cloudAPIURL+"/v1/provider-agent/heartbeat",
		bytes.NewReader(payload),
	)
	if err != nil {
		return fmt.Errorf("build heartbeat request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+credential)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "inferops-cloud-agent")

	response, err := a.client.Do(request)
	if err != nil {
		return fmt.Errorf("send heartbeat: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("cloud API returned HTTP %d", response.StatusCode)
	}
	var responseBody heartbeatResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&responseBody); err == nil {
		sync := responseBody.OfferingSync
		if sync.Discovered > 0 || sync.Registered > 0 || sync.Rejected > 0 {
			log.Printf(
				"heartbeat accepted: state=%s offeringsCount=%d offeringSync discovered=%d registered=%d rejected=%d",
				responseBody.State,
				responseBody.OfferingsCount,
				sync.Discovered,
				sync.Registered,
				sync.Rejected,
			)
		}
		for _, syncError := range sync.Errors {
			log.Printf("offering sync rejected: %s", syncError)
		}
	}
	a.lastHeartbeatOK.Store(true)
	return nil
}

func configFromEnvironment() (config, error) {
	cfg := config{
		address:           getenvDefault("INFEROPS_CLOUD_AGENT_ADDRESS", defaultAddress),
		cloudAPIURL:       strings.TrimRight(os.Getenv("INFEROPS_CLOUD_API_URL"), "/"),
		providerAccountID: strings.TrimSpace(os.Getenv("INFEROPS_PROVIDER_ACCOUNT_ID")),
		providerClusterID: strings.TrimSpace(os.Getenv("INFEROPS_PROVIDER_CLUSTER_ID")),
		namespace:         strings.TrimSpace(os.Getenv("POD_NAMESPACE")),
		region:            getenvDefault("INFEROPS_CLOUD_REGION", "local-dev"),
		inferopsVersion:   getenvDefault("INFEROPS_VERSION", "unknown"),
		agentVersion:      getenvDefault("INFEROPS_CLOUD_AGENT_VERSION", "inferops-cloud-agent"),
		meshProvider:      strings.TrimSpace(os.Getenv("INFEROPS_MESH_PROVIDER")),
		meshHostname:      strings.TrimSpace(os.Getenv("INFEROPS_MESH_HOSTNAME")),
		meshIP:            strings.TrimSpace(os.Getenv("INFEROPS_MESH_IP")),
		meshEndpointURL:   strings.TrimSpace(os.Getenv("INFEROPS_MESH_ENDPOINT_URL")),
		credentialFile:    strings.TrimSpace(os.Getenv("INFEROPS_CLOUD_AGENT_CREDENTIAL_FILE")),
		gatewayTokenFile:  strings.TrimSpace(os.Getenv("INFEROPS_GATEWAY_AUTH_TOKEN_FILE")),
		heartbeatInterval: defaultHeartbeatInterval,
		discoveryRequired: true,
	}
	if raw := strings.TrimSpace(os.Getenv("INFEROPS_CLOUD_DISCOVERY_REQUIRED")); raw != "" {
		required, err := parseBool(raw)
		if err != nil {
			return config{}, fmt.Errorf("parse INFEROPS_CLOUD_DISCOVERY_REQUIRED: %w", err)
		}
		cfg.discoveryRequired = required
	}
	if raw := strings.TrimSpace(os.Getenv("INFEROPS_CLOUD_HEARTBEAT_INTERVAL_SECONDS")); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil {
			return config{}, fmt.Errorf("parse INFEROPS_CLOUD_HEARTBEAT_INTERVAL_SECONDS: %w", err)
		}
		if seconds < 5 {
			return config{}, errors.New("INFEROPS_CLOUD_HEARTBEAT_INTERVAL_SECONDS must be at least 5")
		}
		cfg.heartbeatInterval = time.Duration(seconds) * time.Second
	}
	missing := missingConfig(cfg)
	if len(missing) > 0 {
		return config{}, fmt.Errorf("missing required environment: %s", strings.Join(missing, ", "))
	}
	if err := validateURL(cfg.cloudAPIURL, true); err != nil {
		return config{}, fmt.Errorf("validate INFEROPS_CLOUD_API_URL: %w", err)
	}
	if err := validateURL(cfg.meshEndpointURL, false); err != nil {
		return config{}, fmt.Errorf("validate INFEROPS_MESH_ENDPOINT_URL: %w", err)
	}
	return cfg, nil
}

func missingConfig(cfg config) []string {
	var missing []string
	required := []struct {
		name  string
		value string
	}{
		{"INFEROPS_CLOUD_API_URL", cfg.cloudAPIURL},
		{"INFEROPS_PROVIDER_ACCOUNT_ID", cfg.providerAccountID},
		{"INFEROPS_PROVIDER_CLUSTER_ID", cfg.providerClusterID},
		{"POD_NAMESPACE", cfg.namespace},
		{"INFEROPS_MESH_PROVIDER", cfg.meshProvider},
		{"INFEROPS_MESH_HOSTNAME", cfg.meshHostname},
		{"INFEROPS_MESH_ENDPOINT_URL", cfg.meshEndpointURL},
		{"INFEROPS_CLOUD_AGENT_CREDENTIAL_FILE", cfg.credentialFile},
		{"INFEROPS_GATEWAY_AUTH_TOKEN_FILE", cfg.gatewayTokenFile},
	}
	for _, item := range required {
		if strings.TrimSpace(item.value) == "" {
			missing = append(missing, item.name)
		}
	}
	return missing
}

func (a *agent) discoverySnapshot(ctx context.Context) (clusterSnapshot, error) {
	if a.discovery == nil {
		snapshot := clusterSnapshot{DiscoveryError: "Kubernetes discovery is disabled"}
		if a.config.discoveryRequired {
			return snapshot, errors.New(snapshot.DiscoveryError)
		}
		return snapshot, nil
	}
	snapshot, err := a.discovery.Snapshot(ctx)
	if err != nil {
		snapshot.DiscoveryError = err.Error()
		if a.config.discoveryRequired {
			return snapshot, err
		}
	}
	snapshot.Version = firstNonEmptyString(snapshot.Version, a.config.inferopsVersion)
	snapshot.AgentVersion = firstNonEmptyString(snapshot.AgentVersion, a.config.agentVersion)
	snapshot.Region = firstNonEmptyString(snapshot.Region, a.config.region)
	return snapshot, nil
}

func newKubernetesDiscovery(namespace, region string) (*kubernetesDiscovery, error) {
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("load in-cluster Kubernetes config: %w", err)
	}
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes discovery client: %w", err)
	}
	return &kubernetesDiscovery{client: dynamicClient, namespace: namespace, region: region}, nil
}

func (d *kubernetesDiscovery) Snapshot(ctx context.Context) (clusterSnapshot, error) {
	deployments, err := d.client.Resource(schema.GroupVersionResource{
		Group:    "inference.inferops.dev",
		Version:  "v1alpha1",
		Resource: "modeldeployments",
	}).Namespace(d.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return clusterSnapshot{}, fmt.Errorf("list ModelDeployments: %w", err)
	}
	runtimes, err := d.client.Resource(schema.GroupVersionResource{
		Group:    "inference.inferops.dev",
		Version:  "v1alpha1",
		Resource: "modelruntimes",
	}).Namespace(d.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return clusterSnapshot{}, fmt.Errorf("list ModelRuntimes: %w", err)
	}
	caches, err := d.client.Resource(schema.GroupVersionResource{
		Group:    "inference.inferops.dev",
		Version:  "v1alpha1",
		Resource: "modelcaches",
	}).Namespace(d.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return clusterSnapshot{}, fmt.Errorf("list ModelCaches: %w", err)
	}
	nodes, err := d.client.Resource(schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "nodes",
	}).List(ctx, metav1.ListOptions{})
	if err != nil {
		return clusterSnapshot{}, fmt.Errorf("list Nodes: %w", err)
	}

	snapshot := clusterSnapshot{
		ModelDeployments: len(deployments.Items),
		ModelRuntimes:    len(runtimes.Items),
		ModelCaches:      len(caches.Items),
		Region:           d.region,
		Offerings:        modelOfferings(deployments.Items, d.namespace, d.region, nodeGPU(nodes.Items)),
		Nodes:            nodeCapacity(nodes.Items),
	}
	return snapshot, nil
}

func modelOfferings(items []unstructured.Unstructured, namespace, region string, defaultGPU gpuSnapshot) []offeringSnapshot {
	offerings := make([]offeringSnapshot, 0, len(items))
	for _, item := range items {
		if offering, ok := modelDeploymentOffering(item, namespace, region, defaultGPU); ok {
			offerings = append(offerings, offering)
		}
	}
	sort.Slice(offerings, func(i, j int) bool {
		return offerings[i].Name < offerings[j].Name
	})
	return offerings
}

func modelDeploymentOffering(item unstructured.Unstructured, namespace, region string, defaultGPU gpuSnapshot) (offeringSnapshot, bool) {
	spec, _, _ := unstructured.NestedMap(item.Object, "spec")
	status, _, _ := unstructured.NestedMap(item.Object, "status")
	modelMap, _, _ := unstructured.NestedMap(spec, "model")
	statusModelMap, _, _ := unstructured.NestedMap(status, "model")
	runtimeMap, _, _ := unstructured.NestedMap(spec, "runtime")
	routingMap, _, _ := unstructured.NestedMap(spec, "routing")
	endpointMap, _, _ := unstructured.NestedMap(status, "endpoint")
	capacityMap, _, _ := unstructured.NestedMap(status, "capacity")
	metricsMap, _, _ := unstructured.NestedMap(status, "metrics")
	rateLimitMap, _, _ := unstructured.NestedMap(status, "rateLimits")
	pricingMap, _, _ := unstructured.NestedMap(status, "pricing")
	if len(pricingMap) == 0 {
		pricingMap, _, _ = unstructured.NestedMap(spec, "pricing")
	}

	name := item.GetName()
	phase := firstNestedString(status, "phase")
	endpoint := firstNonEmptyString(firstNestedString(status, "endpoint"), firstNestedString(endpointMap, "url"), firstNestedString(endpointMap, "path"), firstNestedString(routingMap, "path"))
	model := firstNonEmptyString(
		firstNestedString(status, "modelName"),
		firstNestedString(statusModelMap, "repo"),
		firstNestedString(statusModelMap, "name"),
		firstNestedString(status, "model"),
		firstNestedString(spec, "modelName"),
		firstNestedString(modelMap, "repo"),
		firstNestedString(modelMap, "name"),
		name,
	)
	runtime := firstNonEmptyString(firstNestedString(status, "runtime"), firstNestedString(runtimeMap, "ref"), firstNestedString(spec, "runtime"), "unknown")
	endpointPath := firstNonEmptyString(firstNestedString(endpointMap, "basePath"), firstNestedString(status, "endpointPath"), firstNestedString(spec, "endpointPath"), "/v1")
	openAICompatible := firstNestedValue(routingMap, "openAICompatible")
	customEndpointsPresent := boolFromAny(firstNestedValue(status, "customEndpointsPresent")) ||
		boolFromAny(firstNestedValue(spec, "customEndpointsPresent")) ||
		(openAICompatible != nil && !boolFromAny(openAICompatible)) ||
		endpointPath != "/v1"

	offering := offeringSnapshot{
		Name:                   name,
		Namespace:              namespace,
		Phase:                  phase,
		Endpoint:               endpoint,
		Model:                  model,
		Runtime:                runtime,
		EndpointPath:           endpointPath,
		CustomEndpointsPresent: customEndpointsPresent,
		GPU:                    gpuFromDeployment(status, spec, defaultGPU),
		Region:                 firstNonEmptyString(firstNestedString(status, "region"), firstNestedString(spec, "region"), region, "local-dev"),
		Pricing: pricingSnapshot{
			Unit:     firstNonEmptyString(firstNestedString(pricingMap, "unit"), "million_input_tokens"),
			PriceUSD: firstNonEmptyString(firstNestedString(pricingMap, "priceUSD"), firstNestedString(pricingMap, "priceUsd"), "0.01"),
		},
		ObservedCapacity: capacitySnapshot{
			ActiveRequests:             intFromAny(firstNonEmptyAny(firstNestedValue(metricsMap, "activeRequests"), firstNestedValue(capacityMap, "activeRequests"))),
			QueueDepth:                 intFromAny(firstNonEmptyAny(firstNestedValue(metricsMap, "queueDepth"), firstNestedValue(capacityMap, "queueDepth"))),
			MaxConcurrentRequests:      maxInt(1, intFromAny(firstNonEmptyAny(firstNestedValue(metricsMap, "maxConcurrentRequests"), firstNestedValue(capacityMap, "maxConcurrentRequests"), 1))),
			TokensPerSecondEstimate:    positiveFloatOrDefault(firstNonEmptyAny(firstNestedValue(metricsMap, "tokensPerSecondEstimate"), firstNestedValue(capacityMap, "tokensPerSecondEstimate")), 1),
			P95FirstTokenLatencyMillis: intFromAny(firstNonEmptyAny(firstNestedValue(metricsMap, "p95FirstTokenLatencyMillis"), firstNestedValue(capacityMap, "p95FirstTokenLatencyMillis"))),
			P95TotalLatencyMillis:      intFromAny(firstNonEmptyAny(firstNestedValue(metricsMap, "p95TotalLatencyMillis"), firstNestedValue(capacityMap, "p95TotalLatencyMillis"))),
			ErrorRatePercent:           floatFromAny(firstNonEmptyAny(firstNestedValue(metricsMap, "errorRatePercent"), firstNestedValue(capacityMap, "errorRatePercent"))),
			GPUUtilizationPercent:      floatFromAny(firstNonEmptyAny(firstNestedValue(metricsMap, "gpuUtilizationPercent"), firstNestedValue(capacityMap, "gpuUtilizationPercent"))),
			GPUMemoryPressurePercent:   floatFromAny(firstNonEmptyAny(firstNestedValue(metricsMap, "gpuMemoryPressurePercent"), firstNestedValue(capacityMap, "gpuMemoryPressurePercent"))),
			EstimatedQueueDelayMillis:  intFromAny(firstNonEmptyAny(firstNestedValue(metricsMap, "estimatedQueueDelayMillis"), firstNestedValue(capacityMap, "estimatedQueueDelayMillis"))),
		},
		RateLimits: rateLimitSnapshot{
			MaxConcurrentRequests: maxInt(1, intFromAny(firstNonEmptyAny(firstNestedValue(rateLimitMap, "maxConcurrentRequests"), firstNestedValue(spec, "maxConcurrentRequests"), 1))),
			MaxQueueDepth:         intFromAny(firstNonEmptyAny(firstNestedValue(rateLimitMap, "maxQueueDepth"), firstNestedValue(spec, "maxQueueDepth"))),
			MaxRequestsPerMinute:  maxInt(1, intFromAny(firstNonEmptyAny(firstNestedValue(rateLimitMap, "maxRequestsPerMinute"), firstNestedValue(spec, "maxRequestsPerMinute"), 60))),
			MaxTokensPerMinute:    maxInt(1, intFromAny(firstNonEmptyAny(firstNestedValue(rateLimitMap, "maxTokensPerMinute"), firstNestedValue(spec, "maxTokensPerMinute"), 1000))),
		},
	}
	return offering, strings.TrimSpace(offering.Model) != ""
}

func nodeCapacity(items []unstructured.Unstructured) []nodeSnapshot {
	nodes := make([]nodeSnapshot, 0, len(items))
	for _, item := range items {
		allocatable, _, _ := unstructured.NestedStringMap(item.Object, "status", "allocatable")
		filtered := make(map[string]string)
		for name, value := range allocatable {
			if name == "cpu" || name == "memory" || isGPUResource(name) {
				filtered[name] = value
			}
		}
		nodes = append(nodes, nodeSnapshot{
			Name:        item.GetName(),
			Allocatable: filtered,
		})
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Name < nodes[j].Name
	})
	return nodes
}

func nodeGPU(items []unstructured.Unstructured) gpuSnapshot {
	gpu := gpuSnapshot{Vendor: "unknown", Type: "unknown", Count: 0, MemoryGB: 1}
	for _, item := range items {
		labels := item.GetLabels()
		allocatable, _, _ := unstructured.NestedStringMap(item.Object, "status", "allocatable")
		count := parseQuantity(allocatable["nvidia.com/gpu"])
		if count == 0 {
			count = parseQuantity(allocatable["amd.com/gpu"])
		}
		if count == 0 {
			count = parseQuantity(allocatable["gpu.intel.com/i915"])
		}
		gpu.Count += count
		if gpu.Type == "unknown" {
			gpu.Type = firstNonEmptyString(labels["nvidia.com/gpu.product"], labels["gpu.product"], labels["accelerator"])
			if gpu.Type == "" {
				gpu.Type = "unknown"
			}
		}
		switch {
		case strings.Contains(strings.ToLower(gpu.Type), "amd") || allocatable["amd.com/gpu"] != "":
			gpu.Vendor = "AMD"
		case strings.Contains(strings.ToLower(gpu.Type), "intel") || allocatable["gpu.intel.com/i915"] != "":
			gpu.Vendor = "Intel"
		case count > 0:
			gpu.Vendor = "NVIDIA"
		}
		if memory := parseQuantity(firstNonEmptyString(allocatable["nvidia.com/gpu.memory"], allocatable["gpu.memory"])); memory > 0 {
			gpu.MemoryGB = memory
		}
	}
	if gpu.Count <= 0 {
		gpu.Count = 1
	}
	return gpu
}

func gpuFromDeployment(status, spec map[string]any, fallback gpuSnapshot) gpuSnapshot {
	gpuMap, _, _ := unstructured.NestedMap(status, "gpu")
	if len(gpuMap) == 0 {
		gpuMap, _, _ = unstructured.NestedMap(spec, "gpu")
	}
	gpu := gpuSnapshot{
		Vendor:   firstNonEmptyString(firstNestedString(gpuMap, "vendor"), fallback.Vendor, "unknown"),
		Type:     firstNonEmptyString(firstNestedString(gpuMap, "type"), fallback.Type, "unknown"),
		Count:    maxInt(1, intFromAny(firstNonEmptyAny(firstNestedValue(gpuMap, "count"), fallback.Count))),
		MemoryGB: maxInt(1, intFromAny(firstNonEmptyAny(firstNestedValue(gpuMap, "memoryGB"), firstNestedValue(gpuMap, "memoryGb"), fallback.MemoryGB))),
	}
	return gpu
}

func isGPUResource(name string) bool {
	return strings.Contains(strings.ToLower(name), "gpu")
}

func firstNestedValue(parent map[string]any, key string) any {
	if value, ok := parent[key]; ok && nonEmptyAny(value) {
		return value
	}
	return nil
}

func firstNestedString(parent map[string]any, key string) string {
	value := firstNestedValue(parent, key)
	if value == nil {
		return ""
	}
	switch value.(type) {
	case map[string]any, map[string]string, []any, []string:
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func firstNonEmptyAny(values ...any) any {
	for _, value := range values {
		if nonEmptyAny(value) {
			return value
		}
	}
	return nil
}

func nonEmptyAny(value any) bool {
	return value != nil && strings.TrimSpace(fmt.Sprint(value)) != "" && fmt.Sprint(value) != "<nil>"
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func boolFromAny(value any) bool {
	parsed, _ := strconv.ParseBool(strings.TrimSpace(fmt.Sprint(value)))
	return parsed
}

func intFromAny(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(v))
		return parsed
	default:
		return 0
	}
}

func floatFromAny(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case string:
		parsed, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return parsed
	default:
		return 0
	}
}

func positiveFloatOrDefault(value any, fallback float64) float64 {
	parsed := floatFromAny(value)
	if parsed <= 0 {
		return fallback
	}
	return parsed
}

func parseQuantity(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	value = strings.TrimSuffix(value, "Gi")
	value = strings.TrimSuffix(value, "G")
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}

func maxInt(minimum, value int) int {
	if value < minimum {
		return minimum
	}
	return value
}

func readSecretFile(path string) (string, error) {
	value, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read credential file: %w", err)
	}
	trimmed := strings.TrimSpace(string(value))
	if trimmed == "" {
		return "", errors.New("credential file is empty")
	}
	return trimmed, nil
}

func getenvDefault(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func parseBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "t", "true", "y", "yes", "on":
		return true, nil
	case "0", "f", "false", "n", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean %q", value)
	}
}

func heartbeatTimeout(interval time.Duration) time.Duration {
	if interval <= 0 {
		return defaultHeartbeatInterval
	}
	if interval < 30*time.Second {
		return interval
	}
	return 30 * time.Second
}

func validateURL(value string, requireHTTPSForRemote bool) error {
	parsed, err := url.Parse(value)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("URL must use http or https")
	}
	if parsed.Host == "" {
		return errors.New("URL host is required")
	}
	if requireHTTPSForRemote && parsed.Scheme == "http" && !isLocalHost(parsed.Hostname()) {
		return errors.New("URL must use https unless it targets a local development host")
	}
	return nil
}

func isLocalHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1", "host.docker.internal", "gateway.docker.internal", "kubernetes.docker.internal":
		return true
	default:
		return false
	}
}
