package main

import (
	"reflect"
	"testing"
	"time"

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
		{
			name: "invalid default cache size",
			mutate: func(config *operatorConfig) {
				config.defaultCacheSize = "not-a-quantity"
			},
		},
		{
			name: "invalid GPU type label",
			mutate: func(config *operatorConfig) {
				config.gpuTypeLabel = "not a label"
			},
		},
		{
			name: "invalid webhook port",
			mutate: func(config *operatorConfig) {
				config.webhookEnabled = true
				config.webhookPort = 70000
				config.webhookCertDir = "/tmp/certs"
			},
		},
		{
			name: "missing webhook cert directory",
			mutate: func(config *operatorConfig) {
				config.webhookEnabled = true
				config.webhookPort = 9443
				config.webhookCertDir = ""
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

func TestScalarEnvironmentParsing(t *testing.T) {
	t.Setenv("INFEROPS_TEST_BOOL", "true")
	value, err := boolFromEnv("INFEROPS_TEST_BOOL", false)
	if err != nil || !value {
		t.Fatalf("boolFromEnv() = %t, %v, want true, nil", value, err)
	}
	t.Setenv("INFEROPS_TEST_BOOL", "sometimes")
	if _, err := boolFromEnv("INFEROPS_TEST_BOOL", false); err == nil {
		t.Fatal("boolFromEnv() accepted invalid boolean")
	}

	t.Setenv("INFEROPS_TEST_INT", "9443")
	port, err := intFromEnv("INFEROPS_TEST_INT", 1)
	if err != nil || port != 9443 {
		t.Fatalf("intFromEnv() = %d, %v, want 9443, nil", port, err)
	}
	t.Setenv("INFEROPS_TEST_INT", "not-an-int")
	if _, err := intFromEnv("INFEROPS_TEST_INT", 1); err == nil {
		t.Fatal("intFromEnv() accepted invalid integer")
	}
}

func TestDurationFromEnv(t *testing.T) {
	t.Setenv("INFEROPS_TEST_DURATION", "15s")
	got, err := durationFromEnv("INFEROPS_TEST_DURATION", time.Minute)
	if err != nil {
		t.Fatalf("durationFromEnv() error = %v", err)
	}
	if got != 15*time.Second {
		t.Errorf("duration = %s, want 15s", got)
	}

	t.Setenv("INFEROPS_TEST_DURATION", "0s")
	if _, err := durationFromEnv("INFEROPS_TEST_DURATION", time.Minute); err == nil {
		t.Fatal("durationFromEnv() accepted a non-positive duration")
	}
}
