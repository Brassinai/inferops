package main

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestParseNodeSelector(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		want    map[string]string
		wantErr bool
	}{
		{name: "empty", value: "", want: nil},
		{
			name:  "multiple",
			value: "inferops.dev/cache=true,kubernetes.io/arch=amd64",
			want: map[string]string{
				"inferops.dev/cache": "true",
				"kubernetes.io/arch": "amd64",
			},
		},
		{name: "missing equals", value: "inferops.dev/cache", wantErr: true},
		{name: "empty entry", value: "a=b,", wantErr: true},
		{name: "invalid key", value: "not a key=value", wantErr: true},
		{name: "invalid value", value: "key=not a value", wantErr: true},
		{name: "duplicate key", value: "key=one,key=two", wantErr: true},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseNodeSelector(test.value)
			if (err != nil) != test.wantErr {
				t.Fatalf("parseNodeSelector() error = %v, wantErr %t", err, test.wantErr)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parseNodeSelector() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestParseResourceNames(t *testing.T) {
	t.Parallel()

	got, err := parseResourceNames("nvidia.com/gpu,amd.com/gpu")
	if err != nil {
		t.Fatalf("parseResourceNames() error = %v", err)
	}
	want := []corev1.ResourceName{"nvidia.com/gpu", "amd.com/gpu"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseResourceNames() = %#v, want %#v", got, want)
	}

	for _, value := range []string{"nvidia.com/gpu,", "not a resource", "nvidia.com/gpu,nvidia.com/gpu"} {
		if _, err := parseResourceNames(value); err == nil {
			t.Errorf("parseResourceNames(%q) expected error", value)
		}
	}
}

func TestOperatorConfigValidation(t *testing.T) {
	t.Parallel()

	valid := operatorConfig{
		cacheRoot:               "/var/lib/inferops/models",
		downloaderImage:         "ghcr.io/inferops/model-downloader:v0.1.0",
		cacheCapacityAnnotation: "inferops.dev/cache-capacity",
	}
	if err := valid.validate(); err != nil {
		t.Fatalf("valid config error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*operatorConfig)
	}{
		{
			name: "mutable image",
			mutate: func(config *operatorConfig) {
				config.downloaderImage = "ghcr.io/inferops/model-downloader:latest"
			},
		},
		{
			name: "invalid capacity annotation",
			mutate: func(config *operatorConfig) {
				config.cacheCapacityAnnotation = "not a valid annotation"
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := valid
			test.mutate(&config)
			if err := config.validate(); err == nil {
				t.Fatal("validate() expected error")
			}
		})
	}
}
