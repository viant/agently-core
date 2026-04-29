package core

import (
	"context"
	"strings"
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

func TestGenerateInput_Init_SkipsSkillsPromptWhenSkillAlreadyActive(t *testing.T) {
	in := &GenerateInput{
		Binding: &binding.Binding{
			SkillsPrompt: "<skills_instructions>\n## Skills\n- forecasting-cube: Daily forecasting.\n</skills_instructions>",
			Task:         binding.Task{Prompt: "forecast now"},
			History: binding.History{
				Current: &binding.Turn{
					Messages: []*binding.Message{
						{
							Kind:     binding.MessageKindToolResult,
							ToolName: "llm/skills:activate",
							ToolArgs: map[string]interface{}{"name": "forecasting-cube"},
							Content:  "Loaded skill",
						},
					},
				},
			},
		},
		Prompt: &binding.Prompt{Text: "{{.Task.Prompt}}", Engine: "go"},
	}

	if err := in.Init(context.Background()); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for _, msg := range in.Message {
		if strings.Contains(msg.Content, "<skills_instructions>") {
			t.Fatalf("unexpected skills prompt in messages: %#v", in.Message)
		}
	}
}
