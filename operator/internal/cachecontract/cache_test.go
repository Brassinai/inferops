package cachecontract

import (
	"strings"
	"testing"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNameAndPathAreDeterministicAndArtifactScoped(t *testing.T) {
	t.Parallel()

	deployment := &v1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      strings.Repeat("model-", 12),
			Namespace: "tenant-a",
		},
		Spec: v1alpha1.ModelDeploymentSpec{
			Model: v1alpha1.ModelSpec{Repo: "org/model", Revision: "v1"},
			Cache: v1alpha1.CacheSpec{Path: "/var/lib/inferops/models"},
		},
	}
	first, err := Name(deployment, "/ignored")
	if err != nil {
		t.Fatalf("Name() error = %v", err)
	}
	second, err := Name(deployment, "/ignored")
	if err != nil {
		t.Fatalf("Name() second error = %v", err)
	}
	if first != second {
		t.Fatalf("Name() is not deterministic: %q != %q", first, second)
	}
	if len(first) > 63 {
		t.Fatalf("name length = %d, want <= 63", len(first))
	}

	path, err := Path(deployment, "/ignored")
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	if !strings.HasPrefix(path, "/var/lib/inferops/models/tenant-a/") {
		t.Errorf("path = %q, want deployment cache root and namespace", path)
	}
	if !strings.Contains(path, "/artifacts/") {
		t.Errorf("path = %q, want artifact-scoped path", path)
	}

	sameArtifact := deployment.DeepCopy()
	sameArtifact.Name = "other-deployment"
	sameName, err := Name(sameArtifact, "/ignored")
	if err != nil {
		t.Fatalf("Name() same artifact error = %v", err)
	}
	if sameName != first {
		t.Fatalf("same artifact cache name = %q, want shared %q", sameName, first)
	}

	deployment.Spec.Model.Revision = "v2"
	changed, err := Name(deployment, "/ignored")
	if err != nil {
		t.Fatalf("Name() changed revision error = %v", err)
	}
	if changed == first {
		t.Error("cache name did not change with revision")
	}
}

func TestIdentityBoundaries(t *testing.T) {
	t.Parallel()

	base := &v1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "model-a", Namespace: "tenant-a"},
		Spec: v1alpha1.ModelDeploymentSpec{
			Model: v1alpha1.ModelSpec{Repo: "org/model"},
			Cache: v1alpha1.CacheSpec{Path: "/cache"},
		},
	}
	baseName, err := Name(base, "/ignored")
	if err != nil {
		t.Fatalf("Name() base error = %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*v1alpha1.ModelDeployment)
	}{
		{name: "namespace", mutate: func(d *v1alpha1.ModelDeployment) { d.Namespace = "tenant-b" }},
		{name: "repo", mutate: func(d *v1alpha1.ModelDeployment) { d.Spec.Model.Repo = "org/other" }},
		{name: "revision", mutate: func(d *v1alpha1.ModelDeployment) { d.Spec.Model.Revision = "v2" }},
		{name: "source", mutate: func(d *v1alpha1.ModelDeployment) { d.Spec.Model.Source = "s3" }},
		{name: "root", mutate: func(d *v1alpha1.ModelDeployment) { d.Spec.Cache.Path = "/other-cache" }},
		{name: "secret", mutate: func(d *v1alpha1.ModelDeployment) {
			d.Spec.Secrets.HuggingFaceTokenSecretName = "hf-private"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deployment := base.DeepCopy()
			test.mutate(deployment)
			name, err := Name(deployment, "/ignored")
			if err != nil {
				t.Fatalf("Name() error = %v", err)
			}
			if name == baseName {
				t.Fatalf("cache name did not change across %s boundary", test.name)
			}
		})
	}
}

func TestLookupRejectsPartialReadyStatus(t *testing.T) {
	t.Parallel()

	cache := &v1alpha1.ModelCache{
		Status: v1alpha1.ModelCacheStatus{
			Phase:    v1alpha1.ModelCachePhaseReady,
			NodeName: "node-a",
			Path:     "/cache/model",
			Revision: "main",
		},
	}
	if Lookup(cache).Ready {
		t.Fatal("Lookup() accepted cache without Ready condition")
	}
	cache.Status.Conditions = []v1alpha1.Condition{{
		Type:   v1alpha1.CacheConditionReady,
		Status: metav1.ConditionTrue,
	}}
	if result := Lookup(cache); !result.Ready || result.NodeName != "node-a" {
		t.Fatalf("Lookup() = %#v, want ready node-a", result)
	}
}
