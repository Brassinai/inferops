package routingpath

import "testing"

func TestNormalize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		prefix  string
		want    string
		wantErr bool
	}{
		{name: "model route", prefix: "/models/qwen", want: "/models/qwen"},
		{name: "single trailing slash", prefix: "/models/qwen/", want: "/models/qwen"},
		{name: "relative", prefix: "models/qwen", wantErr: true},
		{name: "root", prefix: "/", wantErr: true},
		{name: "duplicate slash", prefix: "/models//qwen", wantErr: true},
		{name: "dot segment", prefix: "/models/../qwen", wantErr: true},
		{name: "escaped separator", prefix: "/models/qwen%2fother", wantErr: true},
		{name: "query delimiter", prefix: "/models/qwen?debug", wantErr: true},
		{name: "backslash", prefix: "/models/qwen\\other", wantErr: true},
		{name: "health endpoint", prefix: "/healthz", wantErr: true},
		{name: "readiness subtree", prefix: "/readyz/model", wantErr: true},
		{name: "metrics endpoint", prefix: "/metrics", wantErr: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := Normalize(test.prefix)
			if (err != nil) != test.wantErr {
				t.Fatalf("Normalize(%q) error = %v, wantErr %t", test.prefix, err, test.wantErr)
			}
			if got != test.want {
				t.Errorf("Normalize(%q) = %q, want %q", test.prefix, got, test.want)
			}
		})
	}
}
