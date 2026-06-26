package templates

import "testing"

func TestStableRuntimeNamesAndRoutes(t *testing.T) {
	t.Parallel()

	if got, want := RuntimeContainerName, "runtime"; got != want {
		t.Fatalf("RuntimeContainerName = %q, want %q", got, want)
	}
	if got, want := RuntimeReadinessPath, "/readiness"; got != want {
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
}
