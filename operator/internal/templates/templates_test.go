package templates

import "testing"

func TestStableRuntimeNamesAndRoutes(t *testing.T) {
	t.Parallel()

	if got, want := RuntimeContainerName, "runtime"; got != want {
		t.Fatalf("RuntimeContainerName = %q, want %q", got, want)
	}
	if got, want := RuntimeReadinessPath, "/health"; got != want {
		t.Fatalf("RuntimeReadinessPath = %q, want %q", got, want)
	}
	if got, want := RuntimeServiceName("qwen-chat"), "qwen-chat-runtime"; got != want {
		t.Fatalf("RuntimeServiceName() = %q, want %q", got, want)
	}
	if got, want := GatewayModelPath("qwen-chat"), "/models/qwen-chat"; got != want {
		t.Fatalf("GatewayModelPath() = %q, want %q", got, want)
	}
	if got, want := GatewayOpenAIBasePath("qwen-chat"), "/models/qwen-chat/v1"; got != want {
		t.Fatalf("GatewayOpenAIBasePath() = %q, want %q", got, want)
	}
	if got, want := CacheVolumeName, "model-cache"; got != want {
		t.Fatalf("CacheVolumeName = %q, want %q", got, want)
	}
	if got, want := HTTPPortName, "http"; got != want {
		t.Fatalf("HTTPPortName = %q, want %q", got, want)
	}
	if got, want := RuntimeModelMountPath, "/models/model"; got != want {
		t.Fatalf("RuntimeModelMountPath = %q, want %q", got, want)
	}
	if got, want := DefaultGPUVendor, "nvidia"; got != want {
		t.Fatalf("DefaultGPUVendor = %q, want %q", got, want)
	}
	if got, want := CacheDownloaderContainerName, "downloader"; got != want {
		t.Fatalf("CacheDownloaderContainerName = %q, want %q", got, want)
	}
	if got, want := CacheDownloaderJobSuffix, "-download"; got != want {
		t.Fatalf("CacheDownloaderJobSuffix = %q, want %q", got, want)
	}
	if got, want := EnvModelDType, "MODEL_DTYPE"; got != want {
		t.Fatalf("EnvModelDType = %q, want %q", got, want)
	}
}
