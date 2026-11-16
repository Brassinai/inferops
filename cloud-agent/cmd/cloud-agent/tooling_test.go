package main

import (
	"encoding/json"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func toolingPod(args ...any) unstructured.Unstructured {
	return unstructured.Unstructured{Object: map[string]any{
		"spec":   map[string]any{"containers": []any{map[string]any{"name": "runtime", "image": "test:1", "args": args}}},
		"status": map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"name": "runtime", "ready": true, "imageID": "sha256:1"}}},
	}}
}

func TestToolingRequiresExplicitInspectableConfiguration(t *testing.T) {
	for _, test := range []struct {
		name, engine, state string
		args                []any
	}{
		{"engine alone", "llama-cpp", "Unknown", nil},
		{"llama jinja", "llama-cpp", "Enabled", []any{"--jinja"}},
		{"disabled", "llama-cpp", "Disabled", []any{"--no-jinja"}},
		{"conflicting flags", "llama-cpp", "Unknown", []any{"--jinja", "--no-jinja"}},
		{"vllm parser", "vllm", "Enabled", []any{"--enable-auto-tool-choice", "--tool-call-parser", "hermes"}},
		{"vllm missing parser", "vllm", "Unknown", []any{"--enable-auto-tool-choice"}},
		{"unknown engine", "custom-name", "Unknown", []any{"--jinja"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := runtimeTooling(test.engine, []unstructured.Unstructured{toolingPod(test.args...)})
			if got.State != test.state {
				t.Fatalf("tooling = %+v, expected %s", got, test.state)
			}
		})
	}
}

func TestToolingRejectsUnreadySecretSourcedAndCustomCommands(t *testing.T) {
	for _, field := range []string{"unready", "command", "envFrom", "env"} {
		t.Run(field, func(t *testing.T) {
			pod := toolingPod("--jinja")
			container := firstNestedSlice(toolingObject(pod.Object, "spec"), "containers")[0].(map[string]any)
			switch field {
			case "unready":
				firstNestedSlice(toolingObject(pod.Object, "status"), "containerStatuses")[0].(map[string]any)["ready"] = false
			case "command":
				container["command"] = []any{"custom-wrapper"}
			case "envFrom":
				container["envFrom"] = []any{map[string]any{"secretRef": map[string]any{"name": "private-canary"}}}
			case "env":
				container["env"] = []any{map[string]any{"name": "TOOL_CALLING_ENABLED", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": "private-canary"}}}}
			}
			if got := runtimeTooling("llama-cpp", []unstructured.Unstructured{pod}); got.State != "Unknown" {
				t.Fatalf("uninspectable configuration promoted: %+v", got)
			}
		})
	}
	if got := runtimeTooling("llama-cpp", []unstructured.Unstructured{toolingPod("--jinja"), toolingPod("--no-jinja")}); got.State != "Mixed" {
		t.Fatalf("mixed rollout = %+v", got)
	}
}

func TestCustomRuntimeToolingBindsConfigurationWithoutExposingArguments(t *testing.T) {
	pod := toolingPod("--jinja", "--chat-template-file", "/private/template-canary", "--api-key", "secret-canary")
	items := []offeringSnapshot{{Name: "coder", Runtime: "custom-cpu", Evidence: evidenceSnapshot{RuntimeMetadata: map[string]any{}}}}
	reportRuntimeTooling(items, map[string]runtimeConfig{"custom-cpu": {Engine: "llama-cpp"}}, map[string][]unstructured.Unstructured{"coder": {pod}})
	got := items[0].Evidence.RuntimeMetadata["tooling"].(runtimeToolingSnapshot)
	if items[0].Runtime != "custom-cpu" || got.State != "Enabled" || got.ConfigurationHash == "" {
		t.Fatalf("custom runtime identity or tooling lost: %+v", got)
	}
	raw, _ := json.Marshal(items[0].Evidence.RuntimeMetadata)
	if strings.Contains(string(raw), "canary") {
		t.Fatal("raw configuration leaked into tooling report")
	}
	changed := runtimeTooling("llama-cpp", []unstructured.Unstructured{toolingPod("--no-jinja")})
	if changed.ConfigurationHash == got.ConfigurationHash {
		t.Fatal("configuration change did not change evidence hash")
	}
}
