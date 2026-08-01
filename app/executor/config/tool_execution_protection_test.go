package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestToolExecutionProtectionDefaultsOffWhenOmitted(t *testing.T) {
	var defaults Defaults
	if defaults.ToolExecutionProtection.Enabled {
		t.Fatal("zero-value tool execution protection is enabled")
	}
	if err := yaml.Unmarshal([]byte("model: example\n"), &defaults); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}
	if defaults.ToolExecutionProtection.Enabled || defaults.HasToolExecutionProtection() {
		t.Fatalf("omitted protection was treated as configured: %#v", defaults.ToolExecutionProtection)
	}
}

func TestToolExecutionProtectionValidate(t *testing.T) {
	valid := ToolExecutionProtectionDefaults{Enabled: true, Rules: []ToolExecutionProtectionRule{{
		ID: "delivery", Tool: "delivery/send", Mode: ToolExecutionProtectionModeAtMostOnce,
	}}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []struct {
		name string
		cfg  ToolExecutionProtectionDefaults
	}{
		{name: "empty rules", cfg: ToolExecutionProtectionDefaults{Enabled: true}},
		{name: "duplicate id", cfg: ToolExecutionProtectionDefaults{Enabled: true, Rules: []ToolExecutionProtectionRule{
			{ID: "same", Tool: "one/tool", Mode: ToolExecutionProtectionModeAtMostOnce},
			{ID: "same", Tool: "two/tool", Mode: ToolExecutionProtectionModeAtMostOnce},
		}}},
		{name: "wildcard tool", cfg: ToolExecutionProtectionDefaults{Enabled: true, Rules: []ToolExecutionProtectionRule{{ID: "id", Tool: "svc/*", Mode: ToolExecutionProtectionModeAtMostOnce}}}},
		{name: "unsupported mode", cfg: ToolExecutionProtectionDefaults{Enabled: true, Rules: []ToolExecutionProtectionRule{{ID: "id", Tool: "svc/tool", Mode: "unsupported"}}}},
		{name: "empty explicit fields", cfg: ToolExecutionProtectionDefaults{Enabled: true, Rules: []ToolExecutionProtectionRule{{ID: "id", Tool: "svc/tool", Mode: ToolExecutionProtectionModeAtMostOnce, SemanticArguments: &ToolExecutionSemanticArguments{Fields: []string{}}}}}},
		{name: "star mixed", cfg: ToolExecutionProtectionDefaults{Enabled: true, Rules: []ToolExecutionProtectionRule{{ID: "id", Tool: "svc/tool", Mode: ToolExecutionProtectionModeAtMostOnce, SemanticArguments: &ToolExecutionSemanticArguments{Fields: []string{"*", "body"}}}}}},
		{name: "duplicate field", cfg: ToolExecutionProtectionDefaults{Enabled: true, Rules: []ToolExecutionProtectionRule{{ID: "id", Tool: "svc/tool", Mode: ToolExecutionProtectionModeAtMostOnce, SemanticArguments: &ToolExecutionSemanticArguments{Fields: []string{"body", "body"}}}}}},
		{name: "exclude star", cfg: ToolExecutionProtectionDefaults{Enabled: true, Rules: []ToolExecutionProtectionRule{{ID: "id", Tool: "svc/tool", Mode: ToolExecutionProtectionModeAtMostOnce, SemanticArguments: &ToolExecutionSemanticArguments{ExcludeFields: []string{"*"}}}}}},
		{name: "exclude outside fields", cfg: ToolExecutionProtectionDefaults{Enabled: true, Rules: []ToolExecutionProtectionRule{{ID: "id", Tool: "svc/tool", Mode: ToolExecutionProtectionModeAtMostOnce, SemanticArguments: &ToolExecutionSemanticArguments{Fields: []string{"body"}, ExcludeFields: []string{"subject"}}}}}},
		{name: "empty selection", cfg: ToolExecutionProtectionDefaults{Enabled: true, Rules: []ToolExecutionProtectionRule{{ID: "id", Tool: "svc/tool", Mode: ToolExecutionProtectionModeAtMostOnce, SemanticArguments: &ToolExecutionSemanticArguments{Fields: []string{"body"}, ExcludeFields: []string{"body"}}}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.cfg.Validate(); err == nil {
				t.Fatalf("Validate() error = nil")
			}
		})
	}

	invalidDisabled := tests[3].cfg
	invalidDisabled.Enabled = false
	if err := invalidDisabled.Validate(); err != nil {
		t.Fatalf("disabled Validate() error = %v", err)
	}
}

func TestToolExecutionProtectionStrictYAML(t *testing.T) {
	for _, source := range []string{
		"toolExecutionProtection:\n  enabled: true\n  store: {}\n",
		"toolExecutionProtection:\n  enabled: true\n  rules:\n    - id: id\n      tool: svc/tool\n      mode: atMostOnce\n      unknownOption: true\n",
		"toolExecutionProtection:\n  enabled: true\n  rules:\n    - id: id\n      tool: svc/tool\n      mode: atMostOnce\n      semanticArguments:\n        nestedPaths: [body.value]\n",
	} {
		var defaults Defaults
		if err := yaml.Unmarshal([]byte(source), &defaults); err == nil {
			t.Fatalf("yaml.Unmarshal(%q) error = nil", source)
		}
	}
}

func TestCanonicalProtectedToolNameAliases(t *testing.T) {
	want := "delivery/send"
	for _, alias := range []string{"delivery/send", "delivery:send", "delivery-send"} {
		got, err := CanonicalProtectedToolName(alias)
		if err != nil || got != want {
			t.Fatalf("CanonicalProtectedToolName(%q) = %q, %v; want %q", alias, got, err, want)
		}
	}
}
