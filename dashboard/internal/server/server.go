// Package server implements the self-hosted InferOps dashboard HTTP API.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"time"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

//go:embed static
var staticFiles embed.FS

const (
	defaultRequestTimeout = 5 * time.Second
	defaultMaxEvents      = 25
)

// Options configures the dashboard handler.
type Options struct {
	Namespace      string
	PrometheusURL  string
	GatewayBaseURL string
	RequestTimeout time.Duration
	MaxEvents      int
	Now            func() time.Time
}

// Server serves a read-only dashboard API backed by the Kubernetes API.
type Server struct {
	client         client.Client
	namespace      string
	prometheusURL  string
	gatewayBaseURL string
	requestTimeout time.Duration
	maxEvents      int
	now            func() time.Time
}

// New returns a dashboard HTTP handler. The handler never reads Kubernetes
// Secrets and never mutates custom resources.
func New(kubernetesClient client.Client, options Options) (*Server, error) {
	if kubernetesClient == nil {
		return nil, errors.New("Kubernetes client is required")
	}
	namespace := strings.TrimSpace(options.Namespace)
	if namespace == "" {
		return nil, errors.New("namespace is required")
	}
	requestTimeout := options.RequestTimeout
	if requestTimeout == 0 {
		requestTimeout = defaultRequestTimeout
	}
	if requestTimeout < time.Second {
		return nil, errors.New("request timeout must be at least one second")
	}
	maxEvents := options.MaxEvents
	if maxEvents == 0 {
		maxEvents = defaultMaxEvents
	}
	if maxEvents < 0 {
		return nil, errors.New("max events must be non-negative")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Server{
		client:         kubernetesClient,
		namespace:      namespace,
		prometheusURL:  strings.TrimRight(strings.TrimSpace(options.PrometheusURL), "/"),
		gatewayBaseURL: strings.TrimRight(strings.TrimSpace(options.GatewayBaseURL), "/"),
		requestTimeout: requestTimeout,
		maxEvents:      maxEvents,
		now:            now,
	}, nil
}

// Handler returns the dashboard HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", plainText(http.StatusOK, "ok\n"))
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("GET /api/snapshot", s.snapshot)
	mux.HandleFunc("GET /api/generated-yaml", s.generatedYAML)
	mux.Handle("/", staticHandler())
	return securityHeaders(mux)
}

func (s *Server) readyz(response http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), s.requestTimeout)
	defer cancel()
	var deployments v1alpha1.ModelDeploymentList
	if err := s.client.List(ctx, &deployments, client.InNamespace(s.namespace), client.Limit(1)); err != nil {
		writeProblem(response, http.StatusServiceUnavailable, fmt.Errorf("list model deployments: %w", err))
		return
	}
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write([]byte("ok\n"))
}

func (s *Server) snapshot(response http.ResponseWriter, request *http.Request) {
	snap, err := s.buildSnapshot(request.Context())
	if err != nil {
		writeProblem(response, http.StatusServiceUnavailable, err)
		return
	}
	writeJSON(response, http.StatusOK, snap)
}

func (s *Server) generatedYAML(response http.ResponseWriter, request *http.Request) {
	snippets, err := s.buildGeneratedYAML(request.Context())
	if err != nil {
		writeProblem(response, http.StatusServiceUnavailable, err)
		return
	}
	writeJSON(response, http.StatusOK, snippets)
}

func (s *Server) buildSnapshot(ctx context.Context) (Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, s.requestTimeout)
	defer cancel()

	var deployments v1alpha1.ModelDeploymentList
	if err := s.client.List(ctx, &deployments, client.InNamespace(s.namespace)); err != nil {
		return Snapshot{}, fmt.Errorf("list model deployments: %w", err)
	}
	var caches v1alpha1.ModelCacheList
	if err := s.client.List(ctx, &caches, client.InNamespace(s.namespace)); err != nil {
		return Snapshot{}, fmt.Errorf("list model caches: %w", err)
	}
	var runtimes v1alpha1.ModelRuntimeList
	if err := s.client.List(ctx, &runtimes, client.InNamespace(s.namespace)); err != nil {
		return Snapshot{}, fmt.Errorf("list model runtimes: %w", err)
	}
	var services corev1.ServiceList
	if err := s.client.List(ctx, &services, client.InNamespace(s.namespace)); err != nil {
		return Snapshot{}, fmt.Errorf("list services: %w", err)
	}
	var pods corev1.PodList
	if err := s.client.List(ctx, &pods, client.InNamespace(s.namespace)); err != nil {
		return Snapshot{}, fmt.Errorf("list pods: %w", err)
	}
	var nodes corev1.NodeList
	if err := s.client.List(ctx, &nodes); err != nil {
		return Snapshot{}, fmt.Errorf("list nodes: %w", err)
	}
	var events corev1.EventList
	if s.maxEvents > 0 {
		if err := s.client.List(ctx, &events, client.InNamespace(s.namespace)); err != nil {
			return Snapshot{}, fmt.Errorf("list events: %w", err)
		}
	}

	serviceIndex := indexServices(services.Items)
	snapshot := Snapshot{
		GeneratedAt: s.now().UTC().Format(time.RFC3339),
		Namespace:   s.namespace,
		Summary: Summary{
			Deployments: len(deployments.Items),
			Caches:      len(caches.Items),
			Runtimes:    len(runtimes.Items),
		},
		Metrics: MetricsLinks{
			PrometheusURL: s.prometheusURL,
			Queries:       defaultMetricQueries(),
		},
	}
	for _, deployment := range deployments.Items {
		summary := summarizeDeployment(deployment, serviceIndex, s.gatewayBaseURL)
		snapshot.Deployments = append(snapshot.Deployments, summary)
		snapshot.Endpoints = append(snapshot.Endpoints, summary.Endpoint)
	}
	for _, cache := range caches.Items {
		snapshot.Caches = append(snapshot.Caches, summarizeCache(cache))
	}
	for _, runtime := range runtimes.Items {
		snapshot.Runtimes = append(snapshot.Runtimes, summarizeRuntime(runtime))
	}
	snapshot.GPUs = summarizeGPUs(nodes.Items, pods.Items)
	snapshot.Events = summarizeEvents(events.Items, s.maxEvents)
	sortSnapshot(&snapshot)
	return snapshot, nil
}

func (s *Server) buildGeneratedYAML(ctx context.Context) (GeneratedYAMLResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, s.requestTimeout)
	defer cancel()

	var deployments v1alpha1.ModelDeploymentList
	if err := s.client.List(ctx, &deployments, client.InNamespace(s.namespace)); err != nil {
		return GeneratedYAMLResponse{}, fmt.Errorf("list model deployments: %w", err)
	}
	snippets := GeneratedYAMLResponse{
		GeneratedAt: s.now().UTC().Format(time.RFC3339),
		Namespace:   s.namespace,
	}
	for _, deployment := range deployments.Items {
		rendered, err := sanitizedDeploymentYAML(deployment)
		if err != nil {
			return GeneratedYAMLResponse{}, fmt.Errorf("render deployment %s/%s YAML: %w", deployment.Namespace, deployment.Name, err)
		}
		snippets.Deployments = append(snippets.Deployments, YAMLSnippet{
			Namespace: deployment.Namespace,
			Name:      deployment.Name,
			Kind:      "ModelDeployment",
			YAML:      rendered,
		})
	}
	sort.Slice(snippets.Deployments, func(i, j int) bool {
		return snippets.Deployments[i].Name < snippets.Deployments[j].Name
	})
	return snippets, nil
}

func sanitizedDeploymentYAML(deployment v1alpha1.ModelDeployment) (string, error) {
	spec, err := sanitizedSpecMap(deployment.Spec)
	if err != nil {
		return "", err
	}
	metadata := map[string]any{
		"name":      deployment.Name,
		"namespace": deployment.Namespace,
	}
	if len(deployment.Labels) > 0 {
		metadata["labels"] = copyStringMap(deployment.Labels)
	}
	manifest := map[string]any{
		"apiVersion": v1alpha1.GroupVersion.String(),
		"kind":       "ModelDeployment",
		"metadata":   metadata,
		"spec":       spec,
	}
	data, err := yaml.Marshal(manifest)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)) + "\n", nil
}

func sanitizedSpecMap(spec v1alpha1.ModelDeploymentSpec) (map[string]any, error) {
	data, err := json.Marshal(spec.DeepCopy())
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	delete(out, "secrets")
	return out, nil
}

func summarizeDeployment(
	deployment v1alpha1.ModelDeployment,
	services map[string]corev1.Service,
	gatewayBaseURL string,
) DeploymentSummary {
	endpoint := EndpointSummary{
		Namespace: deployment.Namespace,
		Name:      deployment.Name,
		Route:     deployment.Spec.Routing.Path,
		Service:   deployment.Status.ServiceName,
		StatusURL: deployment.Status.Endpoint,
	}
	if endpoint.Route == "" {
		endpoint.Route = "/models/" + deployment.Name
	}
	if gatewayBaseURL != "" {
		endpoint.GatewayURL = gatewayBaseURL + endpoint.Route
	}
	if service, found := services[deployment.Status.ServiceName]; found {
		endpoint.ServiceType = string(service.Spec.Type)
		endpoint.ClusterIP = service.Spec.ClusterIP
	}
	return DeploymentSummary{
		Namespace: deployment.Namespace,
		Name:      deployment.Name,
		Model:     deployment.Spec.Model.Repo,
		Runtime:   deployment.Spec.Runtime.Ref,
		Phase:     string(deployment.Status.Phase),
		Endpoint:  endpoint,
		Activation: ActivationSummary{
			DesiredState: string(deployment.Spec.Activation.DesiredState),
			WhenFull:     string(deployment.Spec.Activation.WhenFull),
			Priority:     deployment.Spec.Activation.Priority,
			DrainTimeout: deployment.Spec.Activation.DrainTimeout,
			DrainStarted: timeString(deployment.Status.DrainStartedAt),
		},
		Scaling: ScalingSummary{
			MinReplicas:      deployment.Spec.Scaling.MinReplicas,
			MaxReplicas:      deployment.Spec.Scaling.MaxReplicas,
			TargetPending:    deployment.Spec.Scaling.TargetPendingRequests,
			IdleTimeout:      deployment.Spec.Scaling.IdleTimeout,
			DesiredReplicas:  deployment.Status.Scaling.DesiredReplicas,
			ReadyReplicas:    deployment.Status.Replicas.Ready,
			PendingRequests:  deployment.Status.Scaling.PendingRequests,
			RunningRequests:  deployment.Status.Scaling.RunningRequests,
			CapacityLimited:  deployment.Status.Scaling.CapacityLimited,
			Reason:           deployment.Status.Scaling.Reason,
			Message:          deployment.Status.Scaling.Message,
			LastActivityTime: timeString(deployment.Status.Scaling.LastActivityTime),
		},
		Cache: CacheReference{
			State:    deployment.Status.Cache.State,
			NodeName: deployment.Status.Cache.NodeName,
			Path:     deployment.Status.Cache.Path,
		},
		GPU: GPUAssignment{
			RequestedCount: requestedGPUCount(deployment),
			Vendor:         requestedGPUVendor(deployment),
			Type:           requestedGPUType(deployment),
			AssignedNode:   deployment.Status.AssignedNode,
			AssignedGPUs:   append([]string(nil), deployment.Status.AssignedGPUs...),
		},
		Routing: RoutingSummary{
			Enabled:          deployment.Spec.Routing.Enabled,
			OpenAICompatible: deployment.Spec.Routing.OpenAICompatible,
			Path:             endpoint.Route,
			Strategy:         string(deployment.Spec.Routing.Policy.RoutingStrategy),
			Weight:           int32PtrValue(deployment.Spec.Routing.Policy.Weight),
		},
		Logs: LogsSummary{
			PodSelector: fmt.Sprintf(
				"app.kubernetes.io/part-of=inferops,app.kubernetes.io/component=model-runtime,inferops.dev/model-deployment=%s",
				deployment.Name,
			),
			Kubectl: fmt.Sprintf(
				"kubectl -n %s logs -l app.kubernetes.io/part-of=inferops,app.kubernetes.io/component=model-runtime,inferops.dev/model-deployment=%s --tail=100",
				deployment.Namespace,
				deployment.Name,
			),
		},
		Conditions: summarizeConditions(deployment.Status.Conditions),
	}
}

func summarizeCache(cache v1alpha1.ModelCache) CacheSummary {
	return CacheSummary{
		Namespace:    cache.Namespace,
		Name:         cache.Name,
		ModelRepo:    cache.Spec.ModelRepo,
		Revision:     firstNonEmpty(cache.Status.Revision, cache.Spec.Revision),
		Phase:        string(cache.Status.Phase),
		NodeName:     cache.Status.NodeName,
		Path:         cache.Status.Path,
		Size:         cache.Status.Size,
		ReservedSize: cache.Status.ReservedSize,
		LastUsedTime: timeValueString(cache.Status.LastUsedTime),
		Conditions:   summarizeConditions(cache.Status.Conditions),
	}
}

func summarizeRuntime(runtime v1alpha1.ModelRuntime) RuntimeSummary {
	return RuntimeSummary{
		Namespace:     runtime.Namespace,
		Name:          runtime.Name,
		Engine:        runtime.Spec.Engine,
		Protocol:      runtime.Spec.Protocol,
		DefaultImage:  runtime.Spec.DefaultImage,
		Port:          runtime.Spec.Port,
		HealthPath:    runtime.Spec.HealthPath,
		ReadinessPath: runtime.Spec.ReadinessPath,
		MetricsPath:   runtime.Spec.MetricsPath,
		Phase:         string(runtime.Status.Phase),
		Conditions:    summarizeConditions(runtime.Status.Conditions),
	}
}

func summarizeGPUs(nodes []corev1.Node, pods []corev1.Pod) []GPUSummary {
	requested := requestedGPUByNode(pods)
	var summaries []GPUSummary
	for _, node := range nodes {
		resources := gpuResourceNames(node.Status.Capacity, node.Status.Allocatable, requested[node.Name])
		for _, name := range resources {
			summaries = append(summaries, GPUSummary{
				NodeName:    node.Name,
				Resource:    string(name),
				Capacity:    quantityString(node.Status.Capacity[name]),
				Allocatable: quantityString(node.Status.Allocatable[name]),
				Requested:   quantityString(requested[node.Name][name]),
			})
		}
	}
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].NodeName == summaries[j].NodeName {
			return summaries[i].Resource < summaries[j].Resource
		}
		return summaries[i].NodeName < summaries[j].NodeName
	})
	return summaries
}

func summarizeEvents(events []corev1.Event, maxEvents int) []EventSummary {
	sort.SliceStable(events, func(i, j int) bool {
		return eventTime(events[i]).After(eventTime(events[j]))
	})
	if len(events) > maxEvents {
		events = events[:maxEvents]
	}
	out := make([]EventSummary, 0, len(events))
	for _, event := range events {
		out = append(out, EventSummary{
			Type:           event.Type,
			Reason:         event.Reason,
			Message:        event.Message,
			InvolvedObject: event.InvolvedObject.Kind + "/" + event.InvolvedObject.Name,
			LastSeen:       eventTime(event).UTC().Format(time.RFC3339),
			Count:          event.Count,
		})
	}
	return out
}

func summarizeConditions(conditions []v1alpha1.Condition) []ConditionSummary {
	out := make([]ConditionSummary, 0, len(conditions))
	for _, condition := range conditions {
		out = append(out, ConditionSummary{
			Type:               condition.Type,
			Status:             string(condition.Status),
			Reason:             condition.Reason,
			Message:            condition.Message,
			LastTransitionTime: timeValueString(condition.LastTransitionTime),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Type < out[j].Type
	})
	return out
}

func requestedGPUByNode(pods []corev1.Pod) map[string]map[corev1.ResourceName]resource.Quantity {
	out := make(map[string]map[corev1.ResourceName]resource.Quantity)
	for _, pod := range pods {
		if pod.Spec.NodeName == "" || pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		if out[pod.Spec.NodeName] == nil {
			out[pod.Spec.NodeName] = make(map[corev1.ResourceName]resource.Quantity)
		}
		for _, container := range pod.Spec.Containers {
			for name, quantity := range container.Resources.Requests {
				if !isGPUResource(name) {
					continue
				}
				current := out[pod.Spec.NodeName][name]
				current.Add(quantity)
				out[pod.Spec.NodeName][name] = current
			}
		}
	}
	return out
}

func gpuResourceNames(resourceLists ...corev1.ResourceList) []corev1.ResourceName {
	seen := map[corev1.ResourceName]struct{}{}
	for _, resources := range resourceLists {
		for name := range resources {
			if isGPUResource(name) {
				seen[name] = struct{}{}
			}
		}
	}
	out := make([]corev1.ResourceName, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func isGPUResource(name corev1.ResourceName) bool {
	return strings.Contains(strings.ToLower(string(name)), "gpu")
}

func indexServices(services []corev1.Service) map[string]corev1.Service {
	out := make(map[string]corev1.Service, len(services))
	for _, service := range services {
		out[service.Name] = service
	}
	return out
}

func sortSnapshot(snapshot *Snapshot) {
	sort.Slice(snapshot.Deployments, func(i, j int) bool {
		return snapshot.Deployments[i].Name < snapshot.Deployments[j].Name
	})
	sort.Slice(snapshot.Caches, func(i, j int) bool {
		return snapshot.Caches[i].Name < snapshot.Caches[j].Name
	})
	sort.Slice(snapshot.Runtimes, func(i, j int) bool {
		return snapshot.Runtimes[i].Name < snapshot.Runtimes[j].Name
	})
	sort.Slice(snapshot.Endpoints, func(i, j int) bool {
		return snapshot.Endpoints[i].Name < snapshot.Endpoints[j].Name
	})
}

func defaultMetricQueries() []MetricQuery {
	return []MetricQuery{
		{Name: "Gateway requests", Query: "sum(rate(inferops_gateway_requests_total[5m])) by (model)"},
		{Name: "Gateway latency p95", Query: "histogram_quantile(0.95, sum(rate(inferops_gateway_request_duration_seconds_bucket[5m])) by (le, model))"},
		{Name: "Active requests", Query: "sum(inferops_gateway_active_requests) by (model)"},
		{Name: "GPU slots available", Query: "sum(inferops_gpu_slots_available) by (node, resource)"},
		{Name: "Activation duration p95", Query: "histogram_quantile(0.95, sum(rate(inferops_model_activation_duration_seconds_bucket[30m])) by (le))"},
		{Name: "Runtime waiting requests", Query: "sum(vllm:num_requests_waiting) by (model_name)"},
		{Name: "Runtime tokens per second", Query: "sum(rate(vllm:generation_tokens_total[5m])) by (model_name)"},
	}
}

func requestedGPUCount(deployment v1alpha1.ModelDeployment) int32 {
	if deployment.Spec.Resources.GPU == nil {
		return 0
	}
	return deployment.Spec.Resources.GPU.Count
}

func requestedGPUVendor(deployment v1alpha1.ModelDeployment) string {
	if deployment.Spec.Resources.GPU == nil {
		return ""
	}
	return deployment.Spec.Resources.GPU.Vendor
}

func requestedGPUType(deployment v1alpha1.ModelDeployment) string {
	if deployment.Spec.Resources.GPU == nil {
		return ""
	}
	return deployment.Spec.Resources.GPU.Type
}

func int32PtrValue(value *int32) int32 {
	if value == nil {
		return 0
	}
	return *value
}

func quantityString(quantity resource.Quantity) string {
	if quantity.IsZero() {
		return "0"
	}
	return quantity.String()
}

func timeString(value *metav1.Time) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func timeValueString(value metav1.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func eventTime(event corev1.Event) time.Time {
	switch {
	case !event.LastTimestamp.IsZero():
		return event.LastTimestamp.Time
	case !event.EventTime.IsZero():
		return event.EventTime.Time
	case !event.FirstTimestamp.IsZero():
		return event.FirstTimestamp.Time
	default:
		return event.CreationTimestamp.Time
	}
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func staticHandler() http.Handler {
	content, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(fmt.Sprintf("dashboard static assets are unavailable: %v", err))
	}
	return http.FileServer(http.FS(content))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'")
		next.ServeHTTP(response, request)
	})
}

func plainText(status int, body string) http.HandlerFunc {
	return func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
		response.WriteHeader(status)
		_, _ = response.Write([]byte(body))
	}
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		http.Error(response, "encode response", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_, _ = response.Write(append(data, '\n'))
}

func writeProblem(response http.ResponseWriter, status int, err error) {
	writeJSON(response, status, map[string]string{
		"error": err.Error(),
	})
}
