package resources

import (
	"errors"
	"fmt"
	pathpkg "path"
	"strings"

	"github.com/brassinai/inferops/operator/internal/templates"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
)

const (
	defaultCacheDownloaderCPURequest    = "100m"
	defaultCacheDownloaderCPULimit      = "1"
	defaultCacheDownloaderMemoryRequest = "256Mi"
	defaultCacheDownloaderMemoryLimit   = "1Gi"
)

// BuilderOptions configures trusted values that must not come from workload
// custom resources.
type BuilderOptions struct {
	CacheRoot                string
	CacheDownloaderImage     string
	CacheDownloaderResources corev1.ResourceRequirements
	RuntimeModelPath         string
}

// Builder creates deterministic Kubernetes resources for InferOps custom resources.
type Builder struct {
	cacheRoot                string
	cacheDownloaderImage     string
	cacheDownloaderResources corev1.ResourceRequirements
	runtimeModelPath         string
}

// NewBuilder creates a resource builder with validated operator configuration.
func NewBuilder(options BuilderOptions) (Builder, error) {
	cacheRoot, err := cleanAbsolutePath(options.CacheRoot, "cache root")
	if err != nil {
		return Builder{}, err
	}
	if cacheRoot == "/" {
		return Builder{}, errors.New("cache root must not be the filesystem root")
	}
	if err := validatePinnedImage(options.CacheDownloaderImage); err != nil {
		return Builder{}, fmt.Errorf("cache downloader image: %w", err)
	}
	cacheDownloaderResources, err := validatedCacheDownloaderResources(options.CacheDownloaderResources)
	if err != nil {
		return Builder{}, fmt.Errorf("cache downloader resources: %w", err)
	}

	runtimeModelPath := options.RuntimeModelPath
	if runtimeModelPath == "" {
		runtimeModelPath = templates.RuntimeModelMountPath
	}
	runtimeModelPath, err = cleanAbsolutePath(runtimeModelPath, "runtime model path")
	if err != nil {
		return Builder{}, err
	}
	if runtimeModelPath == "/" {
		return Builder{}, errors.New("runtime model path must not be the filesystem root")
	}

	return Builder{
		cacheRoot:                cacheRoot,
		cacheDownloaderImage:     options.CacheDownloaderImage,
		cacheDownloaderResources: cacheDownloaderResources,
		runtimeModelPath:         runtimeModelPath,
	}, nil
}

func validatedCacheDownloaderResources(input corev1.ResourceRequirements) (corev1.ResourceRequirements, error) {
	if len(input.Requests) == 0 && len(input.Limits) == 0 {
		return corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(defaultCacheDownloaderCPURequest),
				corev1.ResourceMemory: resource.MustParse(defaultCacheDownloaderMemoryRequest),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(defaultCacheDownloaderCPULimit),
				corev1.ResourceMemory: resource.MustParse(defaultCacheDownloaderMemoryLimit),
			},
		}, nil
	}

	for _, name := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
		request, requestFound := input.Requests[name]
		limit, limitFound := input.Limits[name]
		if !requestFound || !limitFound {
			return corev1.ResourceRequirements{}, fmt.Errorf("%s request and limit are required", name)
		}
		if request.Sign() <= 0 || limit.Sign() <= 0 {
			return corev1.ResourceRequirements{}, fmt.Errorf("%s request and limit must be greater than zero", name)
		}
		if request.Cmp(limit) > 0 {
			return corev1.ResourceRequirements{}, fmt.Errorf("%s request must not exceed limit", name)
		}
	}

	return *input.DeepCopy(), nil
}

func (b Builder) validateCachePath(cachePath string) error {
	cleanPath, err := cleanAbsolutePath(cachePath, "cache path")
	if err != nil {
		return err
	}

	cachePrefix := strings.TrimSuffix(b.cacheRoot, "/") + "/"
	if !strings.HasPrefix(cleanPath, cachePrefix) {
		return fmt.Errorf("cache path %q must be a child of configured root %q", cleanPath, b.cacheRoot)
	}
	return nil
}

func cleanAbsolutePath(path, field string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	if !pathpkg.IsAbs(path) {
		return "", fmt.Errorf("%s %q must be absolute", field, path)
	}
	cleanPath := pathpkg.Clean(path)
	if cleanPath != path {
		return "", fmt.Errorf("%s %q must be clean", field, path)
	}
	return cleanPath, nil
}

func validateModelDeploymentName(name string) error {
	if name == "" {
		return errors.New("model deployment name is required")
	}
	runtimeName := templates.RuntimeServiceName(name)
	if messages := utilvalidation.IsDNS1035Label(runtimeName); len(messages) != 0 {
		return fmt.Errorf("runtime resource name %q is invalid: %s", runtimeName, strings.Join(messages, ", "))
	}
	if messages := utilvalidation.IsValidLabelValue(name); len(messages) != 0 {
		return fmt.Errorf("model deployment label value %q is invalid: %s", name, strings.Join(messages, ", "))
	}
	return nil
}

func validateModelCacheName(name string) error {
	if name == "" {
		return errors.New("model cache name is required")
	}
	jobName := name + templates.CacheDownloaderJobSuffix
	if messages := utilvalidation.IsDNS1123Subdomain(jobName); len(messages) != 0 {
		return fmt.Errorf("cache download Job name %q is invalid: %s", jobName, strings.Join(messages, ", "))
	}
	if messages := utilvalidation.IsValidLabelValue(name); len(messages) != 0 {
		return fmt.Errorf("model cache label value %q is invalid: %s", name, strings.Join(messages, ", "))
	}
	return nil
}

func validatePinnedImage(image string) error {
	if image == "" {
		return errors.New("is required")
	}

	lastSlash := strings.LastIndex(image, "/")
	lastColon := strings.LastIndex(image, ":")
	hasTag := lastColon > lastSlash && lastColon < len(image)-1
	hasDigest := strings.Contains(image[lastSlash+1:], "@sha256:")
	if !hasTag && !hasDigest {
		return fmt.Errorf("%q must include a tag or digest", image)
	}
	if strings.HasSuffix(image, ":latest") {
		return fmt.Errorf("%q must not use the mutable latest tag", image)
	}
	return nil
}
