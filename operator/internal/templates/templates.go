package templates

// RuntimeContainerName is the stable container name for managed runtime pods.
const RuntimeContainerName = "runtime"

const (
	// RuntimeServiceSuffix is appended to a ModelDeployment name for its stable Service.
	RuntimeServiceSuffix = "-runtime"
	// RuntimeHTTPPort is the default container and Service target port.
	RuntimeHTTPPort int32 = 8000
	// RuntimeHealthPath is used for readiness and liveness checks.
	RuntimeHealthPath = "/health"
	// RuntimeMetricsPath exposes Prometheus metrics.
	RuntimeMetricsPath = "/metrics"
	// GatewayModelPathPrefix is the stable per-model route prefix.
	GatewayModelPathPrefix = "/models/"
	// OpenAIPathPrefix is the path exposed by OpenAI-compatible runtimes.
	OpenAIPathPrefix = "/v1"

	// EnvModelRepo identifies the source model repository.
	EnvModelRepo = "MODEL_REPO"
	// EnvModelRevision identifies the source model revision.
	EnvModelRevision = "MODEL_REVISION"
	// EnvModelPath identifies the prepared local model path.
	EnvModelPath = "MODEL_PATH"
	// EnvMaxModelLen configures the maximum model context length.
	EnvMaxModelLen = "MAX_MODEL_LEN"
	// EnvTensorParallelSize configures the whole-GPU tensor parallel count.
	EnvTensorParallelSize = "TENSOR_PARALLEL_SIZE"
	// EnvGPUMemoryUtilization configures the runtime GPU memory target.
	EnvGPUMemoryUtilization = "GPU_MEMORY_UTILIZATION"
	// EnvPort configures the runtime HTTP listen port.
	EnvPort = "PORT"
	// EnvDrainTimeout configures the maximum in-flight request drain time.
	EnvDrainTimeout = "INFEROPS_DRAIN_TIMEOUT"
)

// RuntimeServiceName returns the stable runtime Service name for a ModelDeployment.
func RuntimeServiceName(modelDeploymentName string) string {
	return modelDeploymentName + RuntimeServiceSuffix
}

// GatewayModelPath returns the stable gateway base path for a ModelDeployment.
func GatewayModelPath(modelDeploymentName string) string {
	return GatewayModelPathPrefix + modelDeploymentName
}

// GatewayOpenAIBasePath returns the stable OpenAI-compatible gateway base path.
func GatewayOpenAIBasePath(modelDeploymentName string) string {
	return GatewayModelPath(modelDeploymentName) + OpenAIPathPrefix
}
