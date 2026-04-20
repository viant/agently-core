package sdk

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/viant/agently-core/app/executor"
	"github.com/viant/agently-core/genai/llm"
	base "github.com/viant/agently-core/genai/llm/provider/base"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/workspace"
)

type streamTestAgentFinder struct{ agent *agentmdl.Agent }

func (f *streamTestAgentFinder) Find(context.Context, string) (*agentmdl.Agent, error) {
	return f.agent, nil
}

type streamTestModelFinder struct{ model llm.Model }

func (f *streamTestModelFinder) Find(context.Context, string) (llm.Model, error) {
	return f.model, nil
}

type streamTestModel struct{}

func (m *streamTestModel) Generate(context.Context, *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	return &llm.GenerateResponse{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}}}, nil
}

func (m *streamTestModel) Implements(feature string) bool { return feature == base.CanUseTools }

func TestHTTPStream_ActivateSkill_EmitsSkillActivatedEvent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)
	skillDir := filepath.Join(root, "skills", "demo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo\ndescription: Demo skill.\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	agent := &agentmdl.Agent{
		Identity: agentmdl.Identity{ID: "coder", Name: "Coder"},
		ModelSelection: llm.ModelSelection{
			Model: "test-model",
		},
		Prompt: &binding.Prompt{Engine: "go", Text: "{{.Task.Prompt}}"},
		Skills: []string{"demo"},
	}
	rt, err := executor.NewBuilder().
		WithAgentFinder(&streamTestAgentFinder{agent: agent}).
		WithModelFinder(&streamTestModelFinder{model: &streamTestModel{}}).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	backend, err := newBackendFromRuntime(rt)
	if err != nil {
		t.Fatalf("newBackendFromRuntime() error: %v", err)
	}
	handler, err := NewHandlerWithContext(context.Background(), backend)
	if err != nil {
		t.Fatalf("NewHandlerWithContext() error: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()
	client, err := NewHTTP(srv.URL)
	if err != nil {
		t.Fatalf("NewHTTP() error: %v", err)
	}
	conv, err := client.CreateConversation(context.Background(), &CreateConversationInput{AgentID: "coder", Title: "skill event"})
	if err != nil {
		t.Fatalf("CreateConversation() error: %v", err)
	}
	streamCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, err := client.StreamEvents(streamCtx, &StreamEventsInput{ConversationID: conv.Id})
	if err != nil {
		t.Fatalf("StreamEvents() error: %v", err)
	}
	defer sub.Close()
	if _, err := client.ActivateSkill(context.Background(), &ActivateSkillInput{ConversationID: conv.Id, Name: "demo"}); err != nil {
		t.Fatalf("ActivateSkill() error: %v", err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-sub.C():
			if !ok {
				t.Fatal("stream closed before skill event")
			}
			if ev != nil && (ev.Type == streaming.EventTypeSkillStarted || ev.Type == streaming.EventTypeSkillCompleted) {
				if ev.SkillName != "demo" {
					t.Fatalf("skillName = %q, want demo", ev.SkillName)
				}
				if ev.Type == streaming.EventTypeSkillCompleted {
					_ = sub.Close()
					cancel()
					return
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for skill lifecycle event")
		}
	}
}

func TestHTTPStream_SkillRegistryUpdate_EmitsEvent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)
	skillDir := filepath.Join(root, "skills", "demo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo\ndescription: Demo skill.\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	agent := &agentmdl.Agent{
		Identity: agentmdl.Identity{ID: "coder", Name: "Coder"},
		ModelSelection: llm.ModelSelection{
			Model: "test-model",
		},
		Prompt: &binding.Prompt{Engine: "go", Text: "{{.Task.Prompt}}"},
		Skills: []string{"demo"},
	}
	rt, err := executor.NewBuilder().
		WithAgentFinder(&streamTestAgentFinder{agent: agent}).
		WithModelFinder(&streamTestModelFinder{model: &streamTestModel{}}).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	backend, err := newBackendFromRuntime(rt)
	if err != nil {
		t.Fatalf("newBackendFromRuntime() error: %v", err)
	}
	handler, err := NewHandlerWithContext(context.Background(), backend)
	if err != nil {
		t.Fatalf("NewHandlerWithContext() error: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()
	client, err := NewHTTP(srv.URL)
	if err != nil {
		t.Fatalf("NewHTTP() error: %v", err)
	}
	streamCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, err := client.StreamEvents(streamCtx, &StreamEventsInput{})
	if err != nil {
		t.Fatalf("StreamEvents() error: %v", err)
	}
	defer sub.Close()
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo\ndescription: Updated demo skill.\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-sub.C():
			if !ok {
				t.Fatal("stream closed before registry event")
			}
			if ev != nil && ev.Type == streaming.EventTypeSkillRegistryUpdated {
				_ = sub.Close()
				cancel()
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for skill_registry_updated event")
		}
	}
}
