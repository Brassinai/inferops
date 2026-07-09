// Package routingpath validates stable gateway route prefixes shared by the
// API validator and data plane.
package routingpath

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

var reservedPrefixes = []string{"/healthz", "/readyz", "/metrics"}

// DefaultModelPrefix is the stable route root for ModelDeployments.
const DefaultModelPrefix = "/models/"

// DefaultModelRoute returns the stable route for a ModelDeployment name.
func DefaultModelRoute(modelDeploymentName string) string {
	return DefaultModelPrefix + modelDeploymentName
}

// Normalize validates prefix and removes one trailing slash. Route prefixes
// must already be canonical so the Kubernetes API, gateway, and upstream
// cannot interpret the same value differently.
func Normalize(prefix string) (string, error) {
	if prefix == "" {
		return "", errors.New("route prefix is required")
	}
	if !strings.HasPrefix(prefix, "/") {
		return "", errors.New("route prefix must start with /")
	}
	if strings.ContainsAny(prefix, "?#\\%") ||
		strings.IndexFunc(prefix, func(character rune) bool {
			return character < ' ' || character == '\u007f'
		}) >= 0 {
		return "", errors.New("route prefix contains a reserved or control character")
	}

	normalized := strings.TrimSuffix(prefix, "/")
	if normalized == "" || normalized == "/" || path.Clean(prefix) != normalized {
		return "", errors.New("route prefix must be a canonical non-root path")
	}
	for _, reserved := range reservedPrefixes {
		if normalized == reserved || strings.HasPrefix(normalized, reserved+"/") {
			return "", fmt.Errorf("route prefix conflicts with reserved endpoint %q", reserved)
		}
	}
	return normalized, nil
}
