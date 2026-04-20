package config

import (
	"testing"

	execconfig "github.com/viant/agently-core/app/executor/config"
	"gopkg.in/yaml.v3"
)

func TestDefaultsWithFallbackMergesAdvancedDefaults(t *testing.T) {
	fallback := &execconfig.Defaults{
		Model:    "fallback-model",
		Embedder: "fallback-embedder",
		Agent:    "fallback-agent",
		PreviewSettings: execconfig.PreviewSettings{
			Limit:          1000,
			AgedLimit:      200,
			AgedAfterSteps: 3,
		},
		ToolCallMaxResults:    2,
		ToolCallTimeoutSec:    5,
		ElicitationTimeoutSec: 10,
	}

	root := &Root{}
	const yamlConfig = `
default:
  model: openai_gpt-5_4
  skills:
    model: openai_gpt-5.4-mini
  previewSettings:
    limit: 8000
    agedLimit: 2500
    agedAfterSteps: 2
  toolCallMaxResults: 7
  toolCallTimeoutSec: 45
  elicitationTimeoutSec: 90
`
	if err := yaml.Unmarshal([]byte(yamlConfig), root); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}

	got := root.DefaultsWithFallback(fallback)
	if got == nil {
		t.Fatalf("DefaultsWithFallback() = nil")
	}

	if got.Model != "openai_gpt-5_4" {
		t.Fatalf("expected merged model, got %q", got.Model)
	}
	if got.Skills.Model != "openai_gpt-5.4-mini" {
		t.Fatalf("expected merged skills model, got %q", got.Skills.Model)
	}
	if got.PreviewSettings.Limit != 8000 {
		t.Fatalf("expected preview limit 8000, got %d", got.PreviewSettings.Limit)
	}
	if got.PreviewSettings.AgedLimit != 2500 {
		t.Fatalf("expected aged limit 2500, got %d", got.PreviewSettings.AgedLimit)
	}
	if got.PreviewSettings.AgedAfterSteps != 2 {
		t.Fatalf("expected agedAfterSteps 2, got %d", got.PreviewSettings.AgedAfterSteps)
	}
	if got.ToolCallMaxResults != 7 {
		t.Fatalf("expected ToolCallMaxResults 7, got %d", got.ToolCallMaxResults)
	}
	if got.ToolCallTimeoutSec != 45 {
		t.Fatalf("expected ToolCallTimeoutSec 45, got %d", got.ToolCallTimeoutSec)
	}
	if got.ElicitationTimeoutSec != 90 {
		t.Fatalf("expected ElicitationTimeoutSec 90, got %d", got.ElicitationTimeoutSec)
	}
}
