package resources

import (
	"reflect"
	"strings"
	"testing"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	"github.com/brassinai/inferops/operator/internal/templates"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	testCacheRoot       = "/var/lib/inferops/models"
	testCachePath       = testCacheRoot + "/qwen-chat"
	testDownloaderImage = "ghcr.io/inferops/model-downloader:v0.1.0"
)

func testBuilder(t *testing.T) Builder {
	t.Helper()
	builder, err := NewBuilder(BuilderOptions{
		CacheRoot:            testCacheRoot,
		CacheDownloaderImage: testDownloaderImage,
	})
	if err != nil {
		t.Fatalf("NewBuilder() error = %v", err)
	}
	return builder
}

func testModelDeployment() *v1alpha1.ModelDeployment {
	return &v1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "qwen-chat",
			Namespace: "default",
			UID:       types.UID("modeldeployment-uid"),
		},
		Spec: v1alpha1.ModelDeploymentSpec{
			Model: v1alpha1.ModelSpec{
				Name:     "qwen-chat",
				Repo:     "Qwen/Qwen2.5-7B-Instruct",
				Source:   "huggingface",
				Revision: "main",
			},
			Runtime: v1alpha1.RuntimeSpec{
				Ref:                  "nano-vllm",
				MaxModelLen:          4096,
				TensorParallelSize:   1,
				GPUMemoryUtilization: 0.85,
				DType:                "float16",
			},
			Resources: v1alpha1.ResourceRequirements{
				CPU:    "4",
				Memory: "16Gi",
				GPU: &v1alpha1.GPUResourceRequest{
					Count:  1,
					Vendor: "nvidia",
				},
			},
			Activation: v1alpha1.ActivationSpec{
				DesiredState: v1alpha1.ActivationDesiredStateActive,
				WhenFull:     v1alpha1.ActivationWhenFullQueue,
				DrainTimeout: "5m",
			},
			Scaling: v1alpha1.ScalingSpec{
				MinReplicas: 1,
				MaxReplicas: 1,
			},
			Cache: v1alpha1.CacheSpec{
				Enabled: true,
				Type:    "nodeLocal",
				Path:    testCacheRoot,
			},
			Secrets: v1alpha1.SecretReferences{
				HuggingFaceTokenSecretName: "hf-token",
			},
		},
	}
}

func testModelRuntime() *v1alpha1.ModelRuntime {
	return &v1alpha1.ModelRuntime{
		ObjectMeta: metav1.ObjectMeta{Name: "nano-vllm"},
		Spec: v1alpha1.ModelRuntimeSpec{
			Engine:        "nano-vllm",
			Protocol:      "openai",
			DefaultImage:  "ghcr.io/inferops/nano-vllm:v0.1.0",
			Port:          8000,
			HealthPath:    "/health",
			ReadinessPath: "/ready",
			Command:       []string{"/opt/inferops/entrypoint.sh"},
			Args:          []string{"--served-by=inferops"},
			Env: map[string]string{
				"Z_RUNTIME_OPTION": "last",
				"A_RUNTIME_OPTION": "first",
			},
		},
	}
}

func testModelCache() *v1alpha1.ModelCache {
	return &v1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "qwen-chat-cache",
			Namespace: "default",
			UID:       types.UID("modelcache-uid"),
		},
		Spec: v1alpha1.ModelCacheSpec{
			ModelRepo: "Qwen/Qwen2.5-7B-Instruct",
			Revision:  "revision-123",
			Storage: v1alpha1.ModelCacheStorage{
				Type:     "nodeLocal",
				Size:     "100Gi",
				NodeName: "gpu-node-1",
				Path:     testCachePath,
			},
			SecretRef: "hf-token",
		},
	}
}

func TestBuilderOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		options BuilderOptions
		wantErr string
	}{
		{
			name: "valid defaults",
			options: BuilderOptions{
				CacheRoot:            testCacheRoot,
				CacheDownloaderImage: testDownloaderImage,
			},
		},
		{
			name: "relative cache root",
			options: BuilderOptions{
				CacheRoot:            "models",
				CacheDownloaderImage: testDownloaderImage,
			},
			wantErr: "must be absolute",
		},
		{
			name: "filesystem root",
			options: BuilderOptions{
				CacheRoot:            "/",
				CacheDownloaderImage: testDownloaderImage,
			},
			wantErr: "must not be the filesystem root",
		},
		{
			name: "implicit latest image",
			options: BuilderOptions{
				CacheRoot:            testCacheRoot,
				CacheDownloaderImage: "ghcr.io/inferops/model-downloader",
			},
			wantErr: "must include a tag or digest",
		},
		{
			name: "explicit latest image",
			options: BuilderOptions{
				CacheRoot:            testCacheRoot,
				CacheDownloaderImage: "ghcr.io/inferops/model-downloader:latest",
			},
			wantErr: "must not use the mutable latest tag",
		},
		{
			name: "relative runtime mount",
			options: BuilderOptions{
				CacheRoot:            testCacheRoot,
				CacheDownloaderImage: testDownloaderImage,
				RuntimeModelPath:     "models/model",
			},
			wantErr: "must be absolute",
		},
		{
			name: "incomplete downloader resources",
			options: BuilderOptions{
				CacheRoot:            testCacheRoot,
				CacheDownloaderImage: testDownloaderImage,
				CacheDownloaderResources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("100m"),
					},
				},
			},
			wantErr: "request and limit are required",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			builder, err := NewBuilder(test.options)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("NewBuilder() error = %v", err)
				}
				if builder.runtimeModelPath != templates.RuntimeModelMountPath {
					t.Fatalf(
						"runtime model path = %q, want %q",
						builder.runtimeModelPath,
						templates.RuntimeModelMountPath,
					)
				}
				assertResourceRequestLimit(
					t,
					builder.cacheDownloaderResources,
					corev1.ResourceCPU,
					defaultCacheDownloaderCPURequest,
					defaultCacheDownloaderCPULimit,
				)
				assertResourceRequestLimit(
					t,
					builder.cacheDownloaderResources,
					corev1.ResourceMemory,
					defaultCacheDownloaderMemoryRequest,
					defaultCacheDownloaderMemoryLimit,
				)
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("NewBuilder() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func TestValidatePinnedImage(t *testing.T) {
	t.Parallel()

	validDigest := "ghcr.io/inferops/runtime@sha256:" + strings.Repeat("a", 64)
	tests := []struct {
		name    string
		image   string
		wantErr bool
	}{
		{name: "tag", image: "ghcr.io/inferops/runtime:v1.2.3"},
		{name: "registry port and tag", image: "registry.example:5000/runtime:v1"},
		{name: "digest", image: validDigest},
		{name: "missing tag", image: "ghcr.io/inferops/runtime", wantErr: true},
		{name: "latest tag", image: "ghcr.io/inferops/runtime:latest", wantErr: true},
		{name: "latest tag case insensitive", image: "ghcr.io/inferops/runtime:LATEST", wantErr: true},
		{name: "empty digest", image: "ghcr.io/inferops/runtime@sha256:", wantErr: true},
		{name: "short digest", image: "ghcr.io/inferops/runtime@sha256:abc123", wantErr: true},
		{name: "non-hex digest", image: "ghcr.io/inferops/runtime@sha256:" + strings.Repeat("z", 64), wantErr: true},
		{name: "non-canonical digest", image: "ghcr.io/inferops/runtime@sha256:" + strings.Repeat("A", 64), wantErr: true},
		{name: "invalid tag", image: "ghcr.io/inferops/runtime:-bad", wantErr: true},
		{name: "whitespace", image: "ghcr.io/inferops/runtime:v1 ", wantErr: true},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := ValidatePinnedImage(test.image)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidatePinnedImage(%q) error = %v, wantErr %v", test.image, err, test.wantErr)
			}
		})
	}
}

func TestBuilderUsesConfiguredCacheDownloaderResources(t *testing.T) {
	t.Parallel()

	configured := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("250m"),
			corev1.ResourceMemory: resource.MustParse("512Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("2"),
			corev1.ResourceMemory: resource.MustParse("2Gi"),
		},
	}
	builder, err := NewBuilder(BuilderOptions{
		CacheRoot:                testCacheRoot,
		CacheDownloaderImage:     testDownloaderImage,
		CacheDownloaderResources: configured,
	})
	if err != nil {
		t.Fatalf("NewBuilder() error = %v", err)
	}

	configured.Requests[corev1.ResourceCPU] = resource.MustParse("500m")
	assertResourceRequestLimit(
		t,
		builder.cacheDownloaderResources,
		corev1.ResourceCPU,
		"250m",
		"2",
	)
	assertResourceRequestLimit(
		t,
		builder.cacheDownloaderResources,
		corev1.ResourceMemory,
		"512Mi",
		"2Gi",
	)
}

func TestBuildersAreDeterministic(t *testing.T) {
	t.Parallel()

	builder := testBuilder(t)
	md := testModelDeployment()
	runtime := testModelRuntime()
	cache := testModelCache()

	tests := []struct {
		name  string
		build func() (any, error)
	}{
		{
			name: "deployment",
			build: func() (any, error) {
				return builder.BuildRuntimeDeployment(md, runtime, "gpu-node-1", testCachePath)
			},
		},
		{
			name: "service",
			build: func() (any, error) {
				return BuildRuntimeService(md, runtime)
			},
		},
		{
			name: "configmap",
			build: func() (any, error) {
				return BuildRuntimeConfigMap(md, runtime)
			},
		},
		{
			name: "cache downloader job",
			build: func() (any, error) {
				return builder.BuildCacheDownloaderJob(cache)
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			first, err := test.build()
			if err != nil {
				t.Fatalf("first build error = %v", err)
			}
			second, err := test.build()
			if err != nil {
				t.Fatalf("second build error = %v", err)
			}
			if !reflect.DeepEqual(first, second) {
				t.Fatalf("builder output differs for identical input:\nfirst: %#v\nsecond: %#v", first, second)
			}
		})
	}
}

func TestBuildRuntimeDeployment(t *testing.T) {
	t.Parallel()

	builder := testBuilder(t)
	md := testModelDeployment()
	runtime := testModelRuntime()
	deployment, err := builder.BuildRuntimeDeployment(md, runtime, "gpu-node-1", testCachePath)
	if err != nil {
		t.Fatalf("BuildRuntimeDeployment() error = %v", err)
	}

	if deployment.Name != templates.RuntimeServiceName(md.Name) {
		t.Errorf("name = %q, want %q", deployment.Name, templates.RuntimeServiceName(md.Name))
	}
	assertOwnerReference(
		t,
		deployment.OwnerReferences,
		"ModelDeployment",
		md.Name,
		md.UID,
	)
	if got, want := *deployment.Spec.Replicas, int32(1); got != want {
		t.Errorf("replicas = %d, want %d", got, want)
	}
	if got, want := deployment.Spec.Strategy.Type, appsv1.RecreateDeploymentStrategyType; got != want {
		t.Errorf("deployment strategy = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(deployment.Spec.Selector.MatchLabels, SelectorLabels(md.Name)) {
		t.Errorf(
			"selector = %#v, want %#v",
			deployment.Spec.Selector.MatchLabels,
			SelectorLabels(md.Name),
		)
	}
	for key, value := range deployment.Spec.Selector.MatchLabels {
		if deployment.Spec.Template.Labels[key] != value {
			t.Errorf("pod label %q = %q, want %q", key, deployment.Spec.Template.Labels[key], value)
		}
	}

	podSpec := deployment.Spec.Template.Spec
	if podSpec.AutomountServiceAccountToken == nil || *podSpec.AutomountServiceAccountToken {
		t.Error("runtime pod must not automount a Kubernetes API token")
	}
	if got, want := *podSpec.TerminationGracePeriodSeconds, int64(330); got != want {
		t.Errorf("termination grace period = %d, want %d", got, want)
	}
	assertRequiredCacheNode(t, podSpec.Affinity, "gpu-node-1")

	container := podSpec.Containers[0]
	if container.SecurityContext == nil ||
		container.SecurityContext.AllowPrivilegeEscalation == nil ||
		*container.SecurityContext.AllowPrivilegeEscalation {
		t.Error("runtime container must disallow privilege escalation")
	}
	if !reflect.DeepEqual(container.Command, runtime.Spec.Command) {
		t.Errorf("command = %#v, want %#v", container.Command, runtime.Spec.Command)
	}
	if !reflect.DeepEqual(container.Args, runtime.Spec.Args) {
		t.Errorf("args = %#v, want %#v", container.Args, runtime.Spec.Args)
	}
	if container.ReadinessProbe.HTTPGet.Path != runtime.Spec.ReadinessPath {
		t.Errorf("readiness path = %q, want %q", container.ReadinessProbe.HTTPGet.Path, runtime.Spec.ReadinessPath)
	}
	if container.LivenessProbe.HTTPGet.Path != runtime.Spec.HealthPath {
		t.Errorf("liveness path = %q, want %q", container.LivenessProbe.HTTPGet.Path, runtime.Spec.HealthPath)
	}

	assertEqualResource(t, container.Resources, corev1.ResourceCPU, "4")
	assertEqualResource(t, container.Resources, corev1.ResourceMemory, "16Gi")
	assertEqualResource(t, container.Resources, corev1.ResourceName("nvidia.com/gpu"), "1")

	if len(podSpec.Volumes) != 1 || podSpec.Volumes[0].HostPath == nil {
		t.Fatalf("runtime cache volume = %#v, want one hostPath volume", podSpec.Volumes)
	}
	if podSpec.Volumes[0].HostPath.Path != testCachePath {
		t.Errorf("host cache path = %q, want %q", podSpec.Volumes[0].HostPath.Path, testCachePath)
	}
	if len(container.VolumeMounts) != 1 {
		t.Fatalf("volume mounts = %#v, want one", container.VolumeMounts)
	}
	if got, want := container.VolumeMounts[0].MountPath, templates.RuntimeModelMountPath; got != want {
		t.Errorf("mount path = %q, want %q", got, want)
	}
	if !container.VolumeMounts[0].ReadOnly {
		t.Error("runtime cache mount must be read-only")
	}

	environment := environmentByName(container.Env)
	if got, want := environment[templates.EnvModelPath], templates.RuntimeModelMountPath; got != want {
		t.Errorf("%s = %q, want %q", templates.EnvModelPath, got, want)
	}
	if _, found := environment["HF_TOKEN"]; found {
		t.Error("runtime pod must not receive HF_TOKEN")
	}
	if got, want := environment[templates.EnvModelDType], md.Spec.Runtime.DType; got != want {
		t.Errorf("%s = %q, want %q", templates.EnvModelDType, got, want)
	}
	assertEnvironmentOrder(t, container.Env, "A_RUNTIME_OPTION", "Z_RUNTIME_OPTION")
}

func TestImagePullPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		image string
		want  corev1.PullPolicy
	}{
		{name: "implicit latest", image: "ghcr.io/inferops/runtime", want: corev1.PullAlways},
		{name: "explicit latest", image: "ghcr.io/inferops/runtime:latest", want: corev1.PullAlways},
		{name: "version tag", image: "ghcr.io/inferops/runtime:v1.2.3", want: corev1.PullIfNotPresent},
		{
			name:  "digest",
			image: "ghcr.io/inferops/runtime@sha256:" + strings.Repeat("a", 64),
			want:  corev1.PullIfNotPresent,
		},
		{name: "registry port", image: "registry.example:5000/runtime", want: corev1.PullAlways},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := imagePullPolicy(test.image); got != test.want {
				t.Fatalf("imagePullPolicy(%q) = %q, want %q", test.image, got, test.want)
			}
		})
	}
}

func TestBuildRuntimeDeploymentVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*v1alpha1.ModelDeployment, *v1alpha1.ModelRuntime)
		check  func(*testing.T, *appsv1.Deployment)
	}{
		{
			name: "inactive has no runtime pods",
			mutate: func(md *v1alpha1.ModelDeployment, _ *v1alpha1.ModelRuntime) {
				md.Spec.Activation.DesiredState = v1alpha1.ActivationDesiredStateInactive
			},
			check: func(t *testing.T, deployment *appsv1.Deployment) {
				if got := *deployment.Spec.Replicas; got != 0 {
					t.Fatalf("replicas = %d, want 0", got)
				}
				if len(deployment.Spec.Template.Spec.Volumes) != 0 {
					t.Fatalf("inactive deployment volumes = %#v, want none", deployment.Spec.Template.Spec.Volumes)
				}
			},
		},
		{
			name: "CPU only omits GPU",
			mutate: func(md *v1alpha1.ModelDeployment, _ *v1alpha1.ModelRuntime) {
				md.Spec.Resources.GPU = nil
				md.Spec.Runtime.TensorParallelSize = 0
				md.Spec.Runtime.GPUMemoryUtilization = 0
			},
			check: func(t *testing.T, deployment *appsv1.Deployment) {
				resources := deployment.Spec.Template.Spec.Containers[0].Resources
				if _, found := resources.Requests["nvidia.com/gpu"]; found {
					t.Fatal("CPU-only deployment has a GPU request")
				}
				if _, found := resources.Limits["nvidia.com/gpu"]; found {
					t.Fatal("CPU-only deployment has a GPU limit")
				}
			},
		},
		{
			name: "runtime image override",
			mutate: func(md *v1alpha1.ModelDeployment, _ *v1alpha1.ModelRuntime) {
				md.Spec.Runtime.Image = "ghcr.io/inferops/custom-runtime:v1.2.3"
			},
			check: func(t *testing.T, deployment *appsv1.Deployment) {
				if got, want := deployment.Spec.Template.Spec.Containers[0].Image, "ghcr.io/inferops/custom-runtime:v1.2.3"; got != want {
					t.Fatalf("image = %q, want %q", got, want)
				}
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			builder := testBuilder(t)
			md := testModelDeployment()
			runtime := testModelRuntime()
			test.mutate(md, runtime)

			cacheNode, cachePath := "gpu-node-1", testCachePath
			if md.Spec.Activation.DesiredState == v1alpha1.ActivationDesiredStateInactive {
				cacheNode, cachePath = "", ""
			}
			deployment, err := builder.BuildRuntimeDeployment(md, runtime, cacheNode, cachePath)
			if err != nil {
				t.Fatalf("BuildRuntimeDeployment() error = %v", err)
			}
			test.check(t, deployment)
		})
	}
}

func TestBuildRuntimeDeploymentRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		cacheNode string
		cachePath string
		mutate    func(*v1alpha1.ModelDeployment, *v1alpha1.ModelRuntime)
		wantErr   string
	}{
		{
			name:      "active without cache",
			cacheNode: "",
			cachePath: "",
			wantErr:   "active deployment requires",
		},
		{
			name:      "node without path",
			cacheNode: "gpu-node-1",
			cachePath: "",
			wantErr:   "must be provided together",
		},
		{
			name:      "cache path escapes root",
			cacheNode: "gpu-node-1",
			cachePath: "/etc/models",
			wantErr:   "must be a child",
		},
		{
			name:      "invalid CPU quantity",
			cacheNode: "gpu-node-1",
			cachePath: testCachePath,
			mutate: func(md *v1alpha1.ModelDeployment, _ *v1alpha1.ModelRuntime) {
				md.Spec.Resources.CPU = "not-a-quantity"
			},
			wantErr: "parse CPU quantity",
		},
		{
			name:      "invalid memory quantity",
			cacheNode: "gpu-node-1",
			cachePath: testCachePath,
			mutate: func(md *v1alpha1.ModelDeployment, _ *v1alpha1.ModelRuntime) {
				md.Spec.Resources.Memory = "lots"
			},
			wantErr: "parse memory quantity",
		},
		{
			name:      "invalid GPU count",
			cacheNode: "gpu-node-1",
			cachePath: testCachePath,
			mutate: func(md *v1alpha1.ModelDeployment, _ *v1alpha1.ModelRuntime) {
				md.Spec.Resources.GPU.Count = 0
			},
			wantErr: "GPU count must be at least 1",
		},
		{
			name:      "reserved runtime environment",
			cacheNode: "gpu-node-1",
			cachePath: testCachePath,
			mutate: func(_ *v1alpha1.ModelDeployment, runtime *v1alpha1.ModelRuntime) {
				runtime.Spec.Env[templates.EnvModelPath] = "/untrusted"
			},
			wantErr: "managed by InferOps",
		},
		{
			name:      "invalid drain timeout",
			cacheNode: "gpu-node-1",
			cachePath: testCachePath,
			mutate: func(md *v1alpha1.ModelDeployment, _ *v1alpha1.ModelRuntime) {
				md.Spec.Activation.DrainTimeout = "tomorrow"
			},
			wantErr: "parse drain timeout",
		},
		{
			name:      "active with zero maximum replicas",
			cacheNode: "gpu-node-1",
			cachePath: testCachePath,
			mutate: func(md *v1alpha1.ModelDeployment, _ *v1alpha1.ModelRuntime) {
				md.Spec.Scaling.MinReplicas = 0
				md.Spec.Scaling.MaxReplicas = 0
			},
			wantErr: "maximum replicas of at least 1",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			builder := testBuilder(t)
			md := testModelDeployment()
			runtime := testModelRuntime()
			if test.mutate != nil {
				test.mutate(md, runtime)
			}
			_, err := builder.BuildRuntimeDeployment(md, runtime, test.cacheNode, test.cachePath)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("BuildRuntimeDeployment() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func TestBuildRuntimeServiceAndConfigMap(t *testing.T) {
	t.Parallel()

	md := testModelDeployment()
	md.Spec.Routing.Path = "/custom/qwen"
	runtime := testModelRuntime()
	service, err := BuildRuntimeService(md, runtime)
	if err != nil {
		t.Fatalf("BuildRuntimeService() error = %v", err)
	}
	configMap, err := BuildRuntimeConfigMap(md, runtime)
	if err != nil {
		t.Fatalf("BuildRuntimeConfigMap() error = %v", err)
	}

	if service.Name != templates.RuntimeServiceName(md.Name) {
		t.Errorf("service name = %q, want %q", service.Name, templates.RuntimeServiceName(md.Name))
	}
	if service.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Errorf("service type = %q, want %q", service.Spec.Type, corev1.ServiceTypeClusterIP)
	}
	if !reflect.DeepEqual(service.Spec.Selector, SelectorLabels(md.Name)) {
		t.Errorf("service selector = %#v, want %#v", service.Spec.Selector, SelectorLabels(md.Name))
	}
	if got, want := service.Spec.Ports[0].TargetPort.StrVal, templates.HTTPPortName; got != want {
		t.Errorf("target port = %q, want %q", got, want)
	}
	assertOwnerReference(t, service.OwnerReferences, "ModelDeployment", md.Name, md.UID)
	assertOwnerReference(t, configMap.OwnerReferences, "ModelDeployment", md.Name, md.UID)
	if got, want := configMap.Data["route.path"], md.Spec.Routing.Path; got != want {
		t.Errorf("ConfigMap route.path = %q, want %q", got, want)
	}
	if _, found := configMap.Data["routing.customPath"]; found {
		t.Error("ConfigMap contains redundant routing.customPath")
	}
	if _, found := configMap.Data["token"]; found {
		t.Fatal("ConfigMap contains token data")
	}
	for _, value := range configMap.Data {
		if value == md.Spec.Secrets.HuggingFaceTokenSecretName {
			t.Fatal("ConfigMap contains a Secret reference")
		}
	}
}

func TestBuildCacheDownloaderJob(t *testing.T) {
	t.Parallel()

	builder := testBuilder(t)
	cache := testModelCache()
	job, err := builder.BuildCacheDownloaderJob(cache)
	if err != nil {
		t.Fatalf("BuildCacheDownloaderJob() error = %v", err)
	}

	if got, want := job.Name, cache.Name+templates.CacheDownloaderJobSuffix; got != want {
		t.Errorf("job name = %q, want %q", got, want)
	}
	assertOwnerReference(t, job.OwnerReferences, "ModelCache", cache.Name, cache.UID)
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 3 {
		t.Errorf("backoff limit = %v, want 3", job.Spec.BackoffLimit)
	}

	podSpec := job.Spec.Template.Spec
	if podSpec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("restart policy = %q, want %q", podSpec.RestartPolicy, corev1.RestartPolicyNever)
	}
	if podSpec.AutomountServiceAccountToken == nil || *podSpec.AutomountServiceAccountToken {
		t.Error("downloader pod must not automount a Kubernetes API token")
	}
	if got, want := podSpec.NodeSelector[corev1.LabelHostname], cache.Spec.Storage.NodeName; got != want {
		t.Errorf("cache node = %q, want %q", got, want)
	}

	container := podSpec.Containers[0]
	if container.Image != testDownloaderImage {
		t.Errorf("downloader image = %q, want %q", container.Image, testDownloaderImage)
	}
	assertResourceRequestLimit(
		t,
		container.Resources,
		corev1.ResourceCPU,
		defaultCacheDownloaderCPURequest,
		defaultCacheDownloaderCPULimit,
	)
	assertResourceRequestLimit(
		t,
		container.Resources,
		corev1.ResourceMemory,
		defaultCacheDownloaderMemoryRequest,
		defaultCacheDownloaderMemoryLimit,
	)
	wantCommand := []string{
		"hf",
		"download",
		cache.Spec.ModelRepo,
		"--revision",
		cache.Spec.Revision,
		"--local-dir",
		templates.RuntimeModelMountPath,
	}
	if !reflect.DeepEqual(container.Command, wantCommand) {
		t.Errorf("command = %#v, want %#v", container.Command, wantCommand)
	}
	token := environmentVariable(container.Env, "HF_TOKEN")
	if token == nil || token.ValueFrom == nil || token.ValueFrom.SecretKeyRef == nil {
		t.Fatal("HF_TOKEN must use a SecretKeyRef")
	}
	if token.Value != "" {
		t.Fatal("HF_TOKEN must not contain a literal value")
	}
	if got, want := token.ValueFrom.SecretKeyRef.Name, cache.Spec.SecretRef; got != want {
		t.Errorf("Secret name = %q, want %q", got, want)
	}
	if podSpec.Volumes[0].HostPath.Path != cache.Spec.Storage.Path {
		t.Errorf("host cache path = %q, want %q", podSpec.Volumes[0].HostPath.Path, cache.Spec.Storage.Path)
	}
	if container.VolumeMounts[0].ReadOnly {
		t.Error("downloader cache mount must be writable")
	}
}

func TestBuildCacheDownloaderJobRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cache   *v1alpha1.ModelCache
		mutate  func(*v1alpha1.ModelCache)
		wantErr string
	}{
		{name: "nil cache", cache: nil, wantErr: "model cache is required"},
		{
			name:    "missing repository",
			cache:   testModelCache(),
			mutate:  func(cache *v1alpha1.ModelCache) { cache.Spec.ModelRepo = "" },
			wantErr: "repository is required",
		},
		{
			name:    "missing node",
			cache:   testModelCache(),
			mutate:  func(cache *v1alpha1.ModelCache) { cache.Spec.Storage.NodeName = "" },
			wantErr: "node is required",
		},
		{
			name:    "path escapes root",
			cache:   testModelCache(),
			mutate:  func(cache *v1alpha1.ModelCache) { cache.Spec.Storage.Path = "/tmp/model" },
			wantErr: "must be a child",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if test.mutate != nil {
				test.mutate(test.cache)
			}
			_, err := testBuilder(t).BuildCacheDownloaderJob(test.cache)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("BuildCacheDownloaderJob() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func TestCacheVolumeAndNodeAffinityHelpers(t *testing.T) {
	t.Parallel()

	builder := testBuilder(t)
	volume, mount, err := builder.CacheVolumeAndMount(testCachePath, true)
	if err != nil {
		t.Fatalf("CacheVolumeAndMount() error = %v", err)
	}
	if volume.HostPath == nil || volume.HostPath.Path != testCachePath {
		t.Errorf("volume hostPath = %#v, want %q", volume.HostPath, testCachePath)
	}
	if got, want := mount.MountPath, templates.RuntimeModelMountPath; got != want {
		t.Errorf("mount path = %q, want %q", got, want)
	}
	if !mount.ReadOnly {
		t.Error("mount must be read-only")
	}
	if _, _, err := builder.CacheVolumeAndMount("/etc/models", true); err == nil {
		t.Fatal("CacheVolumeAndMount() accepted a path outside the configured root")
	}

	assertRequiredCacheNode(t, &corev1.Affinity{
		NodeAffinity: NodeAffinityForCacheNode("gpu-node-1"),
	}, "gpu-node-1")
	if affinity := NodeAffinityForCacheNode(""); affinity != nil {
		t.Fatalf("NodeAffinityForCacheNode(\"\") = %#v, want nil", affinity)
	}
}

func assertOwnerReference(
	t *testing.T,
	references []metav1.OwnerReference,
	kind string,
	name string,
	uid types.UID,
) {
	t.Helper()
	if len(references) != 1 {
		t.Fatalf("owner references = %#v, want one", references)
	}
	reference := references[0]
	if got, want := reference.APIVersion, v1alpha1.GroupVersion.String(); got != want {
		t.Errorf("owner API version = %q, want %q", got, want)
	}
	if reference.Kind != kind || reference.Name != name || reference.UID != uid {
		t.Errorf(
			"owner = %s %s %s, want %s %s %s",
			reference.Kind,
			reference.Name,
			reference.UID,
			kind,
			name,
			uid,
		)
	}
	if reference.Controller == nil || !*reference.Controller {
		t.Error("owner reference must be controlling")
	}
	if reference.BlockOwnerDeletion == nil || !*reference.BlockOwnerDeletion {
		t.Error("owner reference must block owner deletion")
	}
}

func assertRequiredCacheNode(t *testing.T, affinity *corev1.Affinity, nodeName string) {
	t.Helper()
	if affinity == nil || affinity.NodeAffinity == nil ||
		affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		t.Fatalf("affinity = %#v, want required node affinity", affinity)
	}
	terms := affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	if len(terms) != 1 || len(terms[0].MatchExpressions) != 1 {
		t.Fatalf("node selector terms = %#v, want one expression", terms)
	}
	expression := terms[0].MatchExpressions[0]
	if expression.Key != corev1.LabelHostname ||
		expression.Operator != corev1.NodeSelectorOpIn ||
		!reflect.DeepEqual(expression.Values, []string{nodeName}) {
		t.Errorf("node affinity expression = %#v, want hostname In [%s]", expression, nodeName)
	}
}

func assertEqualResource(
	t *testing.T,
	resources corev1.ResourceRequirements,
	name corev1.ResourceName,
	want string,
) {
	t.Helper()
	request, requestFound := resources.Requests[name]
	limit, limitFound := resources.Limits[name]
	if !requestFound || !limitFound {
		t.Fatalf("resource %q request/limit missing: %#v", name, resources)
	}
	if request.Cmp(limit) != 0 {
		t.Errorf("resource %q request %s does not equal limit %s", name, request.String(), limit.String())
	}
	if request.String() != want {
		t.Errorf("resource %q = %s, want %s", name, request.String(), want)
	}
}

func assertResourceRequestLimit(
	t *testing.T,
	resources corev1.ResourceRequirements,
	name corev1.ResourceName,
	wantRequest string,
	wantLimit string,
) {
	t.Helper()
	request, requestFound := resources.Requests[name]
	limit, limitFound := resources.Limits[name]
	if !requestFound || !limitFound {
		t.Fatalf("resource %q request/limit missing: %#v", name, resources)
	}
	if got := request.String(); got != wantRequest {
		t.Errorf("resource %q request = %s, want %s", name, got, wantRequest)
	}
	if got := limit.String(); got != wantLimit {
		t.Errorf("resource %q limit = %s, want %s", name, got, wantLimit)
	}
}

func environmentByName(environment []corev1.EnvVar) map[string]string {
	result := make(map[string]string, len(environment))
	for _, variable := range environment {
		result[variable.Name] = variable.Value
	}
	return result
}

func environmentVariable(environment []corev1.EnvVar, name string) *corev1.EnvVar {
	for index := range environment {
		if environment[index].Name == name {
			return &environment[index]
		}
	}
	return nil
}

func assertEnvironmentOrder(t *testing.T, environment []corev1.EnvVar, first, second string) {
	t.Helper()
	firstIndex, secondIndex := -1, -1
	for index := range environment {
		switch environment[index].Name {
		case first:
			firstIndex = index
		case second:
			secondIndex = index
		}
	}
	if firstIndex == -1 || secondIndex == -1 || firstIndex >= secondIndex {
		t.Errorf("%q and %q are not in deterministic sorted order: %#v", first, second, environment)
	}
}
