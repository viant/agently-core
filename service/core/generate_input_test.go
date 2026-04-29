package core

import (
	"testing"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/binding"
)

func TestGenerateInput_MatchModelIfNeeded_UsesBindingModel(t *testing.T) {
	input := &GenerateInput{
		Binding: &binding.Binding{
			Model: "openai_gpt4o_mini",
		},
	}

	input.MatchModelIfNeeded(nil)

	if input.Model != "openai_gpt4o_mini" {
		t.Fatalf("model = %q, want %q", input.Model, "openai_gpt4o_mini")
	}
}

type stubMatcher struct{ best string }

func (s stubMatcher) Best(_ *llm.ModelPreferences) string { return s.best }

func TestGenerateInput_MatchModelIfNeeded_PreferencesBeatBindingModel(t *testing.T) {
	input := &GenerateInput{
		ModelSelection: llm.ModelSelection{
			Preferences: &llm.ModelPreferences{Hints: []string{"openai_gpt-5.4"}},
		},
		Binding: &binding.Binding{
			Model: "test-model",
		},
	}

	input.MatchModelIfNeeded(stubMatcher{best: "openai_gpt-5.4"})

	if input.Model != "openai_gpt-5.4" {
		t.Fatalf("model = %q, want %q", input.Model, "openai_gpt-5.4")
	}
}
