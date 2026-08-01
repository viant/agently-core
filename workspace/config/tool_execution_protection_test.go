package config

import (
	"testing"

	execconfig "github.com/viant/agently-core/app/executor/config"
	"gopkg.in/yaml.v3"
)

func TestResolveDefaultsWithFallbackRejectsInvalidProtection(t *testing.T) {
	var root Root
	if err := yaml.Unmarshal([]byte(`default:
  toolExecutionProtection:
    enabled: true
    rules:
      - id: mail
        tool: delivery/*
        mode: atMostOnce
`), &root); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}
	if _, err := root.ResolveDefaultsWithFallback(nil); err == nil {
		t.Fatal("ResolveDefaultsWithFallback() error = nil")
	}
}

func TestResolveDefaultsWithFallbackExplicitDisabledOverridesFallback(t *testing.T) {
	var root Root
	if err := yaml.Unmarshal([]byte("default:\n  toolExecutionProtection:\n    enabled: false\n"), &root); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}
	fallback := &execconfig.Defaults{ToolExecutionProtection: execconfig.ToolExecutionProtectionDefaults{
		Enabled: true,
		Rules:   []execconfig.ToolExecutionProtectionRule{{ID: "id", Tool: "svc/tool", Mode: execconfig.ToolExecutionProtectionModeAtMostOnce}},
	}}
	resolved, err := root.ResolveDefaultsWithFallback(fallback)
	if err != nil {
		t.Fatalf("ResolveDefaultsWithFallback() error = %v", err)
	}
	if resolved.ToolExecutionProtection.Enabled {
		t.Fatal("explicit disabled protection remained enabled")
	}
}

func TestResolveDefaultsWithFallbackDoesNotSwallowProtectionDecodeError(t *testing.T) {
	var root Root
	if err := yaml.Unmarshal([]byte("default:\n  toolExecutionProtection:\n    enabled: [true]\n"), &root); err != nil {
		t.Fatalf("root yaml.Unmarshal() error = %v", err)
	}
	if _, err := root.ResolveDefaultsWithFallback(nil); err == nil {
		t.Fatal("ResolveDefaultsWithFallback() error = nil")
	}
}

func TestDefaultsWithFallbackDecodeErrorPreservesLegacyBuiltinBase(t *testing.T) {
	var root Root
	if err := yaml.Unmarshal([]byte("default:\n  toolExecutionProtection:\n    enabled: [true]\n"), &root); err != nil {
		t.Fatalf("root yaml.Unmarshal() error = %v", err)
	}
	defaults := root.DefaultsWithFallback(nil)
	if defaults.AppName != "Agently" || defaults.AppIconRef != "builtin:viant" || defaults.Model != "openai_gpt-5.2" || defaults.Embedder != "openai_text" || defaults.Agent != "chatter" {
		t.Fatalf("legacy defaults lost after decode error: %#v", defaults)
	}
}

func TestDefaultsWithFallbackDecodeErrorPreservesLegacyExplicitFallbackReplacement(t *testing.T) {
	var root Root
	if err := yaml.Unmarshal([]byte("default:\n  toolExecutionProtection:\n    enabled: [true]\n"), &root); err != nil {
		t.Fatalf("root yaml.Unmarshal() error = %v", err)
	}
	defaults := root.DefaultsWithFallback(&execconfig.Defaults{Model: "custom-model"})
	if defaults.Model != "custom-model" {
		t.Fatalf("legacy fallback model = %q", defaults.Model)
	}
	// Before the strict API existed, a non-nil fallback replaced (rather than
	// merged over) the built-in base. Preserve that behavior in the wrapper.
	if defaults.AppName != "" || defaults.Embedder != "" || defaults.Agent != "" {
		t.Fatalf("legacy explicit fallback was unexpectedly merged: %#v", defaults)
	}
}
