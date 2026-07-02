package resources

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/brassinai/inferops/operator/internal/paths"
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
	cacheRoot, err := paths.CleanAbsolutePath(options.CacheRoot, "cache root")
	if err != nil {
		return Builder{}, err
	}
	if cacheRoot == "/" {
		return Builder{}, errors.New("cache root must not be the filesystem root")
	}
	if err := ValidatePinnedImage(options.CacheDownloaderImage); err != nil {
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
	runtimeModelPath, err = paths.CleanAbsolutePath(runtimeModelPath, "runtime model path")
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
	cleanPath, err := paths.CleanAbsolutePath(cachePath, "cache path")
	if err != nil {
		return err
	}
	return paths.UnderRoot(cleanPath, b.cacheRoot, "cache path")
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

// ValidatePinnedImage returns an error if an image reference does not include
// a tag or digest, or if it uses the mutable :latest tag.
func ValidatePinnedImage(image string) error {
	if image == "" {
		return errors.New("is required")
	}
	if strings.TrimSpace(image) != image || strings.ContainsAny(image, " \t\r\n") {
		return fmt.Errorf("%q must not contain whitespace", image)
	}

	lastSlash := strings.LastIndex(image, "/")
	lastAt := strings.LastIndex(image, "@")
	if lastAt > lastSlash {
		if strings.Count(image[lastSlash+1:], "@") != 1 || lastAt == len(image)-1 {
			return fmt.Errorf("%q has an invalid digest", image)
		}
		if image[:lastAt] == "" {
			return fmt.Errorf("%q must include an image name", image)
		}
		digest := image[lastAt+1:]
		const sha256Prefix = "sha256:"
		if !strings.HasPrefix(digest, sha256Prefix) {
			return fmt.Errorf("%q must use a sha256 digest", image)
		}
		encoded := strings.TrimPrefix(digest, sha256Prefix)
		if len(encoded) != 64 {
			return fmt.Errorf("%q has an invalid sha256 digest", image)
		}
		if encoded != strings.ToLower(encoded) {
			return fmt.Errorf("%q has a non-canonical sha256 digest", image)
		}
		if _, err := hex.DecodeString(encoded); err != nil {
			return fmt.Errorf("%q has an invalid sha256 digest: %w", image, err)
		}
		return nil
	}

	lastColon := strings.LastIndex(image, ":")
	hasTag := lastColon > lastSlash && lastColon < len(image)-1
	if !hasTag {
		return fmt.Errorf("%q must include a tag or digest", image)
	}
	if lastColon == 0 {
		return fmt.Errorf("%q must include an image name", image)
	}
	tag := image[lastColon+1:]
	if strings.EqualFold(tag, "latest") {
		return fmt.Errorf("%q must not use the mutable latest tag", image)
	}
	if !validImageTag(tag) {
		return fmt.Errorf("%q has an invalid tag", image)
	}
	return nil
}

func validImageTag(tag string) bool {
	if len(tag) == 0 || len(tag) > 128 {
		return false
	}
	for i, r := range tag {
		if r >= 'a' && r <= 'z' ||
			r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' ||
			r == '_' ||
			i > 0 && (r == '.' || r == '-') {
			continue
		}
		return false
	}
	return true
}
