package core

import (
	"testing"

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
