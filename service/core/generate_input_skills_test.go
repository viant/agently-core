package core

import (
	"context"
	"testing"

	"github.com/viant/agently-core/protocol/binding"
)

func TestGenerateInput_Init_AppendsSkillsPromptSystemMessage(t *testing.T) {
	in := &GenerateInput{
		Binding: &binding.Binding{
			SkillsPrompt: "<skills_instructions>\n## Skills\n- playwright-cli: Automate browser interactions.\n</skills_instructions>",
		},
		Prompt: &binding.Prompt{Engine: "go", Text: "hello"},
	}
	if err := in.Init(context.Background()); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
	if len(in.Message) == 0 {
		t.Fatalf("expected skills prompt system message")
	}
	if got := in.Message[0].Content; got != in.Binding.SkillsPrompt {
		t.Fatalf("skills prompt message = %q, want %q", got, in.Binding.SkillsPrompt)
	}
}
