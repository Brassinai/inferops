package templates

import (
	"github.com/brassinai/inferops/internal/routingpath"
	"github.com/brassinai/inferops/internal/runtimecontract"
)

// RuntimeContainerName is the stable container name for managed runtime pods.
const RuntimeContainerName = "runtime"

const (
	// RuntimeServiceSuffix is appended to a ModelDeployment name for its stable Service.
	RuntimeServiceSuffix = runtimecontract.ServiceSuffix
	// RuntimeHTTPPort is the default container and Service target port.
	RuntimeHTTPPort int32 = 8000
	// RuntimeHealthPath is used for process liveness checks.
	RuntimeHealthPath = "/health"
	// RuntimeReadinessPath is the default readiness endpoint for packaged runtimes.
	RuntimeReadinessPath = "/health"
	// RuntimeMetricsPath exposes Prometheus metrics.
	RuntimeMetricsPath = "/metrics"
	// GatewayModelPathPrefix is the stable per-model route prefix.
	GatewayModelPathPrefix = routingpath.DefaultModelPrefix
	// OpenAIPathPrefix is the path exposed by OpenAI-compatible runtimes.
	OpenAIPathPrefix = "/v1"

	// EnvModelRepo identifies the model name exposed by the runtime API.
	EnvModelRepo = "MODEL_REPO"
	// EnvModelPath identifies the prepared local model path.
	EnvModelPath = "MODEL_PATH"
	// EnvMaxModelLen configures the maximum model context length.
	EnvMaxModelLen = "MAX_MODEL_LEN"
	// EnvTensorParallelSize configures the whole-GPU tensor parallel count.
	EnvTensorParallelSize = "TENSOR_PARALLEL_SIZE"
	// EnvGPUMemoryUtilization configures the runtime GPU memory target.
	EnvGPUMemoryUtilization = "GPU_MEMORY_UTILIZATION"
	// EnvModelDType configures the runtime model data type.
	EnvModelDType = "MODEL_DTYPE"
	// EnvPort configures the runtime HTTP listen port.
	EnvPort = "PORT"

	// CacheVolumeName is the name of the volume mounting the prepared model cache.
	CacheVolumeName = "model-cache"
	// RuntimeModelMountPath is the stable in-container path for prepared model files.
	RuntimeModelMountPath = "/models/model"
	// HTTPPortName is the canonical name for the runtime HTTP port.
	HTTPPortName = runtimecontract.HTTPPortName
	// DefaultGPUVendor is the default GPU vendor resource name prefix.
	DefaultGPUVendor = "nvidia"
	// CacheDownloaderContainerName is the container name for cache download Jobs.
	CacheDownloaderContainerName = "downloader"
	// CacheDownloaderJobSuffix is appended to a ModelCache name for its download Job.
	CacheDownloaderJobSuffix = "-download"

	// ProbePeriodSeconds is the default probe interval.
	ProbePeriodSeconds int32 = 10
	// ProbeTimeoutSeconds is the default probe timeout.
	ProbeTimeoutSeconds int32 = 5
	// ProbeFailureThreshold is the default failure threshold for readiness/liveness.
	ProbeFailureThreshold int32 = 3
	// StartupProbeFailureThreshold is the startup probe failure threshold.
	StartupProbeFailureThreshold int32 = 30
)

// RuntimeServiceName returns the stable runtime Service name for a ModelDeployment.
func RuntimeServiceName(modelDeploymentName string) string {
	return runtimecontract.ServiceName(modelDeploymentName)
}

// GatewayModelPath returns the stable gateway base path for a ModelDeployment.
func GatewayModelPath(modelDeploymentName string) string {
	return routingpath.DefaultModelRoute(modelDeploymentName)
}

// GatewayOpenAIBasePath returns the stable OpenAI-compatible gateway base path.
func GatewayOpenAIBasePath(modelDeploymentName string) string {
	return GatewayModelPath(modelDeploymentName) + OpenAIPathPrefix
}
