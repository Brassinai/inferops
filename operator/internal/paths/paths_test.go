package paths

import "testing"

func TestCleanAbsolutePath(t *testing.T) {
	t.Parallel()

	if got, err := CleanAbsolutePath("/var/lib/inferops/models", "cache root"); err != nil || got != "/var/lib/inferops/models" {
		t.Fatalf("CleanAbsolutePath() = %q, %v", got, err)
	}
	for _, path := range []string{"", "relative/path", "/var/lib/../models", "/var/lib/models/"} {
		if _, err := CleanAbsolutePath(path, "cache root"); err == nil {
			t.Errorf("CleanAbsolutePath(%q) expected error", path)
		}
	}
}

func TestUnderRootUsesPathBoundary(t *testing.T) {
	t.Parallel()

	root := "/var/lib/inferops/models"
	for _, path := range []string{root, root + "/qwen"} {
		if err := UnderRoot(path, root, "cache path"); err != nil {
			t.Errorf("UnderRoot(%q) error = %v", path, err)
		}
	}
	for _, path := range []string{"/var/lib/inferops/models-escape", "/etc/models"} {
		if err := UnderRoot(path, root, "cache path"); err == nil {
			t.Errorf("UnderRoot(%q) expected error", path)
		}
	}
}

func TestChildOfRoot(t *testing.T) {
	t.Parallel()

	got, err := ChildOfRoot("/var/lib/inferops/models/qwen", "/var/lib/inferops/models")
	if err != nil || got != "qwen" {
		t.Fatalf("ChildOfRoot() = %q, %v", got, err)
	}
	if _, err := ChildOfRoot("/var/lib/inferops/models", "/var/lib/inferops/models"); err == nil {
		t.Fatal("ChildOfRoot() accepted the root itself")
	}
	if _, err := ChildOfRoot("/etc/models", "/var/lib/inferops/models"); err == nil {
		t.Fatal("ChildOfRoot() accepted a path outside the root")
	}
}
