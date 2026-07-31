package cachecontract

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	"github.com/brassinai/inferops/operator/internal/paths"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
)

const (
	defaultRevision = "main"
	defaultSource   = "huggingface"
	hashLength      = 32
	maxLabelLength  = 63

	LabelCacheIdentity = "inferops.dev/cache-identity"
	LabelCacheMode     = "inferops.dev/cache-mode"

	AnnotationCacheSource   = "inferops.dev/cache-source"
	AnnotationCacheRepo     = "inferops.dev/cache-repo"
	AnnotationCacheRevision = "inferops.dev/cache-revision"
	AnnotationCacheRoot     = "inferops.dev/cache-root"

	ModeShared = "shared"
)

// Identity returns the immutable artifact identity used by shared ModelCache
// lookup. Namespace and credential reference are deliberate sharing boundaries;
// deployment name is deliberately excluded so identical artifacts share one
// cache object.
func Identity(deployment *v1alpha1.ModelDeployment, configuredRoot string) (string, error) {
	parts, err := IdentityParts(deployment, configuredRoot)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		"namespace=" + parts.Namespace,
		"source=" + parts.Source,
		"repo=" + parts.Repo,
		"revision=" + parts.Revision,
		"storageType=" + parts.StorageType,
		"cacheRoot=" + parts.CacheRoot,
		"secretRef=" + parts.SecretRef,
	}, "\n"), nil
}

// IdentityParts returns the normalized fields that make up a shared cache
// identity.
func IdentityParts(deployment *v1alpha1.ModelDeployment, configuredRoot string) (Parts, error) {
	if deployment == nil {
		return Parts{}, errors.New("model deployment is required")
	}
	if deployment.Namespace == "" {
		return Parts{}, errors.New("model deployment namespace is required")
	}
	if deployment.Spec.Model.Repo == "" {
		return Parts{}, errors.New("model repository is required")
	}
	source := strings.TrimSpace(deployment.Spec.Model.Source)
	if source == "" {
		source = defaultSource
	}
	root := deployment.Spec.Cache.Path
	if root == "" {
		root = configuredRoot
	}
	cleanRoot, err := paths.CleanAbsolutePath(root, "deployment cache root")
	if err != nil {
		return Parts{}, err
	}
	if cleanRoot == "/" {
		return Parts{}, errors.New("deployment cache root must not be the filesystem root")
	}
	return Parts{
		Namespace:   deployment.Namespace,
		Source:      source,
		Repo:        deployment.Spec.Model.Repo,
		Revision:    Revision(deployment),
		StorageType: "nodeLocal",
		CacheRoot:   cleanRoot,
		SecretRef:   deployment.Spec.Secrets.HuggingFaceTokenSecretName,
	}, nil
}

// Parts contains normalized cache identity fields.
type Parts struct {
	Namespace   string
	Source      string
	Repo        string
	Revision    string
	StorageType string
	CacheRoot   string
	SecretRef   string
}

// Revision returns the effective model revision used by cache identity.
func Revision(deployment *v1alpha1.ModelDeployment) string {
	if deployment == nil || deployment.Spec.Model.Revision == "" {
		return defaultRevision
	}
	return deployment.Spec.Model.Revision
}

// Hash returns the bounded artifact hash used in names, labels, and paths.
func Hash(deployment *v1alpha1.ModelDeployment, configuredRoot string) (string, error) {
	identity, err := Identity(deployment, configuredRoot)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:])[:hashLength], nil
}

// Name returns a deterministic, label-safe ModelCache name.
func Name(deployment *v1alpha1.ModelDeployment, configuredRoot string) (string, error) {
	hash, err := Hash(deployment, configuredRoot)
	if err != nil {
		return "", err
	}
	name := "model-" + hash + "-cache"
	if messages := utilvalidation.IsDNS1123Label(name); len(messages) != 0 {
		return "", fmt.Errorf("generated cache name %q is invalid: %s", name, strings.Join(messages, ", "))
	}
	return name, nil
}

// LegacyName returns the pre-shared-cache deployment-scoped name. It is kept
// only so upgrades can keep existing caches readable during migration.
func LegacyName(deployment *v1alpha1.ModelDeployment) (string, error) {
	if deployment == nil {
		return "", errors.New("model deployment is required")
	}
	if deployment.Namespace == "" || deployment.Name == "" {
		return "", errors.New("model deployment namespace and name are required")
	}
	if deployment.Spec.Model.Repo == "" {
		return "", errors.New("model repository is required")
	}
	identity := strings.Join([]string{
		deployment.Namespace,
		deployment.Name,
		deployment.Spec.Model.Repo,
		Revision(deployment),
	}, "\n")
	sum := sha256.Sum256([]byte(identity))
	suffix := "-" + hex.EncodeToString(sum[:])[:12] + "-cache"
	prefixLength := maxLabelLength - len(suffix)
	prefix := deployment.Name
	if len(prefix) > prefixLength {
		prefix = strings.TrimRight(prefix[:prefixLength], "-.")
	}
	if prefix == "" {
		prefix = "model"
	}
	name := prefix + suffix
	if messages := utilvalidation.IsDNS1123Label(name); len(messages) != 0 {
		return "", fmt.Errorf("generated legacy cache name %q is invalid: %s", name, strings.Join(messages, ", "))
	}
	return name, nil
}

// Path returns a deterministic final cache path below the requested root.
func Path(deployment *v1alpha1.ModelDeployment, configuredRoot string) (string, error) {
	parts, err := IdentityParts(deployment, configuredRoot)
	if err != nil {
		return "", err
	}
	hash, err := Hash(deployment, configuredRoot)
	if err != nil {
		return "", err
	}
	path := filepath.Join(parts.CacheRoot, deployment.Namespace, "artifacts", hash)
	if err := paths.UnderRoot(path, parts.CacheRoot, "deployment cache path"); err != nil {
		return "", err
	}
	return path, nil
}

// LegacyPath returns the pre-shared-cache deployment-scoped path.
func LegacyPath(deployment *v1alpha1.ModelDeployment, configuredRoot string) (string, error) {
	name, err := LegacyName(deployment)
	if err != nil {
		return "", err
	}
	root := deployment.Spec.Cache.Path
	if root == "" {
		root = configuredRoot
	}
	cleanRoot, err := paths.CleanAbsolutePath(root, "deployment cache root")
	if err != nil {
		return "", err
	}
	if cleanRoot == "/" {
		return "", errors.New("deployment cache root must not be the filesystem root")
	}
	path := filepath.Join(cleanRoot, deployment.Namespace, name)
	if err := paths.UnderRoot(path, cleanRoot, "deployment cache path"); err != nil {
		return "", err
	}
	return path, nil
}

// Labels returns low-cardinality labels for a shared artifact cache.
func Labels(deployment *v1alpha1.ModelDeployment, configuredRoot string) (map[string]string, error) {
	hash, err := Hash(deployment, configuredRoot)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		LabelCacheIdentity: hash,
		LabelCacheMode:     ModeShared,
	}, nil
}

// Annotations returns non-secret identity annotations for a shared artifact
// cache. SecretRef is intentionally omitted because it is already present in
// spec.secretRef and should not be duplicated into display metadata.
func Annotations(deployment *v1alpha1.ModelDeployment, configuredRoot string) (map[string]string, error) {
	parts, err := IdentityParts(deployment, configuredRoot)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		AnnotationCacheSource:   parts.Source,
		AnnotationCacheRepo:     parts.Repo,
		AnnotationCacheRevision: parts.Revision,
		AnnotationCacheRoot:     parts.CacheRoot,
	}, nil
}

// Lookup projects only a verified ready cache placement. Callers must not
// consume node or path fields from an unready cache.
func Lookup(cache *v1alpha1.ModelCache) Result {
	if cache == nil || cache.Status.Phase != v1alpha1.ModelCachePhaseReady {
		return Result{}
	}
	if cache.Status.NodeName == "" || cache.Status.Path == "" || cache.Status.Revision == "" {
		return Result{}
	}
	for i := range cache.Status.Conditions {
		condition := cache.Status.Conditions[i]
		if condition.Type == v1alpha1.CacheConditionReady &&
			condition.Status == metav1.ConditionTrue {
			return Result{
				Ready:    true,
				NodeName: cache.Status.NodeName,
				Path:     cache.Status.Path,
				Revision: cache.Status.Revision,
			}
		}
	}
	return Result{}
}

// Result is the cache handoff consumed by runtime reconciliation.
type Result struct {
	Ready    bool
	NodeName string
	Path     string
	Revision string
}
