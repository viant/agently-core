package agent

import (
	"context"
	"testing"

	"github.com/viant/agently-core/genai/llm"
	ag "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/service/core"
)

func TestEnsureGenerateOptions_AppliesAgentTemperature(t *testing.T) {
	in := &core.GenerateInput{}
	a := &ag.Agent{
		Temperature: 0.7,
	}

	EnsureGenerateOptions(context.Background(), in, a)

	if in.Options == nil {
		t.Fatalf("expected options to be initialized")
	}
	if got := in.Options.Temperature; got != 0.7 {
		t.Fatalf("unexpected temperature: %v", got)
	}
}

func TestEnsureGenerateOptions_DoesNotOverrideRequestTemperature(t *testing.T) {
	in := &core.GenerateInput{
		ModelSelection: llm.ModelSelection{
			Options: &llm.Options{
				Temperature: 0.2,
			},
		},
	}
	a := &ag.Agent{
		Temperature: 0.8,
	}

	EnsureGenerateOptions(context.Background(), in, a)

	if got := in.Options.Temperature; got != 0.2 {
		t.Fatalf("expected existing request temperature to win, got: %v", got)
	}
}

func TestEnsureGenerateOptions_ModelArtifactGeneration(t *testing.T) {
	in := &core.GenerateInput{}
	a := &ag.Agent{
		Capabilities: &ag.Capabilities{
			ModelArtifactGeneration: true,
		},
	}

	EnsureGenerateOptions(context.Background(), in, a)

	if in.Options == nil || in.Options.Metadata == nil {
		t.Fatalf("expected metadata to be initialized")
	}
	got, ok := in.Options.Metadata["modelArtifactGeneration"].(bool)
	if !ok {
		t.Fatalf("expected modelArtifactGeneration metadata flag")
	}
	if !got {
		t.Fatalf("expected modelArtifactGeneration metadata to be true")
	}
}

func TestEnsureGenerateOptions_DefaultsModeToTask(t *testing.T) {
	in := &core.GenerateInput{}
	a := &ag.Agent{}

	EnsureGenerateOptions(context.Background(), in, a)

	if in.Options == nil {
		t.Fatalf("expected options to be initialized")
	}
	if got := in.Options.Mode; got != "task" {
		t.Fatalf("expected default mode task, got %q", got)
	}
}

func TestEnsureGenerateOptions_PreservesExplicitMode(t *testing.T) {
	in := &core.GenerateInput{
		ModelSelection: llm.ModelSelection{
			Options: &llm.Options{
				Mode: "router",
			},
		},
	}
	a := &ag.Agent{}

	EnsureGenerateOptions(context.Background(), in, a)

	if got := in.Options.Mode; got != "router" {
		t.Fatalf("expected explicit mode to win, got %q", got)
	}
}
