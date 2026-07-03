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
	nameHashLength  = 12
	maxLabelLength  = 63
)

// Identity returns the immutable artifact identity used by deployment-owned
// cache lookup. Namespace and deployment name prevent unrelated tenants from
// accidentally sharing a cache object.
func Identity(deployment *v1alpha1.ModelDeployment) (string, error) {
	if deployment == nil {
		return "", errors.New("model deployment is required")
	}
	if deployment.Namespace == "" || deployment.Name == "" {
		return "", errors.New("model deployment namespace and name are required")
	}
	if deployment.Spec.Model.Repo == "" {
		return "", errors.New("model repository is required")
	}
	revision := deployment.Spec.Model.Revision
	if revision == "" {
		revision = defaultRevision
	}
	return strings.Join([]string{
		deployment.Namespace,
		deployment.Name,
		deployment.Spec.Model.Repo,
		revision,
	}, "\n"), nil
}

// Name returns a deterministic, label-safe ModelCache name.
func Name(deployment *v1alpha1.ModelDeployment) (string, error) {
	identity, err := Identity(deployment)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(identity))
	suffix := "-" + hex.EncodeToString(sum[:])[:nameHashLength] + "-cache"
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
		return "", fmt.Errorf("generated cache name %q is invalid: %s", name, strings.Join(messages, ", "))
	}
	return name, nil
}

// Path returns a deterministic final cache path below the requested root.
func Path(deployment *v1alpha1.ModelDeployment, configuredRoot string) (string, error) {
	name, err := Name(deployment)
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
