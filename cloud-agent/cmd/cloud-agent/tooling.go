package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// runtimeTooling inspects only owner-bound, ready runtime containers. It never
// forwards raw arguments, environment variables, or template file paths.
func runtimeTooling(runtime string, pods []unstructured.Unstructured) runtimeToolingSnapshot {
	result := runtimeToolingSnapshot{State: "Unknown", Source: "ProviderReported"}
	configs := []runtimeToolingSnapshot{}
	for _, object := range pods {
		pod := object.Object
		if firstNestedString(toolingObject(pod, "status"), "phase") != "Running" || toolingObject(pod, "metadata")["deletionTimestamp"] != nil {
			continue
		}
		ready, imageID := false, ""
		for _, item := range firstNestedSlice(toolingObject(pod, "status"), "containerStatuses") {
			status, _ := item.(map[string]any)
			if firstNestedString(status, "name") == "runtime" {
				ready, imageID = status["ready"] == true, firstNestedString(status, "imageID")
			}
		}
		if !ready {
			continue
		}
		for _, item := range firstNestedSlice(toolingObject(pod, "spec"), "containers") {
			container, _ := item.(map[string]any)
			if firstNestedString(container, "name") == "runtime" {
				configs = append(configs, containerTooling(runtime, container, imageID))
			}
		}
	}
	for i, config := range configs {
		if i == 0 {
			result = config
		} else if result != config {
			return runtimeToolingSnapshot{State: "Mixed", Source: "ProviderReported"}
		}
	}
	return result
}

func containerTooling(runtime string, container map[string]any, imageID string) runtimeToolingSnapshot {
	result := runtimeToolingSnapshot{State: "Unknown", Source: "ProviderReported"}
	env := map[string]string{}
	for _, item := range firstNestedSlice(container, "env") {
		entry, _ := item.(map[string]any)
		name := firstNestedString(entry, "name")
		switch name {
		case "TOOL_CALLING_ENABLED", "TOOL_CALL_PARSER", "CHAT_TEMPLATE_FILE", "LLAMA_ARG_JINJA", "LLAMA_ARG_CHAT_TEMPLATE_FILE", "LLAMA_ARG_CHAT_TEMPLATE", "LLAMA_ARG_SKIP_CHAT_PARSING", "LLAMA_ARG_N_PREDICT", "MODEL_FILE", "MAX_MODEL_LEN", "MAX_NUM_SEQS":
			if entry["valueFrom"] != nil {
				return result
			}
			env[name] = firstNestedString(entry, "value")
		}
	}
	// envFrom and custom commands may change the effective engine invocation.
	if len(firstNestedSlice(container, "envFrom")) > 0 || len(firstNestedSlice(container, "command")) > 0 {
		return result
	}
	flags := map[string]string{}
	args := firstNestedSlice(container, "args")
	for i := 0; i < len(args); i++ {
		arg, _ := args[i].(string)
		key, value, assigned := strings.Cut(arg, "=")
		switch key {
		case "--jinja", "--no-jinja", "--enable-auto-tool-choice", "--disable-auto-tool-choice", "--skip-chat-parsing", "--no-skip-chat-parsing":
			if !assigned {
				value = "true"
			}
			flags[key] = value
		case "--tool-call-parser", "--tool-parser-plugin", "--chat-template", "--chat-template-file", "--chat-template-kwargs", "--chat-template-content-format", "--ctx-size", "--max-model-len", "--model", "-m":
			if !assigned && i+1 < len(args) {
				i++
				value, _ = args[i].(string)
			}
			flags[key] = value
		}
	}
	enabled := env["TOOL_CALLING_ENABLED"] == "true" || env["TOOL_CALLING_ENABLED"] == "1"
	// False adds no wrapper flags; only an explicit engine setting establishes
	// Disabled. Upstream arguments/environment may still enable tooling.
	disabled := false
	template := env["CHAT_TEMPLATE_FILE"]
	runtime = strings.ToLower(runtime)
	switch {
	case runtime == "llama-cpp" || runtime == "llama.cpp":
		enabled = enabled || env["LLAMA_ARG_JINJA"] == "true" || env["LLAMA_ARG_JINJA"] == "1" || flags["--jinja"] == "true"
		disabled = disabled || flags["--no-jinja"] == "true" || flags["--jinja"] == "false" || flags["--skip-chat-parsing"] == "true" || env["LLAMA_ARG_SKIP_CHAT_PARSING"] == "1" || env["LLAMA_ARG_SKIP_CHAT_PARSING"] == "true"
		if template == "" {
			// The packaged entrypoint appends CHAT_TEMPLATE_FILE after spec.args.
			template = firstNonEmptyString(flags["--chat-template-file"], flags["--chat-template"], env["LLAMA_ARG_CHAT_TEMPLATE_FILE"], env["LLAMA_ARG_CHAT_TEMPLATE"])
		}
	case runtime == "vllm" || runtime == "vllm-cpu":
		result.Parser = flags["--tool-call-parser"]
		if enabled {
			result.Parser = env["TOOL_CALL_PARSER"]
		}
		enabled = enabled || flags["--enable-auto-tool-choice"] == "true"
		disabled = disabled || flags["--disable-auto-tool-choice"] == "true" || flags["--enable-auto-tool-choice"] == "false"
		if result.Parser == "" {
			enabled = false
		}
		if template == "" {
			template = flags["--chat-template"]
		}
	default:
		return result
	}
	if enabled && !disabled {
		result.State = "Enabled"
	} else if disabled && !enabled {
		result.State = "Disabled"
	}
	if template != "" {
		hash := sha256.Sum256([]byte(template))
		result.TemplateReferenceHash = hex.EncodeToString(hash[:])
	}
	// Bind configuration evidence to images and relevant settings, not pod names
	// or restarts. Approved evidence changes invalidate existing attestations.
	value, _ := json.Marshal([]any{runtime, firstNestedString(container, "image"), imageID, flags, env})
	hash := sha256.Sum256(value)
	result.ConfigurationHash = hex.EncodeToString(hash[:])
	if result.Parser != "" && !toolingParserIdentifier.MatchString(result.Parser) {
		return runtimeToolingSnapshot{State: "Unknown", Source: "ProviderReported"}
	}
	return result
}

// runtimeToolingSnapshot follows InferCloud's provider-reported tooling contract.
// It describes configuration; only cloud operator checks establish compatibility.
type runtimeToolingSnapshot struct {
	State                 string `json:"state"`
	Source                string `json:"source"`
	Parser                string `json:"parser,omitempty"`
	TemplateReferenceHash string `json:"templateReferenceHash,omitempty"`
	ConfigurationHash     string `json:"configurationHash,omitempty"`
}

var toolingParserIdentifier = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

func toolingObject(parent map[string]any, key string) map[string]any {
	value, _ := parent[key].(map[string]any)
	return value
}

func reportRuntimeTooling(offerings []offeringSnapshot, configs map[string]runtimeConfig, pods map[string][]unstructured.Unstructured) {
	for i := range offerings {
		offering := &offerings[i]
		engine := configs[offering.Runtime].Engine
		offering.Evidence.RuntimeMetadata["runtimeEngine"] = engine
		offering.Evidence.RuntimeMetadata["tooling"] = runtimeTooling(engine, pods[offering.Name])
	}
}
