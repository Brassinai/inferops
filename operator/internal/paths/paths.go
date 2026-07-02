package paths

import (
	"errors"
	"fmt"
	pathpkg "path"
	"strings"
)

// CleanAbsolutePath validates that a path is absolute and clean and returns
// the cleaned path.
func CleanAbsolutePath(path, field string) (string, error) {
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

// UnderRoot returns an error if path is not equal to root or a child of it.
func UnderRoot(path, root, field string) error {
	root = strings.TrimSuffix(root, "/")
	if path == root {
		return nil
	}
	prefix := root + "/"
	if !strings.HasPrefix(path, prefix) {
		return fmt.Errorf("%s %q must be %q or a child of it", field, path, root)
	}
	return nil
}

// ChildOfRoot returns the path relative to root, or an error if path is not
// under root.
func ChildOfRoot(path, root string) (string, error) {
	root = strings.TrimSuffix(root, "/")
	if path == root {
		return "", errors.New("path must not equal the root")
	}
	prefix := root + "/"
	if !strings.HasPrefix(path, prefix) {
		return "", fmt.Errorf("path %q is not under root %q", path, root)
	}
	return strings.TrimPrefix(path, prefix), nil
}
