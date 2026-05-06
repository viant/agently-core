package agent

import (
	"context"
	"testing"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	"github.com/viant/agently-core/protocol/binding"
	skillproto "github.com/viant/agently-core/protocol/skill"
	agruntime "github.com/viant/agently-core/runtime"
)

func skillStrPtr(v string) *string { return &v }

func TestRuntimeActivatedSkill(t *testing.T) {
	input := &QueryInput{
		Runtime: &agruntime.Context{
			SkillActivation: &skillproto.ActivationContext{Name: "forecast", Body: "Loaded skill body"},
		},
	}
	name, body, ok := runtimeActivatedSkill(input)
	if !ok {
		t.Fatalf("expected active skill context")
	}
	if name != "forecast" {
		t.Fatalf("name = %q", name)
	}
	if body != "Loaded skill body" {
		t.Fatalf("body = %q", body)
	}
}

func TestResolveActiveSkillNames_FallsBackToContext(t *testing.T) {
	input := &QueryInput{
		Runtime: &agruntime.Context{
			SkillActivation: &skillproto.ActivationContext{Name: "forecast", Body: "Loaded skill body"},
		},
	}
	names := resolveActiveSkillNames(&binding.History{}, input, nil, nil, "", "")
	if len(names) != 1 || names[0] != "forecast" {
		t.Fatalf("names = %#v", names)
	}
}

func TestLatestInlineSkillContextForTurn_ExtractsMostRecentInlineActivation(t *testing.T) {
	contentDetach := `{"name":"beta","body":"loaded beta","mode":"detach","args":"x"}`
	contentInline := `{"name":"forecast","body":"loaded forecast","mode":"inline","args":"y"}`
	conv := &apiconv.Conversation{
		Transcript: []*agconv.TranscriptView{
			{
				Id: "turn-1",
				Message: []*agconv.MessageView{
					{ToolName: skillStrPtr("llm/skills:activate"), Content: skillStrPtr(contentDetach)},
					{ToolName: skillStrPtr("llm_skills-activate"), Content: skillStrPtr(contentInline)},
				},
			},
		},
	}

	rt := latestInlineSkillContextForTurn(conv, "turn-1")
	if rt == nil || rt.SkillActivation == nil {
		t.Fatalf("expected skill context")
	}
	value := rt.SkillActivation
	if value.Name != "forecast" {
		t.Fatalf("skillActivationName = %#v", value.Name)
	}
	if value.Body != "loaded forecast" {
		t.Fatalf("skillActivationBody = %#v", value.Body)
	}
	if value.Mode != "inline" {
		t.Fatalf("skillActivationMode = %#v", value.Mode)
	}
	if value.Args != "y" {
		t.Fatalf("skillActivationArgs = %#v", value.Args)
	}
}

func TestLoadInlineSkillContextForTurn_LoadsConversationTranscript(t *testing.T) {
	store := convmem.New()
	ctx := context.Background()

	conv := apiconv.NewConversation()
	conv.SetId("conv-skill")
	if err := store.PatchConversations(ctx, conv); err != nil {
		t.Fatal(err)
	}
	msg := apiconv.NewMessage()
	msg.SetId("msg-skill")
	msg.SetConversationID("conv-skill")
	msg.SetTurnID("turn-skill")
	msg.SetRole("tool")
	msg.SetType("tool_op")
	content := `{"name":"forecast","body":"loaded forecast","mode":"inline","args":"z"}`
	msg.SetContent(content)
	msg.SetToolName("llm/skills:activate")
	if err := store.PatchMessage(ctx, msg); err != nil {
		t.Fatal(err)
	}

	got := loadInlineSkillContextForTurn(ctx, store, "conv-skill", "turn-skill")
	if got == nil || got.SkillActivation == nil {
		t.Fatalf("expected skill context")
	}
	value := got.SkillActivation
	if value.Name != "forecast" {
		t.Fatalf("skillActivationName = %#v", value.Name)
	}
}
