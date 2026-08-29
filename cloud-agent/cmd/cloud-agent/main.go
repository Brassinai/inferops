package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/brassinai/inferops/internal/runtimecontract"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/yaml"
)

const (
	defaultAddress            = ":8080"
	defaultHeartbeatInterval  = 30 * time.Second
	defaultRuntimeHTTPPort    = 8000
	defaultRuntimeMetricsPath = "/metrics"
	runtimeMetricsTimeout     = 2 * time.Second
	benchmarkRetryInterval    = 10 * time.Minute
	maxEvidencePods           = 10
	maxOwnerReferenceDepth    = 4
	podEvidenceBindingPolicy  = "inferops-modeldeployment-owner-reference"
)

var requiredInferOpsRuntimePodLabels = map[string]string{
	runtimecontract.LabelManagedBy: runtimecontract.ValueManagedBy,
	runtimecontract.LabelPartOf:    runtimecontract.ValuePartOf,
	runtimecontract.LabelComponent: runtimecontract.ValueRuntimeComponent,
}

type config struct {
	address                   string
	cloudAPIURL               string
	providerAccountID         string
	providerClusterID         string
	namespace                 string
	region                    string
	inferopsVersion           string
	agentVersion              string
	meshProvider              string
	meshHostname              string
	meshIP                    string
	meshEndpointURL           string
	credentialFile            string
	gatewayTokenFile          string
	heartbeatInterval         time.Duration
	discoveryRequired         bool
	benchmarkEnabled          bool
	benchmarkTimeout          time.Duration
	benchmarkMaxTokens        int
	benchmarkMaxOutputTokens  int
	benchmarkMaxContextTokens int
	benchmarkMaxConcurrency   int
	benchmarkLatencySLOMillis int
	benchmarkTotalTimeout     time.Duration
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
	Evidence               evidenceSnapshot  `json:"evidence,omitempty"`
}

type evidenceSnapshot struct {
	ManifestYAML    string         `json:"manifestYaml,omitempty"`
	PodMetadata     map[string]any `json:"podMetadata,omitempty"`
	RuntimeMetadata map[string]any `json:"runtimeMetadata,omitempty"`
	ImageMetadata   map[string]any `json:"imageMetadata,omitempty"`
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
	ActiveRequests                int     `json:"activeRequests"`
	QueueDepth                    int     `json:"queueDepth"`
	MaxConcurrentRequests         int     `json:"maxConcurrentRequests"`
	MaxConcurrentRequestsSource   string  `json:"maxConcurrentRequestsSource,omitempty"`
	TokensPerSecondEstimate       float64 `json:"tokensPerSecondEstimate"`
	InputTokensPerSecondEstimate  float64 `json:"inputTokensPerSecondEstimate,omitempty"`
	OutputTokensPerSecondEstimate float64 `json:"outputTokensPerSecondEstimate,omitempty"`
	TokensPerMinuteEstimate       int     `json:"tokensPerMinuteEstimate,omitempty"`
	ContextWindowTokens           int     `json:"contextWindowTokens,omitempty"`
	ContextWindowSource           string  `json:"contextWindowSource,omitempty"`
	MaxOutputTokens               int     `json:"maxOutputTokens,omitempty"`
	MaxOutputTokensSource         string  `json:"maxOutputTokensSource,omitempty"`
	CapacitySource                string  `json:"capacitySource,omitempty"`
	P50FirstTokenLatencyMillis    int     `json:"p50FirstTokenLatencyMillis,omitempty"`
	P95FirstTokenLatencyMillis    int     `json:"p95FirstTokenLatencyMillis"`
	P99FirstTokenLatencyMillis    int     `json:"p99FirstTokenLatencyMillis,omitempty"`
	P50TotalLatencyMillis         int     `json:"p50TotalLatencyMillis,omitempty"`
	P95TotalLatencyMillis         int     `json:"p95TotalLatencyMillis"`
	P99TotalLatencyMillis         int     `json:"p99TotalLatencyMillis,omitempty"`
	ErrorRatePercent              float64 `json:"errorRatePercent"`
	GPUUtilizationPercent         float64 `json:"gpuUtilizationPercent"`
	GPUMemoryPressurePercent      float64 `json:"gpuMemoryPressurePercent"`
	KVCacheUsagePercent           float64 `json:"kvCacheUsagePercent,omitempty"`
	PrefixCacheHitRatePercent     float64 `json:"prefixCacheHitRatePercent,omitempty"`
	CachedPromptTokensPerSecond   float64 `json:"cachedPromptTokensPerSecondEstimate,omitempty"`
	PromptCacheSupported          bool    `json:"promptCacheSupported,omitempty"`
	CachedTokenReportingSupported bool    `json:"cachedTokenReportingSupported,omitempty"`
	EstimatedQueueDelayMillis     int     `json:"estimatedQueueDelayMillis"`
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
	client                    dynamic.Interface
	metricsClient             *http.Client
	namespace                 string
	region                    string
	benchmarkEnabled          bool
	benchmarkTimeout          time.Duration
	benchmarkMaxTokens        int
	benchmarkMaxOutputTokens  int
	benchmarkMaxContextTokens int
	benchmarkMaxConcurrency   int
	benchmarkLatencySLOMillis int
	benchmarkTotalTimeout     time.Duration
	tokenSamples              map[string]tokenCounterSample
	benchmarkSamples          map[string]runtimeMetricSnapshot
	benchmarkAttempts         map[string]time.Time
}

type runtimeConfig struct {
	Engine      string
	Port        int
	MetricsPath string
	Env         map[string]string
}

type runtimeMetricSnapshot struct {
	ActiveRequests                int
	QueueDepth                    int
	InputTokensPerSecondEstimate  float64
	OutputTokensPerSecondEstimate float64
	TokensPerSecondEstimate       float64
	GPUMemoryPressurePercent      float64
	P95FirstTokenLatencyMillis    int
	P50FirstTokenLatencyMillis    int
	P99FirstTokenLatencyMillis    int
	P95TotalLatencyMillis         int
	P50TotalLatencyMillis         int
	P99TotalLatencyMillis         int
	InputTokens                   int
	OutputTokens                  int
	KVCacheUsagePercent           float64
	PrefixCacheHitRatePercent     float64
	CachedPromptTokensPerSecond   float64
	PromptCacheSupported          bool
	CachedTokenReportingSupported bool
	ContextWindowTokens           int
	ContextWindowSource           string
	MaxOutputTokens               int
	MaxOutputTokensSource         string
	MaxConcurrentRequests         int
	MaxConcurrentRequestsSource   string
	Source                        string
}

type tokenCounterSample struct {
	InputTokens        float64
	OutputTokens       float64
	PrefixCacheHits    float64
	PrefixCacheQueries float64
	CachedPromptTokens float64
	ObservedAt         time.Time
}

type automaticCapacityEstimate struct {
	InputTokensPerSecond  float64
	OutputTokensPerSecond float64
	TokensPerSecond       float64
	TokensPerMinute       int
	MaxConcurrentRequests int
	ContextWindowTokens   int
	Source                string
}

type benchmarkProbeLimits struct {
	ContextTokens       int
	OutputTokens        int
	Concurrency         int
	LatencySLOMillis    int
	ThroughputMaxTokens int
	ContextSource       string
	OutputSource        string
	ConcurrencySource   string
}

type declaredRuntimeLimits struct {
	ContextTokens     int
	ContextSource     string
	OutputTokens      int
	OutputSource      string
	Concurrency       int
	ConcurrencySource string
}

type benchmarkUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type benchmarkChatResponse struct {
	Usage benchmarkUsage `json:"usage"`
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
	discovery, err := newKubernetesDiscovery(cfg)
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
		address:                   getenvDefault("INFEROPS_CLOUD_AGENT_ADDRESS", defaultAddress),
		cloudAPIURL:               strings.TrimRight(os.Getenv("INFEROPS_CLOUD_API_URL"), "/"),
		providerAccountID:         strings.TrimSpace(os.Getenv("INFEROPS_PROVIDER_ACCOUNT_ID")),
		providerClusterID:         strings.TrimSpace(os.Getenv("INFEROPS_PROVIDER_CLUSTER_ID")),
		namespace:                 strings.TrimSpace(os.Getenv("POD_NAMESPACE")),
		region:                    getenvDefault("INFEROPS_CLOUD_REGION", "local-dev"),
		inferopsVersion:           getenvDefault("INFEROPS_VERSION", "unknown"),
		agentVersion:              getenvDefault("INFEROPS_CLOUD_AGENT_VERSION", "inferops-cloud-agent"),
		meshProvider:              strings.TrimSpace(os.Getenv("INFEROPS_MESH_PROVIDER")),
		meshHostname:              strings.TrimSpace(os.Getenv("INFEROPS_MESH_HOSTNAME")),
		meshIP:                    strings.TrimSpace(os.Getenv("INFEROPS_MESH_IP")),
		meshEndpointURL:           strings.TrimSpace(os.Getenv("INFEROPS_MESH_ENDPOINT_URL")),
		credentialFile:            strings.TrimSpace(os.Getenv("INFEROPS_CLOUD_AGENT_CREDENTIAL_FILE")),
		gatewayTokenFile:          strings.TrimSpace(os.Getenv("INFEROPS_GATEWAY_AUTH_TOKEN_FILE")),
		heartbeatInterval:         defaultHeartbeatInterval,
		discoveryRequired:         true,
		benchmarkTimeout:          10 * time.Second,
		benchmarkMaxTokens:        16,
		benchmarkMaxOutputTokens:  256,
		benchmarkMaxContextTokens: 32768,
		benchmarkMaxConcurrency:   8,
		benchmarkLatencySLOMillis: 10000,
		benchmarkTotalTimeout:     2 * time.Minute,
	}
	if raw := strings.TrimSpace(os.Getenv("INFEROPS_CLOUD_DISCOVERY_REQUIRED")); raw != "" {
		required, err := parseBool(raw)
		if err != nil {
			return config{}, fmt.Errorf("parse INFEROPS_CLOUD_DISCOVERY_REQUIRED: %w", err)
		}
		cfg.discoveryRequired = required
	}
	if raw := strings.TrimSpace(os.Getenv("INFEROPS_CLOUD_BENCHMARK_ENABLED")); raw != "" {
		enabled, err := parseBool(raw)
		if err != nil {
			return config{}, fmt.Errorf("parse INFEROPS_CLOUD_BENCHMARK_ENABLED: %w", err)
		}
		cfg.benchmarkEnabled = enabled
	}
	if raw := strings.TrimSpace(os.Getenv("INFEROPS_CLOUD_BENCHMARK_TIMEOUT_SECONDS")); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil {
			return config{}, fmt.Errorf("parse INFEROPS_CLOUD_BENCHMARK_TIMEOUT_SECONDS: %w", err)
		}
		if seconds < 1 || seconds > 60 {
			return config{}, errors.New("INFEROPS_CLOUD_BENCHMARK_TIMEOUT_SECONDS must be between 1 and 60")
		}
		cfg.benchmarkTimeout = time.Duration(seconds) * time.Second
	}
	if raw := strings.TrimSpace(os.Getenv("INFEROPS_CLOUD_BENCHMARK_MAX_TOKENS")); raw != "" {
		tokens, err := strconv.Atoi(raw)
		if err != nil {
			return config{}, fmt.Errorf("parse INFEROPS_CLOUD_BENCHMARK_MAX_TOKENS: %w", err)
		}
		if tokens < 1 || tokens > 128 {
			return config{}, errors.New("INFEROPS_CLOUD_BENCHMARK_MAX_TOKENS must be between 1 and 128")
		}
		cfg.benchmarkMaxTokens = tokens
	}
	if raw := strings.TrimSpace(os.Getenv("INFEROPS_CLOUD_BENCHMARK_MAX_OUTPUT_TOKENS")); raw != "" {
		tokens, err := strconv.Atoi(raw)
		if err != nil {
			return config{}, fmt.Errorf("parse INFEROPS_CLOUD_BENCHMARK_MAX_OUTPUT_TOKENS: %w", err)
		}
		if tokens < 1 || tokens > 4096 {
			return config{}, errors.New("INFEROPS_CLOUD_BENCHMARK_MAX_OUTPUT_TOKENS must be between 1 and 4096")
		}
		cfg.benchmarkMaxOutputTokens = tokens
	}
	if raw := strings.TrimSpace(os.Getenv("INFEROPS_CLOUD_BENCHMARK_MAX_CONTEXT_TOKENS")); raw != "" {
		tokens, err := strconv.Atoi(raw)
		if err != nil {
			return config{}, fmt.Errorf("parse INFEROPS_CLOUD_BENCHMARK_MAX_CONTEXT_TOKENS: %w", err)
		}
		if tokens < 1024 || tokens > 262144 {
			return config{}, errors.New("INFEROPS_CLOUD_BENCHMARK_MAX_CONTEXT_TOKENS must be between 1024 and 262144")
		}
		cfg.benchmarkMaxContextTokens = tokens
	}
	if raw := strings.TrimSpace(os.Getenv("INFEROPS_CLOUD_BENCHMARK_MAX_CONCURRENCY")); raw != "" {
		concurrency, err := strconv.Atoi(raw)
		if err != nil {
			return config{}, fmt.Errorf("parse INFEROPS_CLOUD_BENCHMARK_MAX_CONCURRENCY: %w", err)
		}
		if concurrency < 1 || concurrency > 64 {
			return config{}, errors.New("INFEROPS_CLOUD_BENCHMARK_MAX_CONCURRENCY must be between 1 and 64")
		}
		cfg.benchmarkMaxConcurrency = concurrency
	}
	if raw := strings.TrimSpace(os.Getenv("INFEROPS_CLOUD_BENCHMARK_LATENCY_SLO_MILLIS")); raw != "" {
		millis, err := strconv.Atoi(raw)
		if err != nil {
			return config{}, fmt.Errorf("parse INFEROPS_CLOUD_BENCHMARK_LATENCY_SLO_MILLIS: %w", err)
		}
		if millis < 100 || millis > 60000 {
			return config{}, errors.New("INFEROPS_CLOUD_BENCHMARK_LATENCY_SLO_MILLIS must be between 100 and 60000")
		}
		cfg.benchmarkLatencySLOMillis = millis
	}
	if raw := strings.TrimSpace(os.Getenv("INFEROPS_CLOUD_BENCHMARK_TOTAL_TIMEOUT_SECONDS")); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil {
			return config{}, fmt.Errorf("parse INFEROPS_CLOUD_BENCHMARK_TOTAL_TIMEOUT_SECONDS: %w", err)
		}
		if seconds < 5 || seconds > 600 {
			return config{}, errors.New("INFEROPS_CLOUD_BENCHMARK_TOTAL_TIMEOUT_SECONDS must be between 5 and 600")
		}
		cfg.benchmarkTotalTimeout = time.Duration(seconds) * time.Second
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

func newKubernetesDiscovery(cfg config) (*kubernetesDiscovery, error) {
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("load in-cluster Kubernetes config: %w", err)
	}
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes discovery client: %w", err)
	}
	return &kubernetesDiscovery{
		client:                    dynamicClient,
		metricsClient:             &http.Client{Timeout: runtimeMetricsTimeout},
		namespace:                 cfg.namespace,
		region:                    cfg.region,
		benchmarkEnabled:          cfg.benchmarkEnabled,
		benchmarkTimeout:          cfg.benchmarkTimeout,
		benchmarkMaxTokens:        cfg.benchmarkMaxTokens,
		benchmarkMaxOutputTokens:  cfg.benchmarkMaxOutputTokens,
		benchmarkMaxContextTokens: cfg.benchmarkMaxContextTokens,
		benchmarkMaxConcurrency:   cfg.benchmarkMaxConcurrency,
		benchmarkLatencySLOMillis: cfg.benchmarkLatencySLOMillis,
		benchmarkTotalTimeout:     cfg.benchmarkTotalTimeout,
		tokenSamples:              map[string]tokenCounterSample{},
		benchmarkSamples:          map[string]runtimeMetricSnapshot{},
		benchmarkAttempts:         map[string]time.Time{},
	}, nil
}

func (d *kubernetesDiscovery) Snapshot(ctx context.Context) (clusterSnapshot, error) {
	deployments, err := d.client.Resource(schema.GroupVersionResource{
		Group:    runtimecontract.ModelDeploymentAPIGroup,
		Version:  "v1alpha1",
		Resource: "modeldeployments",
	}).Namespace(d.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return clusterSnapshot{}, fmt.Errorf("list ModelDeployments: %w", err)
	}
	runtimes, err := d.client.Resource(schema.GroupVersionResource{
		Group:    runtimecontract.ModelDeploymentAPIGroup,
		Version:  "v1alpha1",
		Resource: "modelruntimes",
	}).Namespace(d.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return clusterSnapshot{}, fmt.Errorf("list ModelRuntimes: %w", err)
	}
	caches, err := d.client.Resource(schema.GroupVersionResource{
		Group:    runtimecontract.ModelDeploymentAPIGroup,
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
	pods, err := d.client.Resource(schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "pods",
	}).Namespace(d.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return clusterSnapshot{}, fmt.Errorf("list Pods: %w", err)
	}
	replicaSets, err := d.client.Resource(schema.GroupVersionResource{
		Group:    "apps",
		Version:  "v1",
		Resource: "replicasets",
	}).Namespace(d.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return clusterSnapshot{}, fmt.Errorf("list ReplicaSets: %w", err)
	}
	workloadDeployments, err := d.client.Resource(schema.GroupVersionResource{
		Group:    "apps",
		Version:  "v1",
		Resource: "deployments",
	}).Namespace(d.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return clusterSnapshot{}, fmt.Errorf("list Deployments: %w", err)
	}

	runtimeConfigs := runtimeConfigsByName(runtimes.Items)
	runtimeMetrics := d.runtimeMetricSnapshots(ctx, deployments.Items, runtimeConfigs)
	snapshot := clusterSnapshot{
		ModelDeployments: len(deployments.Items),
		ModelRuntimes:    len(runtimes.Items),
		ModelCaches:      len(caches.Items),
		Region:           d.region,
		Offerings:        modelOfferingsWithMetrics(deployments.Items, d.namespace, d.region, nodeGPU(nodes.Items), podsByModelDeployment(pods.Items, replicaSets.Items, workloadDeployments.Items), runtimeMetrics),
		Nodes:            nodeCapacity(nodes.Items),
	}
	return snapshot, nil
}

func modelOfferings(items []unstructured.Unstructured, namespace, region string, defaultGPU gpuSnapshot, podsByDeployment map[string][]unstructured.Unstructured) []offeringSnapshot {
	return modelOfferingsWithMetrics(items, namespace, region, defaultGPU, podsByDeployment, nil)
}

func modelOfferingsWithMetrics(items []unstructured.Unstructured, namespace, region string, defaultGPU gpuSnapshot, podsByDeployment map[string][]unstructured.Unstructured, runtimeMetrics map[string]runtimeMetricSnapshot) []offeringSnapshot {
	offerings := make([]offeringSnapshot, 0, len(items))
	for _, item := range items {
		if offering, ok := modelDeploymentOfferingWithMetrics(item, namespace, region, defaultGPU, podsByDeployment[item.GetName()], runtimeMetrics[item.GetName()]); ok {
			offerings = append(offerings, offering)
		}
	}
	sort.Slice(offerings, func(i, j int) bool {
		return offerings[i].Name < offerings[j].Name
	})
	return offerings
}

func runtimeConfigsByName(items []unstructured.Unstructured) map[string]runtimeConfig {
	configs := make(map[string]runtimeConfig, len(items))
	for _, item := range items {
		spec, _, _ := unstructured.NestedMap(item.Object, "spec")
		name := strings.TrimSpace(item.GetName())
		if name == "" {
			continue
		}
		configs[name] = runtimeConfig{
			Engine:      firstNestedString(spec, "engine"),
			Port:        maxInt(1, intFromAny(firstNonEmptyAny(firstNestedValue(spec, "port"), defaultRuntimeHTTPPort))),
			MetricsPath: normalizeEndpointPath(firstNonEmptyString(firstNestedString(spec, "metricsPath"), defaultRuntimeMetricsPath)),
			Env:         stringMapFromAny(firstNestedValue(spec, "env")),
		}
	}
	return configs
}

func (d *kubernetesDiscovery) runtimeMetricSnapshots(ctx context.Context, deployments []unstructured.Unstructured, configs map[string]runtimeConfig) map[string]runtimeMetricSnapshot {
	snapshots := map[string]runtimeMetricSnapshot{}
	for _, item := range deployments {
		name := strings.TrimSpace(item.GetName())
		if name == "" {
			continue
		}
		spec, _, _ := unstructured.NestedMap(item.Object, "spec")
		status, _, _ := unstructured.NestedMap(item.Object, "status")
		serviceName := firstNonEmptyString(firstNestedString(status, "serviceName"), runtimecontract.ServiceName(name))
		cfg := runtimeConfigForDeployment(spec, status, configs)
		metrics := runtimeMetricSnapshot{}
		if deploymentRuntimeProbeEligible(item) {
			declared := declaredRuntimeLimitsForDeployment(item, cfg)
			metrics = d.scrapeRuntimeMetrics(ctx, item.GetNamespace(), serviceName, cfg)
			metrics = applyDeclaredRuntimeLimits(metrics, declared)
			if metrics.TokensPerSecondEstimate <= 0 {
				if benchmark := d.benchmarkRuntime(ctx, item, serviceName, cfg); benchmark.Source != "" {
					metrics = mergeRuntimeMetricSnapshots(benchmark, metrics)
				}
			} else if benchmark := d.cachedRuntimeBenchmark(item, cfg); benchmark.Source != "" {
				metrics = mergeRuntimeMetricSnapshots(benchmark, metrics)
			}
		}
		if !runtimeMetricSnapshotEmpty(metrics) {
			snapshots[name] = metrics
		}
	}
	return snapshots
}

func applyDeclaredRuntimeLimits(metrics runtimeMetricSnapshot, declared declaredRuntimeLimits) runtimeMetricSnapshot {
	if declared.ContextTokens > 0 {
		metrics.ContextWindowTokens = declared.ContextTokens
		metrics.ContextWindowSource = declared.ContextSource
	}
	if declared.OutputTokens > 0 {
		metrics.MaxOutputTokens = declared.OutputTokens
		metrics.MaxOutputTokensSource = declared.OutputSource
	}
	return metrics
}

func runtimeMetricSnapshotEmpty(metrics runtimeMetricSnapshot) bool {
	return metrics.Source == "" &&
		metrics.ActiveRequests == 0 &&
		metrics.QueueDepth == 0 &&
		metrics.InputTokensPerSecondEstimate == 0 &&
		metrics.OutputTokensPerSecondEstimate == 0 &&
		metrics.TokensPerSecondEstimate == 0 &&
		metrics.GPUMemoryPressurePercent == 0 &&
		metrics.KVCacheUsagePercent == 0 &&
		metrics.PrefixCacheHitRatePercent == 0 &&
		metrics.CachedPromptTokensPerSecond == 0 &&
		!metrics.PromptCacheSupported &&
		!metrics.CachedTokenReportingSupported &&
		metrics.ContextWindowTokens == 0 &&
		metrics.MaxOutputTokens == 0 &&
		metrics.MaxConcurrentRequests == 0
}

func runtimeConfigForDeployment(spec, status map[string]any, configs map[string]runtimeConfig) runtimeConfig {
	runtimeMap, _, _ := unstructured.NestedMap(spec, "runtime")
	ref := firstNonEmptyString(firstNestedString(runtimeMap, "ref"), firstNestedString(status, "runtime"), firstNestedString(spec, "runtime"))
	cfg := configs[ref]
	if cfg.Port <= 0 {
		cfg.Port = defaultRuntimeHTTPPort
	}
	if cfg.MetricsPath == "" {
		cfg.MetricsPath = defaultRuntimeMetricsPath
	}
	if cfg.Engine == "" {
		cfg.Engine = firstNonEmptyString(firstNestedString(status, "runtime"), ref)
	}
	return cfg
}

func deploymentRuntimeProbeEligible(item unstructured.Unstructured) bool {
	spec, _, _ := unstructured.NestedMap(item.Object, "spec")
	status, _, _ := unstructured.NestedMap(item.Object, "status")
	routingMap, _, _ := unstructured.NestedMap(spec, "routing")
	endpointMap, _, _ := unstructured.NestedMap(status, "endpoint")
	phase := strings.ToLower(firstNestedString(status, "phase"))
	if phase != "active" {
		return false
	}
	endpointPath := firstNonEmptyString(firstNestedString(endpointMap, "basePath"), firstNestedString(status, "endpointPath"), firstNestedString(status, "endpoint"), firstNestedString(spec, "endpointPath"), "/v1")
	openAICompatible := firstNestedValue(routingMap, "openAICompatible")
	return (openAICompatible == nil || boolFromAny(openAICompatible)) && isDefaultOpenAIEndpointPath(endpointPath)
}

func (d *kubernetesDiscovery) scrapeRuntimeMetrics(ctx context.Context, namespace, serviceName string, cfg runtimeConfig) runtimeMetricSnapshot {
	namespace = firstNonEmptyString(namespace, d.namespace)
	if namespace == "" || serviceName == "" {
		return runtimeMetricSnapshot{}
	}
	metricsPath := cfg.MetricsPath
	if metricsPath == "" {
		metricsPath = defaultRuntimeMetricsPath
	}
	u := url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s.%s.svc:%d", serviceName, namespace, cfg.Port),
		Path:   metricsPath,
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return runtimeMetricSnapshot{}
	}
	client := d.metricsClient
	if client == nil {
		client = &http.Client{Timeout: runtimeMetricsTimeout}
	}
	response, err := client.Do(request)
	if err != nil {
		return runtimeMetricSnapshot{}
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return runtimeMetricSnapshot{}
	}
	parser := expfmt.TextParser{}
	families, err := parser.TextToMetricFamilies(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return runtimeMetricSnapshot{}
	}
	return d.runtimeMetricsFromFamilies(namespace+"/"+serviceName, time.Now().UTC(), families)
}

func mergeRuntimeMetricSnapshots(primary, secondary runtimeMetricSnapshot) runtimeMetricSnapshot {
	out := primary
	if secondary.TokensPerSecondEstimate > 0 {
		out.InputTokensPerSecondEstimate = secondary.InputTokensPerSecondEstimate
		out.OutputTokensPerSecondEstimate = secondary.OutputTokensPerSecondEstimate
		out.TokensPerSecondEstimate = secondary.TokensPerSecondEstimate
		out.Source = secondary.Source
	}
	if secondary.ActiveRequests > 0 {
		out.ActiveRequests = secondary.ActiveRequests
	}
	if secondary.QueueDepth > 0 {
		out.QueueDepth = secondary.QueueDepth
	}
	if secondary.GPUMemoryPressurePercent > 0 {
		out.GPUMemoryPressurePercent = secondary.GPUMemoryPressurePercent
	}
	if secondary.KVCacheUsagePercent > 0 {
		out.KVCacheUsagePercent = secondary.KVCacheUsagePercent
	}
	if secondary.PrefixCacheHitRatePercent > 0 {
		out.PrefixCacheHitRatePercent = secondary.PrefixCacheHitRatePercent
	}
	if secondary.CachedPromptTokensPerSecond > 0 {
		out.CachedPromptTokensPerSecond = secondary.CachedPromptTokensPerSecond
	}
	if secondary.PromptCacheSupported {
		out.PromptCacheSupported = true
	}
	if secondary.CachedTokenReportingSupported {
		out.CachedTokenReportingSupported = true
	}
	if secondary.P50FirstTokenLatencyMillis > 0 {
		out.P50FirstTokenLatencyMillis = secondary.P50FirstTokenLatencyMillis
	}
	if secondary.P95FirstTokenLatencyMillis > 0 {
		out.P95FirstTokenLatencyMillis = secondary.P95FirstTokenLatencyMillis
	}
	if secondary.P99FirstTokenLatencyMillis > 0 {
		out.P99FirstTokenLatencyMillis = secondary.P99FirstTokenLatencyMillis
	}
	if secondary.P50TotalLatencyMillis > 0 {
		out.P50TotalLatencyMillis = secondary.P50TotalLatencyMillis
	}
	if secondary.P95TotalLatencyMillis > 0 {
		out.P95TotalLatencyMillis = secondary.P95TotalLatencyMillis
	}
	if secondary.P99TotalLatencyMillis > 0 {
		out.P99TotalLatencyMillis = secondary.P99TotalLatencyMillis
	}
	if secondary.ContextWindowTokens > 0 {
		out.ContextWindowTokens = secondary.ContextWindowTokens
		out.ContextWindowSource = secondary.ContextWindowSource
	}
	if secondary.MaxOutputTokens > 0 {
		out.MaxOutputTokens = secondary.MaxOutputTokens
		out.MaxOutputTokensSource = secondary.MaxOutputTokensSource
	}
	if secondary.MaxConcurrentRequests > 0 {
		out.MaxConcurrentRequests = secondary.MaxConcurrentRequests
		out.MaxConcurrentRequestsSource = secondary.MaxConcurrentRequestsSource
	}
	return out
}

func (d *kubernetesDiscovery) cachedRuntimeBenchmark(item unstructured.Unstructured, cfg runtimeConfig) runtimeMetricSnapshot {
	if d == nil || len(d.benchmarkSamples) == 0 {
		return runtimeMetricSnapshot{}
	}
	return d.benchmarkSamples[deploymentBenchmarkKey(item, cfg)]
}

func (d *kubernetesDiscovery) benchmarkRuntime(ctx context.Context, item unstructured.Unstructured, serviceName string, cfg runtimeConfig) runtimeMetricSnapshot {
	if d == nil || !d.benchmarkEnabled {
		return runtimeMetricSnapshot{}
	}
	key := deploymentBenchmarkKey(item, cfg)
	if key == "" {
		return runtimeMetricSnapshot{}
	}
	if cached, ok := d.benchmarkSamples[key]; ok {
		return cached
	}
	if lastAttempt, ok := d.benchmarkAttempts[key]; ok && time.Since(lastAttempt) < benchmarkRetryInterval {
		return runtimeMetricSnapshot{}
	}
	if d.benchmarkAttempts == nil {
		d.benchmarkAttempts = map[string]time.Time{}
	}
	d.benchmarkAttempts[key] = time.Now()
	spec, _, _ := unstructured.NestedMap(item.Object, "spec")
	status, _, _ := unstructured.NestedMap(item.Object, "status")
	modelMap, _, _ := unstructured.NestedMap(spec, "model")
	statusModelMap, _, _ := unstructured.NestedMap(status, "model")
	model := firstNonEmptyString(
		firstNestedString(status, "modelName"),
		firstNestedString(statusModelMap, "repo"),
		firstNestedString(statusModelMap, "name"),
		firstNestedString(status, "model"),
		firstNestedString(modelMap, "repo"),
		firstNestedString(modelMap, "name"),
		item.GetName(),
	)
	namespace := firstNonEmptyString(item.GetNamespace(), d.namespace)
	if namespace == "" || serviceName == "" || model == "" {
		return runtimeMetricSnapshot{}
	}
	declared := declaredRuntimeLimitsForDeployment(item, cfg)
	limits := d.benchmarkProbeLimits(item, cfg)
	totalTimeout := d.benchmarkTotalTimeout
	if totalTimeout <= 0 {
		totalTimeout = 2 * time.Minute
	}
	benchmarkCtx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()
	inputUsage, inputLatency, inputOK := d.runBenchmarkRequest(benchmarkCtx, namespace, serviceName, cfg, model, benchmarkPrompt(128), 1)
	if !inputOK {
		log.Printf("cloud benchmark failed: namespace=%q deployment=%q service=%q stage=%q", namespace, item.GetName(), serviceName, "input-throughput")
		return runtimeMetricSnapshot{}
	}
	outputUsage, outputLatency, outputOK := d.runBenchmarkRequest(benchmarkCtx, namespace, serviceName, cfg, model, "Reply with exactly one short sentence.", limits.ThroughputMaxTokens)
	sample := runtimeMetricSnapshot{Source: "runtime-microbenchmark"}
	latencies := []time.Duration{}
	if inputLatency > 0 && inputUsage.PromptTokens > 0 {
		sample.InputTokens = inputUsage.PromptTokens
		sample.InputTokensPerSecondEstimate = roundFloat(float64(inputUsage.PromptTokens)/inputLatency.Seconds(), 2)
		latencies = append(latencies, inputLatency)
	}
	if outputOK && outputLatency > 0 && outputUsage.CompletionTokens > 0 {
		sample.OutputTokens = outputUsage.CompletionTokens
		sample.OutputTokensPerSecondEstimate = roundFloat(float64(outputUsage.CompletionTokens)/outputLatency.Seconds(), 2)
		latencies = append(latencies, outputLatency)
	}
	if firstTokenLatency, ok := d.runStreamingLatencyProbe(benchmarkCtx, namespace, serviceName, cfg, model); ok {
		sample.P50FirstTokenLatencyMillis = int(firstTokenLatency.Milliseconds())
		sample.P95FirstTokenLatencyMillis = int(firstTokenLatency.Milliseconds())
		sample.P99FirstTokenLatencyMillis = int(firstTokenLatency.Milliseconds())
	}
	if sample.P95FirstTokenLatencyMillis == 0 && inputLatency > 0 {
		sample.P50FirstTokenLatencyMillis = int(inputLatency.Milliseconds())
		sample.P95FirstTokenLatencyMillis = int(inputLatency.Milliseconds())
		sample.P99FirstTokenLatencyMillis = int(inputLatency.Milliseconds())
	}
	if declared.ContextTokens > 0 {
		sample.ContextWindowTokens = declared.ContextTokens
		sample.ContextWindowSource = declared.ContextSource
	} else if contextTokens := d.probeContextWindowTokens(benchmarkCtx, namespace, serviceName, cfg, model, limits.ContextTokens); contextTokens > 0 {
		sample.ContextWindowTokens = contextTokens
		sample.ContextWindowSource = "benchmark"
	}
	if declared.OutputTokens > 0 {
		sample.MaxOutputTokens = declared.OutputTokens
		sample.MaxOutputTokensSource = declared.OutputSource
	} else if maxOutputTokens := d.probeMaxOutputTokens(benchmarkCtx, namespace, serviceName, cfg, model, limits.OutputTokens); maxOutputTokens > 0 {
		sample.MaxOutputTokens = maxOutputTokens
		sample.MaxOutputTokensSource = "benchmark"
	}
	if concurrent, concurrentLatencies := d.probeConcurrentRequests(benchmarkCtx, namespace, serviceName, cfg, model, limits.Concurrency, limits.ThroughputMaxTokens); concurrent > 0 {
		sample.MaxConcurrentRequests = concurrent
		sample.MaxConcurrentRequestsSource = "benchmark"
		latencies = append(latencies, concurrentLatencies...)
	}
	if len(latencies) > 0 {
		sample.P50TotalLatencyMillis = int(percentileDuration(latencies, 50).Milliseconds())
		sample.P95TotalLatencyMillis = int(percentileDuration(latencies, 95).Milliseconds())
		sample.P99TotalLatencyMillis = int(percentileDuration(latencies, 99).Milliseconds())
	}
	sample.TokensPerSecondEstimate = sample.InputTokensPerSecondEstimate + sample.OutputTokensPerSecondEstimate
	if sample.TokensPerSecondEstimate <= 0 {
		log.Printf("cloud benchmark produced no token throughput: namespace=%q deployment=%q service=%q", namespace, item.GetName(), serviceName)
		return runtimeMetricSnapshot{}
	}
	if d.benchmarkSamples == nil {
		d.benchmarkSamples = map[string]runtimeMetricSnapshot{}
	}
	d.benchmarkSamples[key] = sample
	log.Printf("cloud benchmark completed: namespace=%q deployment=%q service=%q source=%q input_tps=%.2f output_tps=%.2f total_tps=%.2f context_window_tokens=%d max_output_tokens=%d max_concurrent_requests=%d",
		namespace, item.GetName(), serviceName, sample.Source, sample.InputTokensPerSecondEstimate, sample.OutputTokensPerSecondEstimate, sample.TokensPerSecondEstimate, sample.ContextWindowTokens, sample.MaxOutputTokens, sample.MaxConcurrentRequests)
	return sample
}

func (d *kubernetesDiscovery) runBenchmarkRequest(ctx context.Context, namespace, serviceName string, cfg runtimeConfig, model, prompt string, maxTokens int) (benchmarkUsage, time.Duration, bool) {
	if maxTokens <= 0 {
		maxTokens = 1
	}
	basePath := "/v1"
	u := url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s.%s.svc:%d", serviceName, namespace, cfg.Port),
		Path:   path.Join(basePath, "chat", "completions"),
	}
	body := map[string]any{
		"model":       model,
		"messages":    []map[string]string{{"role": "user", "content": prompt}},
		"max_tokens":  maxTokens,
		"temperature": 0,
		"stream":      false,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return benchmarkUsage{}, 0, false
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		return benchmarkUsage{}, 0, false
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "inferops-cloud-agent-benchmark")
	client := d.benchmarkHTTPClient()
	start := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return benchmarkUsage{}, 0, false
	}
	defer response.Body.Close()
	elapsed := time.Since(start)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return benchmarkUsage{}, 0, false
	}
	var decoded benchmarkChatResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&decoded); err != nil {
		return benchmarkUsage{}, 0, false
	}
	if decoded.Usage.PromptTokens <= 0 && decoded.Usage.CompletionTokens <= 0 && decoded.Usage.TotalTokens <= 0 {
		return benchmarkUsage{}, 0, false
	}
	return decoded.Usage, elapsed, true
}

func (d *kubernetesDiscovery) runStreamingLatencyProbe(ctx context.Context, namespace, serviceName string, cfg runtimeConfig, model string) (time.Duration, bool) {
	u := url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s.%s.svc:%d", serviceName, namespace, cfg.Port),
		Path:   path.Join("/v1", "chat", "completions"),
	}
	body := map[string]any{
		"model":       model,
		"messages":    []map[string]string{{"role": "user", "content": "Reply with exactly one word."}},
		"max_tokens":  8,
		"temperature": 0,
		"stream":      true,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return 0, false
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		return 0, false
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "inferops-cloud-agent-benchmark")
	client := d.benchmarkHTTPClient()
	start := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return 0, false
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return 0, false
	}
	scanner := bufio.NewScanner(io.LimitReader(response.Body, 1<<20))
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data != "" && data != "[DONE]" {
				return time.Since(start), true
			}
		}
	}
	return 0, false
}

func (d *kubernetesDiscovery) benchmarkProbeLimits(item unstructured.Unstructured, cfg runtimeConfig) benchmarkProbeLimits {
	declared := declaredRuntimeLimitsForDeployment(item, cfg)
	hardContextCap := d.benchmarkMaxContextTokens
	hardOutputCap := d.benchmarkMaxOutputTokens
	hardConcurrencyCap := d.benchmarkMaxConcurrency
	if hardContextCap < 0 {
		hardContextCap = 0
	}
	if hardOutputCap < 0 {
		hardOutputCap = 0
	}
	if hardConcurrencyCap < 0 {
		hardConcurrencyCap = 0
	}

	contextTokens := declared.ContextTokens
	contextSource := declared.ContextSource
	if contextTokens <= 0 {
		contextTokens = hardContextCap
		contextSource = "probe-ceiling"
	} else if hardContextCap > 0 && contextTokens > hardContextCap {
		contextTokens = hardContextCap
		contextSource = "runtime-config-capped"
	}

	outputTokens := declared.OutputTokens
	outputSource := declared.OutputSource
	if outputTokens <= 0 {
		outputTokens = hardOutputCap
		outputSource = "probe-ceiling"
	}
	if hardOutputCap > 0 && outputTokens > hardOutputCap {
		outputTokens = hardOutputCap
		outputSource = "runtime-config-capped"
	}
	if contextTokens > 0 && outputTokens > contextTokens {
		outputTokens = contextTokens
	}

	concurrency := declared.Concurrency
	concurrencySource := declared.ConcurrencySource
	if concurrency <= 0 {
		concurrency = hardConcurrencyCap
		concurrencySource = "probe-ceiling"
	}
	if hardConcurrencyCap > 0 && concurrency > hardConcurrencyCap {
		concurrency = hardConcurrencyCap
		concurrencySource = "runtime-config-capped"
	}

	throughputTokens := d.benchmarkMaxTokens
	if throughputTokens <= 0 {
		throughputTokens = 1
	}
	if outputTokens > 0 && throughputTokens > outputTokens {
		throughputTokens = outputTokens
	}

	return benchmarkProbeLimits{
		ContextTokens:       contextTokens,
		OutputTokens:        outputTokens,
		Concurrency:         concurrency,
		LatencySLOMillis:    d.benchmarkLatencySLOMillis,
		ThroughputMaxTokens: throughputTokens,
		ContextSource:       contextSource,
		OutputSource:        outputSource,
		ConcurrencySource:   concurrencySource,
	}
}

func declaredRuntimeLimitsForDeployment(item unstructured.Unstructured, cfg runtimeConfig) declaredRuntimeLimits {
	spec, _, _ := unstructured.NestedMap(item.Object, "spec")
	runtimeMap, _, _ := unstructured.NestedMap(spec, "runtime")
	declared := declaredRuntimeLimits{}
	if value := intFromAny(firstPositiveAny(
		firstNestedValue(runtimeMap, "maxModelLen"),
		runtimeEnvInt(cfg.Env, "MAX_MODEL_LEN", "CONTEXT_LENGTH", "N_CTX"),
	)); value > 0 {
		declared.ContextTokens = value
		declared.ContextSource = "runtime-config"
	}
	if value := runtimeEnvInt(cfg.Env, "MAX_OUTPUT_TOKENS", "MAX_NEW_TOKENS", "MAX_TOKENS", "N_PREDICT"); value > 0 {
		declared.OutputTokens = value
		declared.OutputSource = "runtime-config"
	}
	if declared.ContextTokens > 0 && declared.OutputTokens > declared.ContextTokens {
		declared.OutputTokens = declared.ContextTokens
		declared.OutputSource = "runtime-config-capped"
	}
	if value := intFromAny(firstPositiveAny(
		firstNestedValue(runtimeMap, "maxConcurrentRequests"),
		firstNestedValue(runtimeMap, "maxNumSeqs"),
		firstNestedValue(runtimeMap, "maxNumSequences"),
		runtimeEnvInt(cfg.Env, "MAX_NUM_SEQS", "MAX_CONCURRENT_REQUESTS"),
	)); value > 0 {
		declared.Concurrency = value
		declared.ConcurrencySource = "runtime-config"
	}
	return declared
}

func (d *kubernetesDiscovery) probeContextWindowTokens(ctx context.Context, namespace, serviceName string, cfg runtimeConfig, model string, maxTokens int) int {
	if maxTokens <= 0 {
		return 0
	}
	lastAccepted := 0
	for target := minPositiveInt(1024, maxTokens); target <= maxTokens; {
		usage, _, ok := d.runBenchmarkRequest(ctx, namespace, serviceName, cfg, model, benchmarkPrompt(target), 1)
		if !ok {
			break
		}
		if usage.PromptTokens > lastAccepted {
			lastAccepted = usage.PromptTokens
		} else if target > lastAccepted {
			lastAccepted = target
		}
		if target == maxTokens {
			break
		}
		target *= 2
		if target > maxTokens {
			target = maxTokens
		}
	}
	return lastAccepted
}

func (d *kubernetesDiscovery) probeMaxOutputTokens(ctx context.Context, namespace, serviceName string, cfg runtimeConfig, model string, maxTokens int) int {
	if maxTokens <= 0 {
		return 0
	}
	lastAccepted := 0
	for target := minPositiveInt(64, maxTokens); target <= maxTokens; {
		_, _, ok := d.runBenchmarkRequest(ctx, namespace, serviceName, cfg, model, "Write a deterministic numbered list.", target)
		if !ok {
			break
		}
		lastAccepted = target
		if target == maxTokens {
			break
		}
		target *= 2
		if target > maxTokens {
			target = maxTokens
		}
	}
	return lastAccepted
}

func (d *kubernetesDiscovery) probeConcurrentRequests(ctx context.Context, namespace, serviceName string, cfg runtimeConfig, model string, maxConcurrency, maxTokens int) (int, []time.Duration) {
	if maxConcurrency <= 0 {
		return 0, nil
	}
	latencySLO := time.Duration(d.benchmarkLatencySLOMillis) * time.Millisecond
	if latencySLO <= 0 {
		latencySLO = 10 * time.Second
	}
	lastStable := 0
	stableLatencies := []time.Duration{}
	for level := 1; level <= maxConcurrency; level *= 2 {
		latencies, ok := d.runConcurrentBenchmarkLevel(ctx, namespace, serviceName, cfg, model, level, maxTokens)
		if !ok || len(latencies) != level || percentileDuration(latencies, 95) > latencySLO {
			break
		}
		lastStable = level
		stableLatencies = latencies
	}
	return lastStable, stableLatencies
}

func (d *kubernetesDiscovery) runConcurrentBenchmarkLevel(ctx context.Context, namespace, serviceName string, cfg runtimeConfig, model string, concurrency, maxTokens int) ([]time.Duration, bool) {
	if concurrency <= 0 {
		return nil, false
	}
	latencies := make([]time.Duration, concurrency)
	ok := make([]bool, concurrency)
	var wg sync.WaitGroup
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		i := i
		go func() {
			defer wg.Done()
			_, latency, success := d.runBenchmarkRequest(ctx, namespace, serviceName, cfg, model, "Reply with exactly one short sentence.", maxTokens)
			latencies[i] = latency
			ok[i] = success
		}()
	}
	wg.Wait()
	for _, success := range ok {
		if !success {
			return latencies, false
		}
	}
	return latencies, true
}

func (d *kubernetesDiscovery) benchmarkHTTPClient() *http.Client {
	timeout := d.benchmarkTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	if d.metricsClient != nil && d.metricsClient.Transport != nil {
		client = d.metricsClient
	}
	return client
}

func percentileDuration(values []time.Duration, percentile int) time.Duration {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]time.Duration(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	if percentile <= 0 {
		return ordered[0]
	}
	if percentile >= 100 {
		return ordered[len(ordered)-1]
	}
	index := int(math.Ceil(float64(percentile)/100*float64(len(ordered)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(ordered) {
		index = len(ordered) - 1
	}
	return ordered[index]
}

func deploymentBenchmarkKey(item unstructured.Unstructured, cfg runtimeConfig) string {
	spec, _, _ := unstructured.NestedMap(item.Object, "spec")
	status, _, _ := unstructured.NestedMap(item.Object, "status")
	modelMap, _, _ := unstructured.NestedMap(spec, "model")
	runtimeMap, _, _ := unstructured.NestedMap(spec, "runtime")
	model := firstNonEmptyString(firstNestedString(status, "modelName"), firstNestedString(modelMap, "repo"), firstNestedString(modelMap, "name"), item.GetName())
	runtime := firstNonEmptyString(firstNestedString(status, "runtime"), firstNestedString(runtimeMap, "ref"), cfg.Engine)
	if model == "" || runtime == "" {
		return ""
	}
	return strings.Join([]string{
		firstNonEmptyString(item.GetNamespace(), "default"),
		item.GetName(),
		model,
		runtime,
		fmt.Sprint(firstNestedValue(runtimeMap, "maxModelLen")),
		fmt.Sprint(firstNestedValue(runtimeMap, "dtype")),
		fmt.Sprint(item.GetGeneration()),
	}, "/")
}

func benchmarkPrompt(words int) string {
	if words <= 0 {
		words = 32
	}
	const sentence = "InferOps benchmark prompt measures deterministic local inference capacity for provider offering calibration."
	parts := make([]string, 0, words)
	for len(parts) < words {
		parts = append(parts, strings.Fields(sentence)...)
	}
	return strings.Join(parts[:words], " ")
}

func (d *kubernetesDiscovery) runtimeMetricsFromFamilies(key string, observedAt time.Time, families map[string]*dto.MetricFamily) runtimeMetricSnapshot {
	metrics := runtimeMetricSnapshot{}
	metrics.ActiveRequests = int(math.Round(firstMetricValue(families,
		"vllm:num_requests_running",
		"vllm_num_requests_running",
		"llamacpp:requests_processing",
		"llamacpp_requests_processing",
	)))
	metrics.QueueDepth = int(math.Round(firstMetricValue(families,
		"vllm:num_requests_waiting",
		"vllm_num_requests_waiting",
		"llamacpp:requests_deferred",
		"llamacpp_requests_deferred",
	)))
	kvCache := firstMetricValue(families, "vllm:kv_cache_usage_perc", "vllm_kv_cache_usage_perc")
	if kvCache > 0 {
		if kvCache <= 1 {
			kvCache *= 100
		}
		metrics.KVCacheUsagePercent = boundedFloat(kvCache, 0, 100)
		metrics.GPUMemoryPressurePercent = metrics.KVCacheUsagePercent
	}
	prefixHits := firstMetricValue(families,
		"vllm:prefix_cache_hits",
		"vllm_prefix_cache_hits",
		"vllm:gpu_prefix_cache_hits",
		"vllm_gpu_prefix_cache_hits",
		"vllm:external_prefix_cache_hits",
		"vllm_external_prefix_cache_hits",
	)
	prefixQueries := firstMetricValue(families,
		"vllm:prefix_cache_queries",
		"vllm_prefix_cache_queries",
		"vllm:gpu_prefix_cache_queries",
		"vllm_gpu_prefix_cache_queries",
		"vllm:external_prefix_cache_queries",
		"vllm_external_prefix_cache_queries",
	)
	cachedPromptTokens := firstMetricValue(families,
		"vllm:prompt_tokens_cached",
		"vllm_prompt_tokens_cached",
		"vllm:prompt_tokens_cached_total",
		"vllm_prompt_tokens_cached_total",
	)
	metrics.PromptCacheSupported = hasAnyMetricFamily(families,
		"vllm:prefix_cache_hits",
		"vllm_prefix_cache_hits",
		"vllm:prefix_cache_queries",
		"vllm_prefix_cache_queries",
		"vllm:prompt_tokens_cached",
		"vllm_prompt_tokens_cached",
		"vllm:prompt_tokens_cached_total",
		"vllm_prompt_tokens_cached_total",
	)
	if prefixQueries > 0 {
		metrics.PrefixCacheHitRatePercent = boundedFloat(prefixHits/prefixQueries*100, 0, 100)
	}
	inputGauge := firstMetricValue(families, "llamacpp:prompt_tokens_seconds", "llamacpp_prompt_tokens_seconds")
	outputGauge := firstMetricValue(families, "llamacpp:predicted_tokens_seconds", "llamacpp_predicted_tokens_seconds")
	if inputGauge > 0 || outputGauge > 0 {
		metrics.InputTokensPerSecondEstimate = positiveFloat(inputGauge)
		metrics.OutputTokensPerSecondEstimate = positiveFloat(outputGauge)
		metrics.TokensPerSecondEstimate = metrics.InputTokensPerSecondEstimate + metrics.OutputTokensPerSecondEstimate
		metrics.Source = "runtime-metrics-gauge"
		return metrics
	}
	inputTotal := firstMetricValue(families,
		"vllm:prompt_tokens_total",
		"vllm_prompt_tokens_total",
		"llamacpp:prompt_tokens_total",
		"llamacpp_prompt_tokens_total",
	)
	outputTotal := firstMetricValue(families,
		"vllm:generation_tokens_total",
		"vllm_generation_tokens_total",
		"llamacpp:tokens_predicted_total",
		"llamacpp_tokens_predicted_total",
	)
	if inputTotal <= 0 && outputTotal <= 0 {
		if metrics.ActiveRequests > 0 || metrics.QueueDepth > 0 || metrics.GPUMemoryPressurePercent > 0 {
			metrics.Source = "runtime-metrics-live"
		}
		return metrics
	}
	if d.tokenSamples == nil {
		d.tokenSamples = map[string]tokenCounterSample{}
	}
	previous, found := d.tokenSamples[key]
	d.tokenSamples[key] = tokenCounterSample{
		InputTokens:        inputTotal,
		OutputTokens:       outputTotal,
		PrefixCacheHits:    prefixHits,
		PrefixCacheQueries: prefixQueries,
		CachedPromptTokens: cachedPromptTokens,
		ObservedAt:         observedAt,
	}
	elapsed := observedAt.Sub(previous.ObservedAt).Seconds()
	if !found || elapsed <= 0 {
		if metrics.ActiveRequests > 0 || metrics.QueueDepth > 0 || metrics.GPUMemoryPressurePercent > 0 {
			metrics.Source = "runtime-metrics-live"
		}
		return metrics
	}
	inputDelta := inputTotal - previous.InputTokens
	outputDelta := outputTotal - previous.OutputTokens
	if inputDelta < 0 || outputDelta < 0 {
		return metrics
	}
	metrics.InputTokensPerSecondEstimate = inputDelta / elapsed
	metrics.OutputTokensPerSecondEstimate = outputDelta / elapsed
	metrics.TokensPerSecondEstimate = metrics.InputTokensPerSecondEstimate + metrics.OutputTokensPerSecondEstimate
	queryDelta := prefixQueries - previous.PrefixCacheQueries
	hitDelta := prefixHits - previous.PrefixCacheHits
	if queryDelta > 0 && hitDelta >= 0 {
		metrics.PrefixCacheHitRatePercent = boundedFloat(hitDelta/queryDelta*100, 0, 100)
	}
	cachedPromptDelta := cachedPromptTokens - previous.CachedPromptTokens
	if cachedPromptDelta > 0 {
		metrics.CachedPromptTokensPerSecond = cachedPromptDelta / elapsed
	}
	if metrics.TokensPerSecondEstimate > 0 {
		metrics.Source = "runtime-metrics-counter-rate"
	}
	return metrics
}

func podsByModelDeployment(items, replicaSets, deployments []unstructured.Unstructured) map[string][]unstructured.Unstructured {
	grouped := map[string][]unstructured.Unstructured{}
	replicaSetByName := unstructuredByName(replicaSets)
	deploymentByName := unstructuredByName(deployments)
	for _, item := range items {
		owner, ok := trustedModelDeploymentOwner(item, replicaSetByName, deploymentByName)
		if !ok {
			continue
		}
		grouped[owner] = append(grouped[owner], item)
	}
	for owner := range grouped {
		pods := grouped[owner]
		sort.Slice(pods, func(i, j int) bool {
			return pods[i].GetName() < pods[j].GetName()
		})
		grouped[owner] = pods
	}
	return grouped
}

func unstructuredByName(items []unstructured.Unstructured) map[string]unstructured.Unstructured {
	out := make(map[string]unstructured.Unstructured, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item.GetName())
		if name != "" {
			out[name] = item
		}
	}
	return out
}

func trustedModelDeploymentOwner(pod unstructured.Unstructured, replicaSetByName, deploymentByName map[string]unstructured.Unstructured) (string, bool) {
	labels := pod.GetLabels()
	if !hasRequiredInferOpsRuntimeLabels(labels) {
		return "", false
	}
	labeledOwner := strings.TrimSpace(labels[runtimecontract.ModelDeploymentLabel])
	if labeledOwner == "" {
		return "", false
	}
	owner := modelDeploymentOwnerName(pod.GetOwnerReferences(), replicaSetByName, deploymentByName, 0, map[string]struct{}{})
	if owner == "" || owner != labeledOwner {
		return "", false
	}
	return owner, true
}

func hasRequiredInferOpsRuntimeLabels(labels map[string]string) bool {
	for key, want := range requiredInferOpsRuntimePodLabels {
		if labels[key] != want {
			return false
		}
	}
	return true
}

func modelDeploymentOwnerName(ownerRefs []metav1.OwnerReference, replicaSetByName, deploymentByName map[string]unstructured.Unstructured, depth int, visited map[string]struct{}) string {
	if depth > maxOwnerReferenceDepth {
		return ""
	}
	for _, owner := range ownerRefs {
		if isInferOpsModelDeploymentOwner(owner) {
			return strings.TrimSpace(owner.Name)
		}
		ownerKey := owner.APIVersion + "/" + owner.Kind + "/" + owner.Name
		if _, seen := visited[ownerKey]; seen {
			continue
		}
		visited[ownerKey] = struct{}{}
		switch owner.Kind {
		case "ReplicaSet":
			replicaSet, ok := replicaSetByName[owner.Name]
			if !ok {
				continue
			}
			if name := modelDeploymentOwnerName(replicaSet.GetOwnerReferences(), replicaSetByName, deploymentByName, depth+1, visited); name != "" {
				return name
			}
		case "Deployment":
			deployment, ok := deploymentByName[owner.Name]
			if !ok {
				continue
			}
			if name := modelDeploymentOwnerName(deployment.GetOwnerReferences(), replicaSetByName, deploymentByName, depth+1, visited); name != "" {
				return name
			}
		}
	}
	return ""
}

func isInferOpsModelDeploymentOwner(owner metav1.OwnerReference) bool {
	if owner.Kind != runtimecontract.ModelDeploymentKind || strings.TrimSpace(owner.Name) == "" {
		return false
	}
	return strings.HasPrefix(owner.APIVersion, runtimecontract.ModelDeploymentAPIGroup+"/")
}

func isHuggingFaceSource(source string) bool {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "huggingface", "hugging-face", "hf":
		return true
	default:
		return false
	}
}

func modelDeploymentOffering(item unstructured.Unstructured, namespace, region string, defaultGPU gpuSnapshot, pods []unstructured.Unstructured) (offeringSnapshot, bool) {
	return modelDeploymentOfferingWithMetrics(item, namespace, region, defaultGPU, pods, runtimeMetricSnapshot{})
}

func modelDeploymentOfferingWithMetrics(item unstructured.Unstructured, namespace, region string, defaultGPU gpuSnapshot, pods []unstructured.Unstructured, runtimeMetrics runtimeMetricSnapshot) (offeringSnapshot, bool) {
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
	endpointPath := firstNonEmptyString(firstNestedString(endpointMap, "basePath"), firstNestedString(status, "endpointPath"), firstNestedString(status, "endpoint"), firstNestedString(spec, "endpointPath"), "/v1")
	openAICompatible := firstNestedValue(routingMap, "openAICompatible")
	customEndpointsPresent := boolFromAny(firstNestedValue(status, "customEndpointsPresent")) ||
		boolFromAny(firstNestedValue(spec, "customEndpointsPresent")) ||
		(openAICompatible != nil && !boolFromAny(openAICompatible)) ||
		!isDefaultOpenAIEndpointPath(endpointPath)

	gpu := gpuFromDeployment(status, spec, defaultGPU)
	autoCapacity := estimateAutomaticCapacity(model, runtime, gpu, runtimeMap)
	capacitySource := autoCapacity.Source
	if deploymentCapacityReported(metricsMap, capacityMap) {
		capacitySource = "deployment-status"
	}
	if runtimeMetrics.Source != "" {
		capacitySource = runtimeMetrics.Source
	}
	reportedInputTPS := firstCredibleStatusThroughputAny(
		firstNestedValue(metricsMap, "inputTokensPerSecondEstimate"),
		firstNestedValue(metricsMap, "promptTokensPerSecondEstimate"),
		firstNestedValue(capacityMap, "inputTokensPerSecondEstimate"),
		firstNestedValue(capacityMap, "promptTokensPerSecondEstimate"),
	)
	reportedOutputTPS := firstCredibleStatusThroughputAny(
		firstNestedValue(metricsMap, "outputTokensPerSecondEstimate"),
		firstNestedValue(metricsMap, "generationTokensPerSecondEstimate"),
		firstNestedValue(capacityMap, "outputTokensPerSecondEstimate"),
		firstNestedValue(capacityMap, "generationTokensPerSecondEstimate"),
	)
	reportedTotalTPS := firstCredibleStatusThroughputAny(
		firstNestedValue(metricsMap, "tokensPerSecondEstimate"),
		firstNestedValue(capacityMap, "tokensPerSecondEstimate"),
	)
	inputTPS := positiveFloatOrDefault(
		firstPositiveAny(
			runtimeMetrics.InputTokensPerSecondEstimate,
			reportedInputTPS,
		),
		autoCapacity.InputTokensPerSecond,
	)
	outputTPS := positiveFloatOrDefault(
		firstPositiveAny(
			runtimeMetrics.OutputTokensPerSecondEstimate,
			reportedOutputTPS,
		),
		autoCapacity.OutputTokensPerSecond,
	)
	totalTPS := positiveFloatOrDefault(
		firstPositiveAny(
			runtimeMetrics.TokensPerSecondEstimate,
			reportedTotalTPS,
		),
		maxFloat(autoCapacity.TokensPerSecond, inputTPS+outputTPS),
	)
	tokensPerMinute := maxInt(1, int(math.Floor(totalTPS*60)))
	if runtimeMetrics.TokensPerSecondEstimate <= 0 {
		tokensPerMinute = maxInt(1, intFromAny(firstPositiveAny(
			firstCredibleStatusTokensPerMinuteAny(firstNestedValue(metricsMap, "tokensPerMinuteEstimate")),
			firstCredibleStatusTokensPerMinuteAny(firstNestedValue(capacityMap, "tokensPerMinuteEstimate")),
			tokensPerMinute,
		)))
	}
	maxConcurrent := maxInt(1, intFromAny(firstPositiveAny(
		runtimeMetrics.MaxConcurrentRequests,
		firstCredibleStatusConcurrencyAny(firstNestedValue(metricsMap, "maxConcurrentRequests"), autoCapacity.MaxConcurrentRequests),
		firstCredibleStatusConcurrencyAny(firstNestedValue(capacityMap, "maxConcurrentRequests"), autoCapacity.MaxConcurrentRequests),
		autoCapacity.MaxConcurrentRequests,
		1,
	)))
	if maxConcurrent < runtimeMetrics.ActiveRequests {
		maxConcurrent = runtimeMetrics.ActiveRequests
	}
	maxConcurrentSource := sourceForPositiveInt(autoCapacity.MaxConcurrentRequests, autoCapacity.Source)
	if statusConcurrencyIsCredible(firstNestedValue(metricsMap, "maxConcurrentRequests"), autoCapacity.MaxConcurrentRequests) ||
		statusConcurrencyIsCredible(firstNestedValue(capacityMap, "maxConcurrentRequests"), autoCapacity.MaxConcurrentRequests) {
		maxConcurrentSource = "deployment-status"
	}
	if runtimeMetrics.MaxConcurrentRequests > 0 {
		maxConcurrentSource = firstNonEmptyString(runtimeMetrics.MaxConcurrentRequestsSource, runtimeMetrics.Source, "benchmark")
	}
	contextWindowTokens := autoCapacity.ContextWindowTokens
	contextWindowSource := sourceForPositiveInt(autoCapacity.ContextWindowTokens, "runtime-config")
	if runtimeMetrics.ContextWindowTokens > 0 {
		contextWindowTokens = runtimeMetrics.ContextWindowTokens
		contextWindowSource = firstNonEmptyString(runtimeMetrics.ContextWindowSource, "benchmark")
	}
	maxOutputTokens := intFromAny(firstPositiveAny(
		runtimeMetrics.MaxOutputTokens,
		firstNestedValue(metricsMap, "maxOutputTokens"),
		firstNestedValue(capacityMap, "maxOutputTokens"),
	))
	maxOutputTokensSource := ""
	if maxOutputTokens > 0 {
		maxOutputTokensSource = firstNonEmptyString(runtimeMetrics.MaxOutputTokensSource, "deployment-status")
	}
	rateLimitTokensPerMinute := intFromAny(firstPositiveAny(firstNestedValue(rateLimitMap, "maxTokensPerMinute"), firstNestedValue(spec, "maxTokensPerMinute"), tokensPerMinute))
	if rateLimitTokensPerMinute <= 1 && tokensPerMinute > 1 {
		rateLimitTokensPerMinute = tokensPerMinute
	}
	offering := offeringSnapshot{
		Name:                   name,
		Namespace:              namespace,
		Phase:                  phase,
		Endpoint:               endpoint,
		Model:                  model,
		Runtime:                runtime,
		EndpointPath:           endpointPath,
		CustomEndpointsPresent: customEndpointsPresent,
		GPU:                    gpu,
		Region:                 firstNonEmptyString(firstNestedString(status, "region"), firstNestedString(spec, "region"), region, "local-dev"),
		Pricing: pricingSnapshot{
			Unit:     firstNonEmptyString(firstNestedString(pricingMap, "unit"), "million_input_tokens"),
			PriceUSD: firstNonEmptyString(firstNestedString(pricingMap, "priceUSD"), firstNestedString(pricingMap, "priceUsd"), "0.01"),
		},
		ObservedCapacity: capacitySnapshot{
			ActiveRequests:                intFromAny(firstNonEmptyAny(firstNestedValue(metricsMap, "activeRequests"), firstNestedValue(capacityMap, "activeRequests"), runtimeMetrics.ActiveRequests)),
			QueueDepth:                    intFromAny(firstNonEmptyAny(firstNestedValue(metricsMap, "queueDepth"), firstNestedValue(capacityMap, "queueDepth"), runtimeMetrics.QueueDepth)),
			MaxConcurrentRequests:         maxConcurrent,
			MaxConcurrentRequestsSource:   maxConcurrentSource,
			TokensPerSecondEstimate:       totalTPS,
			InputTokensPerSecondEstimate:  inputTPS,
			OutputTokensPerSecondEstimate: outputTPS,
			TokensPerMinuteEstimate:       tokensPerMinute,
			ContextWindowTokens:           contextWindowTokens,
			ContextWindowSource:           contextWindowSource,
			MaxOutputTokens:               maxOutputTokens,
			MaxOutputTokensSource:         maxOutputTokensSource,
			CapacitySource:                capacitySource,
			P50FirstTokenLatencyMillis:    intFromAny(firstPositiveAny(runtimeMetrics.P50FirstTokenLatencyMillis, firstNestedValue(metricsMap, "p50FirstTokenLatencyMillis"), firstNestedValue(capacityMap, "p50FirstTokenLatencyMillis"))),
			P95FirstTokenLatencyMillis:    intFromAny(firstPositiveAny(runtimeMetrics.P95FirstTokenLatencyMillis, firstNestedValue(metricsMap, "p95FirstTokenLatencyMillis"), firstNestedValue(capacityMap, "p95FirstTokenLatencyMillis"))),
			P99FirstTokenLatencyMillis:    intFromAny(firstPositiveAny(runtimeMetrics.P99FirstTokenLatencyMillis, firstNestedValue(metricsMap, "p99FirstTokenLatencyMillis"), firstNestedValue(capacityMap, "p99FirstTokenLatencyMillis"))),
			P50TotalLatencyMillis:         intFromAny(firstPositiveAny(runtimeMetrics.P50TotalLatencyMillis, firstNestedValue(metricsMap, "p50TotalLatencyMillis"), firstNestedValue(capacityMap, "p50TotalLatencyMillis"))),
			P95TotalLatencyMillis:         intFromAny(firstPositiveAny(runtimeMetrics.P95TotalLatencyMillis, firstNestedValue(metricsMap, "p95TotalLatencyMillis"), firstNestedValue(capacityMap, "p95TotalLatencyMillis"))),
			P99TotalLatencyMillis:         intFromAny(firstPositiveAny(runtimeMetrics.P99TotalLatencyMillis, firstNestedValue(metricsMap, "p99TotalLatencyMillis"), firstNestedValue(capacityMap, "p99TotalLatencyMillis"))),
			ErrorRatePercent:              floatFromAny(firstNonEmptyAny(firstNestedValue(metricsMap, "errorRatePercent"), firstNestedValue(capacityMap, "errorRatePercent"))),
			GPUUtilizationPercent:         floatFromAny(firstNonEmptyAny(firstNestedValue(metricsMap, "gpuUtilizationPercent"), firstNestedValue(capacityMap, "gpuUtilizationPercent"))),
			GPUMemoryPressurePercent:      floatFromAny(firstNonEmptyAny(firstNestedValue(metricsMap, "gpuMemoryPressurePercent"), firstNestedValue(capacityMap, "gpuMemoryPressurePercent"), runtimeMetrics.GPUMemoryPressurePercent)),
			KVCacheUsagePercent:           floatFromAny(firstNonEmptyAny(runtimeMetrics.KVCacheUsagePercent, firstNestedValue(metricsMap, "kvCacheUsagePercent"), firstNestedValue(capacityMap, "kvCacheUsagePercent"))),
			PrefixCacheHitRatePercent:     floatFromAny(firstNonEmptyAny(runtimeMetrics.PrefixCacheHitRatePercent, firstNestedValue(metricsMap, "prefixCacheHitRatePercent"), firstNestedValue(capacityMap, "prefixCacheHitRatePercent"))),
			CachedPromptTokensPerSecond:   floatFromAny(firstNonEmptyAny(runtimeMetrics.CachedPromptTokensPerSecond, firstNestedValue(metricsMap, "cachedPromptTokensPerSecondEstimate"), firstNestedValue(capacityMap, "cachedPromptTokensPerSecondEstimate"))),
			PromptCacheSupported:          runtimeMetrics.PromptCacheSupported || boolFromAny(firstNonEmptyAny(firstNestedValue(metricsMap, "promptCacheSupported"), firstNestedValue(capacityMap, "promptCacheSupported"))),
			CachedTokenReportingSupported: runtimeMetrics.CachedTokenReportingSupported || boolFromAny(firstNonEmptyAny(firstNestedValue(metricsMap, "cachedTokenReportingSupported"), firstNestedValue(capacityMap, "cachedTokenReportingSupported"))),
			EstimatedQueueDelayMillis:     intFromAny(firstNonEmptyAny(firstNestedValue(metricsMap, "estimatedQueueDelayMillis"), firstNestedValue(capacityMap, "estimatedQueueDelayMillis"))),
		},
		RateLimits: rateLimitSnapshot{
			MaxConcurrentRequests: maxInt(1, intFromAny(firstNonEmptyAny(firstNestedValue(rateLimitMap, "maxConcurrentRequests"), firstNestedValue(spec, "maxConcurrentRequests"), maxConcurrent))),
			MaxQueueDepth:         intFromAny(firstNonEmptyAny(firstNestedValue(rateLimitMap, "maxQueueDepth"), firstNestedValue(spec, "maxQueueDepth"))),
			MaxRequestsPerMinute:  maxInt(1, intFromAny(firstNonEmptyAny(firstNestedValue(rateLimitMap, "maxRequestsPerMinute"), firstNestedValue(spec, "maxRequestsPerMinute"), 60))),
			MaxTokensPerMinute:    maxInt(1, rateLimitTokensPerMinute),
		},
		Evidence: modelDeploymentEvidence(item, model, runtime, endpointPath, pods),
	}
	return offering, strings.TrimSpace(offering.Model) != ""
}

func modelDeploymentEvidence(item unstructured.Unstructured, model, runtime, endpointPath string, pods []unstructured.Unstructured) evidenceSnapshot {
	manifestYAML := ""
	if payload, err := yaml.Marshal(redactEvidenceValue(stableModelDeploymentManifest(item))); err == nil {
		manifestYAML = string(payload)
	}
	metadata, _, _ := unstructured.NestedMap(item.Object, "metadata")
	spec, _, _ := unstructured.NestedMap(item.Object, "spec")
	status, _, _ := unstructured.NestedMap(item.Object, "status")
	modelMap, _, _ := unstructured.NestedMap(spec, "model")
	statusModelMap, _, _ := unstructured.NestedMap(status, "model")
	runtimeMap, _, _ := unstructured.NestedMap(spec, "runtime")
	modelRepo := firstNonEmptyString(firstNestedString(modelMap, "repo"), firstNestedString(statusModelMap, "repo"))
	modelName := firstNonEmptyString(firstNestedString(modelMap, "name"), firstNestedString(statusModelMap, "name"))
	modelSource := firstNonEmptyString(firstNestedString(modelMap, "source"), firstNestedString(statusModelMap, "source"))
	modelRevision := firstNonEmptyString(
		firstNestedString(modelMap, "revision"),
		firstNestedString(modelMap, "ref"),
		firstNestedString(statusModelMap, "revision"),
		firstNestedString(statusModelMap, "resolvedRevision"),
		firstNestedString(statusModelMap, "commit"),
		firstNestedString(statusModelMap, "commitHash"),
	)
	if modelSource == "" && modelRepo != "" {
		modelSource = "huggingface"
	}
	runtimeMetadata := map[string]any{
		"model":                model,
		"runtime":              runtime,
		"endpointPath":         endpointPath,
		"phase":                firstNestedString(status, "phase"),
		"modelRepo":            modelRepo,
		"modelName":            modelName,
		"modelSource":          modelSource,
		"modelRevision":        modelRevision,
		"runtimeRef":           firstNestedString(runtimeMap, "ref"),
		"runtimeImage":         firstNestedString(runtimeMap, "image"),
		"podBindingPolicy":     podEvidenceBindingPolicy,
		"podBindingVerified":   len(pods) > 0,
		"dtype":                firstNestedString(runtimeMap, "dtype"),
		"maxModelLen":          firstNestedValue(runtimeMap, "maxModelLen"),
		"tensorParallelSize":   firstNestedValue(runtimeMap, "tensorParallelSize"),
		"gpuMemoryUtilization": firstNestedValue(runtimeMap, "gpuMemoryUtilization"),
	}
	if isHuggingFaceSource(modelSource) {
		runtimeMetadata["huggingFaceRepo"] = modelRepo
		runtimeMetadata["huggingFaceRevision"] = modelRevision
	}
	for _, key := range []string{"runtimeVersion", "runtimeClassName", "schedulerName"} {
		if value := firstNonEmptyAny(firstNestedValue(status, key), firstNestedValue(spec, key)); value != nil {
			runtimeMetadata[key] = value
		}
	}
	imageMetadata := map[string]any{}
	for _, key := range []string{"image", "containerImage", "runtimeImage", "imageDigest"} {
		if value := firstNonEmptyAny(firstNestedValue(status, key), firstNestedValue(spec, key), firstNestedValue(runtimeMap, key)); value != nil {
			imageMetadata[key] = value
		}
	}
	if imageMetadata["runtimeImage"] == nil {
		if value := firstNestedValue(runtimeMap, "image"); value != nil {
			imageMetadata["runtimeImage"] = value
		}
	}
	podMetadata := podEvidenceMetadata(item, metadata, status, pods)
	addPodImageEvidence(imageMetadata, pods)
	return evidenceSnapshot{
		ManifestYAML:    manifestYAML,
		PodMetadata:     redactEvidenceMap(compactEvidenceMap(podMetadata)),
		RuntimeMetadata: redactEvidenceMap(compactEvidenceMap(runtimeMetadata)),
		ImageMetadata:   redactEvidenceMap(compactEvidenceMap(imageMetadata)),
	}
}

func podEvidenceMetadata(item unstructured.Unstructured, metadata, status map[string]any, pods []unstructured.Unstructured) map[string]any {
	base := map[string]any{
		"bindingPolicy":     podEvidenceBindingPolicy,
		"bindingVerified":   len(pods) > 0,
		"requiredLabels":    requiredInferOpsRuntimePodLabels,
		"selectorLabel":     runtimecontract.ModelDeploymentLabel,
		"selectorValue":     item.GetName(),
		"totalVerifiedPods": len(pods),
		"totalPods":         len(pods),
	}
	if len(pods) == 0 {
		if nested, _, _ := unstructured.NestedMap(status, "podMetadata"); len(nested) > 0 {
			base["fallback"] = nested
		} else {
			base["fallback"] = map[string]any{
				"name":        item.GetName(),
				"namespace":   firstNonEmptyString(item.GetNamespace(), firstNestedString(metadata, "namespace")),
				"labels":      item.GetLabels(),
				"annotations": item.GetAnnotations(),
			}
		}
		return base
	}
	limit := len(pods)
	if limit > maxEvidencePods {
		limit = maxEvidencePods
		base["truncated"] = true
	}
	podItems := make([]any, 0, limit)
	for _, pod := range pods[:limit] {
		podSpec, _, _ := unstructured.NestedMap(pod.Object, "spec")
		podStatus, _, _ := unstructured.NestedMap(pod.Object, "status")
		podItems = append(podItems, compactEvidenceMap(map[string]any{
			"name":               pod.GetName(),
			"namespace":          pod.GetNamespace(),
			"phase":              firstNestedString(podStatus, "phase"),
			"nodeName":           firstNestedString(podSpec, "nodeName"),
			"podIP":              firstNestedString(podStatus, "podIP"),
			"serviceAccountName": firstNestedString(podSpec, "serviceAccountName"),
			"runtimeClassName":   firstNestedString(podSpec, "runtimeClassName"),
			"labels":             pod.GetLabels(),
			"annotations":        pod.GetAnnotations(),
			"containers":         podContainerEvidence(podSpec, podStatus),
		}))
	}
	base["pods"] = podItems
	return base
}

func podContainerEvidence(podSpec, podStatus map[string]any) []any {
	statusByName := map[string]map[string]any{}
	for _, item := range firstNestedSlice(podStatus, "containerStatuses") {
		status, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := firstNestedString(status, "name")
		if name != "" {
			statusByName[name] = status
		}
	}
	containers := firstNestedSlice(podSpec, "containers")
	out := make([]any, 0, len(containers))
	for _, item := range containers {
		container, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := firstNestedString(container, "name")
		status := statusByName[name]
		out = append(out, compactEvidenceMap(map[string]any{
			"name":         name,
			"image":        firstNestedString(container, "image"),
			"imageID":      firstNestedString(status, "imageID"),
			"ready":        firstNestedValue(status, "ready"),
			"restartCount": firstNestedValue(status, "restartCount"),
			"resources":    objectOrNil(container, "resources"),
		}))
	}
	return out
}

func addPodImageEvidence(imageMetadata map[string]any, pods []unstructured.Unstructured) {
	images := map[string]bool{}
	imageIDs := map[string]bool{}
	for _, pod := range pods {
		podSpec, _, _ := unstructured.NestedMap(pod.Object, "spec")
		podStatus, _, _ := unstructured.NestedMap(pod.Object, "status")
		for _, item := range firstNestedSlice(podSpec, "containers") {
			container, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if image := firstNestedString(container, "image"); image != "" {
				images[image] = true
			}
		}
		for _, item := range firstNestedSlice(podStatus, "containerStatuses") {
			status, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if imageID := firstNestedString(status, "imageID"); imageID != "" {
				imageIDs[imageID] = true
			}
		}
	}
	if len(images) > 0 {
		imageMetadata["podImages"] = sortedKeys(images)
	}
	if len(imageIDs) > 0 {
		imageMetadata["podImageIDs"] = sortedKeys(imageIDs)
	}
}

func stableModelDeploymentManifest(item unstructured.Unstructured) map[string]any {
	metadata, _, _ := unstructured.NestedMap(item.Object, "metadata")
	status, _, _ := unstructured.NestedMap(item.Object, "status")
	manifest := map[string]any{
		"apiVersion": firstNonEmptyString(item.GetAPIVersion(), runtimecontract.ModelDeploymentAPIVersion),
		"kind":       firstNonEmptyString(item.GetKind(), runtimecontract.ModelDeploymentKind),
		"metadata": compactEvidenceMap(map[string]any{
			"name":        item.GetName(),
			"namespace":   firstNonEmptyString(item.GetNamespace(), firstNestedString(metadata, "namespace")),
			"labels":      item.GetLabels(),
			"annotations": item.GetAnnotations(),
		}),
		"spec": objectOrNil(item.Object, "spec"),
	}
	stableStatus := compactEvidenceMap(map[string]any{
		"phase":                  firstNestedString(status, "phase"),
		"modelName":              firstNonEmptyString(firstNestedString(status, "modelName"), firstNestedString(status, "model")),
		"runtime":                firstNestedString(status, "runtime"),
		"endpoint":               objectOrNil(status, "endpoint"),
		"endpointPath":           firstNestedString(status, "endpointPath"),
		"customEndpointsPresent": firstNonEmptyAny(firstNestedValue(status, "customEndpointsPresent"), firstNestedValue(status, "customEndpoints")),
		"gpu":                    objectOrNil(status, "gpu"),
		"region":                 firstNestedString(status, "region"),
		"pricing":                objectOrNil(status, "pricing"),
		"rateLimits":             objectOrNil(status, "rateLimits"),
		"podMetadata":            objectOrNil(status, "podMetadata"),
		"runtimeVersion":         firstNestedString(status, "runtimeVersion"),
		"image":                  firstNestedString(status, "image"),
		"containerImage":         firstNestedString(status, "containerImage"),
		"runtimeImage":           firstNestedString(status, "runtimeImage"),
		"imageDigest":            firstNestedString(status, "imageDigest"),
	})
	if len(stableStatus) > 0 {
		manifest["status"] = stableStatus
	}
	return manifest
}

func objectOrNil(parent map[string]any, key string) any {
	value, ok := parent[key]
	if !ok {
		return nil
	}
	nested, ok := value.(map[string]any)
	if !ok || len(nested) == 0 {
		return nil
	}
	return nested
}

func compactEvidenceMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := map[string]any{}
	for key, value := range input {
		if value == nil {
			continue
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
			continue
		}
		if nested, ok := value.(map[string]any); ok {
			if compacted := compactEvidenceMap(nested); len(compacted) > 0 {
				out[key] = compacted
			}
			continue
		}
		out[key] = value
	}
	return out
}

func redactEvidenceMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := map[string]any{}
	if evidenceMapDeclaresSensitiveName(input) {
		for key, value := range input {
			if key == "value" || key == "valueFrom" {
				out[key] = "[redacted]"
				continue
			}
			out[key] = redactEvidenceValue(value)
		}
		return out
	}
	for key, value := range input {
		if evidenceKeySensitive(key) {
			out[key] = "[redacted]"
			continue
		}
		out[key] = redactEvidenceValue(value)
	}
	return out
}

func redactEvidenceValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return redactEvidenceMap(typed)
	case map[string]string:
		out := make(map[string]string, len(typed))
		for key, item := range typed {
			if evidenceKeySensitive(key) {
				out[key] = "[redacted]"
				continue
			}
			out[key] = item
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, redactEvidenceValue(item))
		}
		return out
	default:
		return value
	}
}

func evidenceMapDeclaresSensitiveName(input map[string]any) bool {
	name, ok := input["name"]
	if !ok {
		return false
	}
	return evidenceKeySensitive(fmt.Sprint(name))
}

func evidenceKeySensitive(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, marker := range []string{"secret", "token", "password", "credential", "authorization", "api_key", "apikey", "private_key"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
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

func estimateAutomaticCapacity(model, runtime string, gpu gpuSnapshot, runtimeMap map[string]any) automaticCapacityEstimate {
	paramsB := modelParameterBillions(model)
	if paramsB <= 0 {
		paramsB = 7
	}
	bytesPerParameter := modelBytesPerParameter(model, firstNestedString(runtimeMap, "dtype"))
	gpuCount := maxInt(1, gpu.Count)
	gpuMemoryGB := maxInt(1, gpu.MemoryGB) * gpuCount
	gpuMemoryTarget := floatFromAny(firstNestedValue(runtimeMap, "gpuMemoryUtilization"))
	if gpuMemoryTarget <= 0 || gpuMemoryTarget > 1 {
		gpuMemoryTarget = 0.85
	}
	maxModelLen := intFromAny(firstPositiveAny(firstNestedValue(runtimeMap, "maxModelLen")))
	capacityContextTokens := maxModelLen
	if capacityContextTokens <= 0 {
		capacityContextTokens = 4096
	}
	weightGB := paramsB * bytesPerParameter * 1.2
	availableKVGB := float64(gpuMemoryGB)*gpuMemoryTarget - weightGB
	if availableKVGB < 1 {
		availableKVGB = 1
	}
	kvGBPerRequest := math.Max(0.08, paramsB*0.01*float64(capacityContextTokens)/1024)
	memoryConcurrency := maxInt(1, int(math.Floor(availableKVGB/kvGBPerRequest)))
	outputTPS := gpuOutputTokensPerSecond(gpu.Type) * float64(gpuCount) * runtimeThroughputFactor(runtime) * quantizationThroughputFactor(bytesPerParameter)
	outputTPS = outputTPS / math.Sqrt(math.Max(paramsB/7, 0.25))
	if outputTPS < 1 {
		outputTPS = 1
	}
	inputTPS := outputTPS * 3
	throughputConcurrency := maxInt(1, int(math.Floor(outputTPS/8)))
	maxConcurrent := minPositiveInt(memoryConcurrency, throughputConcurrency)
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	totalTPS := inputTPS + outputTPS
	return automaticCapacityEstimate{
		InputTokensPerSecond:  roundFloat(inputTPS, 2),
		OutputTokensPerSecond: roundFloat(outputTPS, 2),
		TokensPerSecond:       roundFloat(totalTPS, 2),
		TokensPerMinute:       maxInt(1, int(math.Floor(totalTPS*60))),
		MaxConcurrentRequests: maxConcurrent,
		ContextWindowTokens:   maxModelLen,
		Source:                "hardware-model-estimate",
	}
}

func modelParameterBillions(model string) float64 {
	value := strings.ToLower(model)
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return !(r == '.' || (r >= '0' && r <= '9') || r == 'b' || r == 'm')
	})
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if strings.HasSuffix(field, "b") {
			parsed, err := strconv.ParseFloat(strings.TrimSuffix(field, "b"), 64)
			if err == nil && parsed > 0 {
				return parsed
			}
		}
		if strings.HasSuffix(field, "m") {
			parsed, err := strconv.ParseFloat(strings.TrimSuffix(field, "m"), 64)
			if err == nil && parsed > 0 {
				return parsed / 1000
			}
		}
	}
	return 0
}

func modelBytesPerParameter(model, dtype string) float64 {
	text := strings.ToLower(model + " " + dtype)
	switch {
	case strings.Contains(text, "q2"):
		return 0.25
	case strings.Contains(text, "q3"):
		return 0.375
	case strings.Contains(text, "q4") || strings.Contains(text, "int4"):
		return 0.5
	case strings.Contains(text, "q5"):
		return 0.625
	case strings.Contains(text, "q8") || strings.Contains(text, "int8") || strings.Contains(text, "fp8"):
		return 1
	case strings.Contains(text, "bf16") || strings.Contains(text, "fp16") || strings.Contains(text, "float16"):
		return 2
	default:
		return 2
	}
}

func gpuOutputTokensPerSecond(gpuType string) float64 {
	value := strings.ToLower(gpuType)
	switch {
	case strings.Contains(value, "b200"):
		return 2600
	case strings.Contains(value, "h200"):
		return 2200
	case strings.Contains(value, "h100"):
		return 1800
	case strings.Contains(value, "a100"):
		return 1000
	case strings.Contains(value, "l40"):
		return 850
	case strings.Contains(value, "4090"):
		return 650
	case strings.Contains(value, "a10"):
		return 380
	case strings.Contains(value, "l4"):
		return 300
	case strings.Contains(value, "t4"):
		return 120
	case strings.Contains(value, "mi300"):
		return 1200
	case strings.Contains(value, "mi250"):
		return 700
	default:
		return 80
	}
}

func runtimeThroughputFactor(runtime string) float64 {
	value := strings.ToLower(runtime)
	switch {
	case strings.Contains(value, "sglang"):
		return 1.05
	case strings.Contains(value, "vllm"):
		return 1.10
	case strings.Contains(value, "llama"):
		return 0.75
	default:
		return 1
	}
}

func quantizationThroughputFactor(bytesPerParameter float64) float64 {
	switch {
	case bytesPerParameter <= 0.5:
		return 1.6
	case bytesPerParameter <= 1:
		return 1.25
	default:
		return 1
	}
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

func stringMapFromAny(value any) map[string]string {
	switch v := value.(type) {
	case map[string]string:
		out := make(map[string]string, len(v))
		for key, item := range v {
			if strings.TrimSpace(key) != "" {
				out[strings.TrimSpace(key)] = strings.TrimSpace(item)
			}
		}
		return out
	case map[string]any:
		out := make(map[string]string, len(v))
		for key, item := range v {
			if strings.TrimSpace(key) != "" && nonEmptyAny(item) {
				out[strings.TrimSpace(key)] = strings.TrimSpace(fmt.Sprint(item))
			}
		}
		return out
	default:
		return nil
	}
}

func runtimeEnvInt(values map[string]string, keys ...string) int {
	if len(values) == 0 {
		return 0
	}
	for _, key := range keys {
		value := strings.TrimSpace(values[key])
		if value == "" {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err == nil && parsed > 0 {
			return parsed
		}
	}
	return 0
}

func firstNestedSlice(parent map[string]any, key string) []any {
	value := firstNestedValue(parent, key)
	items, _ := value.([]any)
	return items
}

func firstNonEmptyAny(values ...any) any {
	for _, value := range values {
		if nonEmptyAny(value) {
			return value
		}
	}
	return nil
}

func firstPositiveAny(values ...any) any {
	for _, value := range values {
		if floatFromAny(value) > 0 {
			return value
		}
	}
	return nil
}

func firstCredibleStatusThroughputAny(values ...any) any {
	for _, value := range values {
		if floatFromAny(value) > 1 {
			return value
		}
	}
	return nil
}

func firstCredibleStatusTokensPerMinuteAny(value any) any {
	if intFromAny(value) > 60 {
		return value
	}
	return nil
}

func firstCredibleStatusConcurrencyAny(value any, fallback int) any {
	parsed := intFromAny(value)
	if parsed > 1 || fallback <= 1 {
		return value
	}
	return nil
}

func sourceForPositiveInt(value int, source string) string {
	if value <= 0 {
		return ""
	}
	return source
}

func deploymentCapacityReported(metricsMap, capacityMap map[string]any) bool {
	throughputKeys := []string{
		"tokensPerSecondEstimate",
		"inputTokensPerSecondEstimate",
		"promptTokensPerSecondEstimate",
		"outputTokensPerSecondEstimate",
		"generationTokensPerSecondEstimate",
	}
	for _, key := range throughputKeys {
		if floatFromAny(firstNestedValue(metricsMap, key)) > 1 || floatFromAny(firstNestedValue(capacityMap, key)) > 1 {
			return true
		}
	}
	if intFromAny(firstNestedValue(metricsMap, "maxConcurrentRequests")) > 1 || intFromAny(firstNestedValue(capacityMap, "maxConcurrentRequests")) > 1 {
		return true
	}
	return false
}

func statusConcurrencyIsCredible(value any, fallback int) bool {
	parsed := intFromAny(value)
	return parsed > 1 || (parsed == 1 && fallback <= 1)
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

func normalizeEndpointPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	return path.Clean(value)
}

func isDefaultOpenAIEndpointPath(value string) bool {
	normalized := normalizeEndpointPath(value)
	return normalized == "/v1" || strings.HasPrefix(normalized, "/models/") && strings.HasSuffix(normalized, "/v1")
}

func sortedKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
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
	case int64:
		return float64(v)
	case int32:
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

func positiveFloat(value float64) float64 {
	if value <= 0 {
		return 0
	}
	return value
}

func boundedFloat(value, minimum, maximum float64) float64 {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func firstMetricValue(families map[string]*dto.MetricFamily, names ...string) float64 {
	for _, name := range names {
		value := sumMetricFamily(families[name])
		if value > 0 {
			return value
		}
	}
	return 0
}

func hasAnyMetricFamily(families map[string]*dto.MetricFamily, names ...string) bool {
	for _, name := range names {
		if family := families[name]; family != nil && len(family.Metric) > 0 {
			return true
		}
	}
	return false
}

func sumMetricFamily(family *dto.MetricFamily) float64 {
	if family == nil {
		return 0
	}
	var total float64
	for _, metric := range family.Metric {
		if metric == nil {
			continue
		}
		switch {
		case metric.Gauge != nil:
			total += metric.Gauge.GetValue()
		case metric.Counter != nil:
			total += metric.Counter.GetValue()
		}
	}
	return total
}

func roundFloat(value float64, precision int) float64 {
	scale := math.Pow10(precision)
	return math.Round(value*scale) / scale
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

func minPositiveInt(values ...int) int {
	out := 0
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if out == 0 || value < out {
			out = value
		}
	}
	return out
}

func maxFloat(values ...float64) float64 {
	var out float64
	for _, value := range values {
		if value > out {
			out = value
		}
	}
	return out
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
