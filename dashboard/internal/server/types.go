package server

// Snapshot is the dashboard's read-only cluster view.
type Snapshot struct {
	GeneratedAt string              `json:"generatedAt"`
	Namespace   string              `json:"namespace"`
	Summary     Summary             `json:"summary"`
	Deployments []DeploymentSummary `json:"deployments"`
	Caches      []CacheSummary      `json:"caches"`
	Runtimes    []RuntimeSummary    `json:"runtimes"`
	GPUs        []GPUSummary        `json:"gpus"`
	Endpoints   []EndpointSummary   `json:"endpoints"`
	Events      []EventSummary      `json:"events"`
	Metrics     MetricsLinks        `json:"metrics"`
}

// Summary contains top-level counts.
type Summary struct {
	Deployments int `json:"deployments"`
	Caches      int `json:"caches"`
	Runtimes    int `json:"runtimes"`
}

// DeploymentSummary contains the operator-facing state for one model endpoint.
type DeploymentSummary struct {
	Namespace  string             `json:"namespace"`
	Name       string             `json:"name"`
	Model      string             `json:"model"`
	Runtime    string             `json:"runtime"`
	Phase      string             `json:"phase"`
	Endpoint   EndpointSummary    `json:"endpoint"`
	Activation ActivationSummary  `json:"activation"`
	Scaling    ScalingSummary     `json:"scaling"`
	Cache      CacheReference     `json:"cache"`
	GPU        GPUAssignment      `json:"gpu"`
	Routing    RoutingSummary     `json:"routing"`
	Logs       LogsSummary        `json:"logs"`
	Conditions []ConditionSummary `json:"conditions"`
}

// EndpointSummary describes where a model can be reached.
type EndpointSummary struct {
	Namespace   string `json:"namespace"`
	Name        string `json:"name"`
	Route       string `json:"route"`
	GatewayURL  string `json:"gatewayUrl,omitempty"`
	StatusURL   string `json:"statusUrl,omitempty"`
	Service     string `json:"service,omitempty"`
	ServiceType string `json:"serviceType,omitempty"`
	ClusterIP   string `json:"clusterIP,omitempty"`
}

// ActivationSummary exposes activation policy and observed drain state.
type ActivationSummary struct {
	DesiredState string `json:"desiredState,omitempty"`
	WhenFull     string `json:"whenFull,omitempty"`
	Priority     int32  `json:"priority,omitempty"`
	DrainTimeout string `json:"drainTimeout,omitempty"`
	DrainStarted string `json:"drainStarted,omitempty"`
}

// ScalingSummary exposes configured replica bounds and observed scaling state.
type ScalingSummary struct {
	MinReplicas      int32  `json:"minReplicas,omitempty"`
	MaxReplicas      int32  `json:"maxReplicas,omitempty"`
	TargetPending    int32  `json:"targetPendingRequests,omitempty"`
	IdleTimeout      string `json:"idleTimeout,omitempty"`
	DesiredReplicas  int32  `json:"desiredReplicas,omitempty"`
	ReadyReplicas    int32  `json:"readyReplicas,omitempty"`
	PendingRequests  int64  `json:"pendingRequests,omitempty"`
	RunningRequests  int64  `json:"runningRequests,omitempty"`
	CapacityLimited  bool   `json:"capacityLimited,omitempty"`
	Reason           string `json:"reason,omitempty"`
	Message          string `json:"message,omitempty"`
	LastActivityTime string `json:"lastActivityTime,omitempty"`
}

// CacheReference is the cache observed by a deployment.
type CacheReference struct {
	State    string `json:"state,omitempty"`
	NodeName string `json:"nodeName,omitempty"`
	Path     string `json:"path,omitempty"`
}

// GPUAssignment is the requested and observed GPU placement for a deployment.
type GPUAssignment struct {
	RequestedCount int32    `json:"requestedCount,omitempty"`
	Vendor         string   `json:"vendor,omitempty"`
	Type           string   `json:"type,omitempty"`
	AssignedNode   string   `json:"assignedNode,omitempty"`
	AssignedGPUs   []string `json:"assignedGPUs,omitempty"`
}

// RoutingSummary exposes gateway route policy.
type RoutingSummary struct {
	Enabled          bool   `json:"enabled"`
	OpenAICompatible bool   `json:"openAICompatible"`
	Path             string `json:"path"`
	Strategy         string `json:"strategy,omitempty"`
	Weight           int32  `json:"weight,omitempty"`
}

// LogsSummary contains safe log lookup hints.
type LogsSummary struct {
	PodSelector string `json:"podSelector"`
	Kubectl     string `json:"kubectl"`
}

// CacheSummary contains cache readiness and placement state.
type CacheSummary struct {
	Namespace    string             `json:"namespace"`
	Name         string             `json:"name"`
	ModelRepo    string             `json:"modelRepo"`
	Revision     string             `json:"revision,omitempty"`
	Phase        string             `json:"phase"`
	NodeName     string             `json:"nodeName,omitempty"`
	Path         string             `json:"path,omitempty"`
	Size         string             `json:"size,omitempty"`
	ReservedSize string             `json:"reservedSize,omitempty"`
	LastUsedTime string             `json:"lastUsedTime,omitempty"`
	Conditions   []ConditionSummary `json:"conditions"`
}

// RuntimeSummary contains reusable runtime configuration and health.
type RuntimeSummary struct {
	Namespace     string             `json:"namespace"`
	Name          string             `json:"name"`
	Engine        string             `json:"engine"`
	Protocol      string             `json:"protocol"`
	DefaultImage  string             `json:"defaultImage"`
	Port          int32              `json:"port"`
	HealthPath    string             `json:"healthPath"`
	ReadinessPath string             `json:"readinessPath,omitempty"`
	MetricsPath   string             `json:"metricsPath,omitempty"`
	Phase         string             `json:"phase"`
	Conditions    []ConditionSummary `json:"conditions"`
}

// GPUSummary reports a visible GPU resource on one node.
type GPUSummary struct {
	NodeName    string `json:"nodeName"`
	Resource    string `json:"resource"`
	Capacity    string `json:"capacity"`
	Allocatable string `json:"allocatable"`
	Requested   string `json:"requested"`
}

// EventSummary reports recent Kubernetes Events without extra object payloads.
type EventSummary struct {
	Type           string `json:"type"`
	Reason         string `json:"reason"`
	Message        string `json:"message"`
	InvolvedObject string `json:"involvedObject"`
	LastSeen       string `json:"lastSeen"`
	Count          int32  `json:"count"`
}

// ConditionSummary is a compact condition view shared across InferOps CRDs.
type ConditionSummary struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason,omitempty"`
	Message            string `json:"message,omitempty"`
	LastTransitionTime string `json:"lastTransitionTime,omitempty"`
}

// MetricsLinks contains Prometheus wiring hints.
type MetricsLinks struct {
	PrometheusURL string        `json:"prometheusUrl,omitempty"`
	Queries       []MetricQuery `json:"queries"`
}

// MetricQuery names one dashboard Prometheus query.
type MetricQuery struct {
	Name        string `json:"name"`
	Category    string `json:"category,omitempty"`
	Description string `json:"description,omitempty"`
	Query       string `json:"query"`
}

// GeneratedYAMLResponse contains sanitized manifests for current deployments.
type GeneratedYAMLResponse struct {
	GeneratedAt string        `json:"generatedAt"`
	Namespace   string        `json:"namespace"`
	Deployments []YAMLSnippet `json:"deployments"`
}

// YAMLSnippet is a generated Kubernetes manifest.
type YAMLSnippet struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	YAML      string `json:"yaml"`
}
