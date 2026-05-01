package executor_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/viant/agently-core/app/executor"
	execconfig "github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	provider "github.com/viant/agently-core/genai/llm/provider"
	base "github.com/viant/agently-core/genai/llm/provider/base"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	runtimeprojection "github.com/viant/agently-core/runtime/projection"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/agently-core/workspace"
)

type skillTestAgentFinder struct {
	agent *agentmdl.Agent
}

func (f *skillTestAgentFinder) Find(context.Context, string) (*agentmdl.Agent, error) {
	return f.agent, nil
}

type skillTestModelFinder struct {
	model      llm.Model
	byID       map[string]llm.Model
	errByID    map[string]error
	configByID map[string]*provider.Config
}

func (f *skillTestModelFinder) Find(_ context.Context, id string) (llm.Model, error) {
	if f != nil {
		if err, ok := f.errByID[id]; ok {
			return nil, err
		}
		if model, ok := f.byID[id]; ok {
			return model, nil
		}
	}
	return f.model, nil
}

func (f *skillTestModelFinder) ConfigByIDOrModel(id string) *provider.Config {
	if f != nil && f.configByID != nil {
		return f.configByID[id]
	}
	return nil
}

func (f *skillTestModelFinder) Best(preferences *llm.ModelPreferences) string {
	return f.BestWithFilter(preferences, nil)
}

func (f *skillTestModelFinder) BestWithFilter(preferences *llm.ModelPreferences, allow func(id string) bool) string {
	if preferences == nil || len(f.byID) == 0 {
		return ""
	}
	for _, hint := range preferences.Hints {
		want := strings.TrimSpace(strings.ToLower(hint))
		if want == "" {
			continue
		}
		for id := range f.byID {
			if allow != nil && !allow(id) {
				continue
			}
			candidate := strings.TrimSpace(strings.ToLower(id))
			if candidate == want || strings.Contains(candidate, want) {
				return id
			}
		}
	}
	for id := range f.byID {
		if allow == nil || allow(id) {
			return id
		}
	}
	return ""
}

type skillSequenceModel struct {
	outcomes []llm.GenerateResponse
	calls    int
}

func (m *skillSequenceModel) Generate(context.Context, *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	if m.calls >= len(m.outcomes) {
		return &llm.GenerateResponse{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}}}, nil
	}
	resp := m.outcomes[m.calls]
	m.calls++
	return &resp, nil
}

func (m *skillSequenceModel) Implements(feature string) bool {
	switch feature {
	case base.CanUseTools:
		return true
	case base.CanStream:
		return false
	case base.SupportsContextContinuation:
		return false
	default:
		return false
	}
}

type skillContextModel struct {
	mu               sync.Mutex
	firstResponse    llm.GenerateResponse
	childContent     string
	parentFollowup   string
	firstCallHandled bool
}

func (m *skillContextModel) Generate(_ context.Context, req *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.firstCallHandled {
		m.firstCallHandled = true
		resp := m.firstResponse
		return &resp, nil
	}
	for _, msg := range req.Messages {
		if strings.Contains(msg.Content, `Loaded skill "demo"`) {
			return &llm.GenerateResponse{
				Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: m.childContent}}},
			}, nil
		}
	}
	return &llm.GenerateResponse{
		Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: m.parentFollowup}}},
	}, nil
}

func (m *skillContextModel) Implements(feature string) bool {
	switch feature {
	case base.CanUseTools:
		return true
	case base.CanStream:
		return false
	case base.SupportsContextContinuation:
		return false
	default:
		return false
	}
}

func writeSkill(t *testing.T, root, name, desc, allowed, body string) {
	t.Helper()
	dir := filepath.Join(root, "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + desc + "\n"
	if allowed != "" {
		content += "allowed-tools: " + allowed + "\n"
	}
	content += "---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeSkillWithFrontmatter(t *testing.T, root, name string, frontmatter map[string]string, body string) {
	t.Helper()
	dir := filepath.Join(root, "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\n"
	content += "name: " + name + "\n"
	for _, key := range []string{"description", "context", "allowed-tools", "model", "effort", "temperature", "max-tokens", "preprocess", "preprocess-timeout"} {
		if value, ok := frontmatter[key]; ok && strings.TrimSpace(value) != "" {
			content += key + ": " + value + "\n"
		}
	}
	content += "---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeSkillRaw(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func waitForPayloadFiles(t *testing.T, dir string, minCount int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		matches, err := filepath.Glob(filepath.Join(dir, "llm-request-*.json"))
		if err != nil {
			t.Fatalf("glob payload files: %v", err)
		}
		if len(matches) >= minCount {
			return matches
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d llm-request payloads, got %d", minCount, len(matches))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func payloadContains(data []byte, needle string) bool {
	return strings.Contains(string(data), needle)
}

func newTestAgent(skillName string) *agentmdl.Agent {
	return &agentmdl.Agent{
		Identity: agentmdl.Identity{ID: "coder", Name: "Coder"},
		ModelSelection: llm.ModelSelection{
			Model: "test-model",
		},
		Prompt: &binding.Prompt{Engine: "go", Text: "{{.Task.Prompt}}"},
		Tool: agentmdl.Tool{
			Bundles: []string{"system/exec"},
		},
		Skills: []string{skillName},
	}
}

func TestRuntimeQuery_ExplicitSkillActivation_EmitsSkillInLLMRequest(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "demo", "Demo skill.", "", "# Demo Skill\nFollow the demo instructions.")
	payloadDir := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)
	t.Setenv("AGENTLY_DEBUG_TRACE_FILE", filepath.Join(t.TempDir(), "trace.jsonl"))
	t.Setenv("AGENTLY_DEBUG_PAYLOAD_DIR", payloadDir)
	agent := newTestAgent("demo")
	model := &skillSequenceModel{outcomes: []llm.GenerateResponse{
		{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}}},
	}}
	rt, err := executor.NewBuilder().
		WithAgentFinder(&skillTestAgentFinder{agent: agent}).
		WithModelFinder(&skillTestModelFinder{model: model}).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	out := &agentsvc.QueryOutput{}
	err = rt.Agent.Query(context.Background(), &agentsvc.QueryInput{
		ConversationID: "conv-explicit-skill",
		AgentID:        "coder",
		UserId:         "tester",
		Query:          "/demo use it",
		DisplayQuery:   "/demo use it",
		Agent:          agent,
	}, out)
	if err != nil {
		t.Fatalf("Query() error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(payloadDir, "llm-request-conv-explicit-skill.json"))
	if err != nil {
		t.Fatalf("read llm-request payload: %v", err)
	}
	var payload struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
		Options struct {
			Metadata map[string]interface{} `json:"metadata"`
		} `json:"options"`
		DebugContext map[string]interface{} `json:"debugContext"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	foundBody := false
	for _, msg := range payload.Messages {
		if strings.Contains(msg.Content, `Loaded skill "demo"`) {
			foundBody = true
		}
	}
	if !foundBody {
		t.Fatalf("payload missing expected content: %s", string(data))
	}
	if payload.Options.Metadata == nil {
		t.Fatalf("expected metadata in payload")
	}
	if _, ok := payload.Options.Metadata["activeSkillNames"]; !ok {
		t.Fatalf("expected activeSkillNames in payload metadata: %#v", payload.Options.Metadata)
	}
	if _, ok := payload.Options.Metadata["activeSkillConstraints"]; !ok {
		t.Fatalf("expected activeSkillConstraints in payload metadata: %#v", payload.Options.Metadata)
	}
}

func TestRuntimeQuery_BaseIteration_EmitsAgentModelSourceInLLMRequest(t *testing.T) {
	root := t.TempDir()
	payloadDir := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)
	t.Setenv("AGENTLY_DEBUG_TRACE_FILE", filepath.Join(t.TempDir(), "trace.jsonl"))
	t.Setenv("AGENTLY_DEBUG_PAYLOAD_DIR", payloadDir)
	agent := newTestAgent("demo")
	model := &skillSequenceModel{outcomes: []llm.GenerateResponse{
		{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}}},
	}}
	rt, err := executor.NewBuilder().
		WithAgentFinder(&skillTestAgentFinder{agent: agent}).
		WithModelFinder(&skillTestModelFinder{model: model}).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	if err := rt.Agent.Query(context.Background(), &agentsvc.QueryInput{
		ConversationID: "conv-base-model-source",
		AgentID:        "coder",
		UserId:         "tester",
		Query:          "hello",
		DisplayQuery:   "hello",
		Agent:          agent,
	}, &agentsvc.QueryOutput{}); err != nil {
		t.Fatalf("Query() error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(payloadDir, "llm-request-conv-base-model-source.json"))
	if err != nil {
		t.Fatalf("read llm-request payload: %v", err)
	}
	var payload struct {
		Model   string `json:"model"`
		Options struct {
			Metadata map[string]interface{} `json:"metadata"`
		} `json:"options"`
		DebugContext map[string]interface{} `json:"debugContext"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Model != "test-model" {
		t.Fatalf("expected base model test-model, got %q", payload.Model)
	}
	if payload.Options.Metadata["modelSource"] != "agent.model" {
		t.Fatalf("expected modelSource agent.model, got %#v", payload.Options.Metadata)
	}
}

func TestRuntimeQuery_ExplicitSkillActivation_NarrowsToolListInLLMRequest(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "demo", "Demo skill.", "Bash(echo:*) system/exec:execute", "# Demo Skill\nOnly echo is allowed.")
	payloadDir := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)
	t.Setenv("AGENTLY_DEBUG_TRACE_FILE", filepath.Join(t.TempDir(), "trace.jsonl"))
	t.Setenv("AGENTLY_DEBUG_PAYLOAD_DIR", payloadDir)
	agent := newTestAgent("demo")
	model := &skillSequenceModel{outcomes: []llm.GenerateResponse{
		{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}}},
	}}
	rt, err := executor.NewBuilder().
		WithAgentFinder(&skillTestAgentFinder{agent: agent}).
		WithModelFinder(&skillTestModelFinder{model: model}).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	if err := rt.Agent.Query(context.Background(), &agentsvc.QueryInput{
		ConversationID: "conv-narrow-tools",
		AgentID:        "coder",
		UserId:         "tester",
		Query:          "/demo use it",
		DisplayQuery:   "/demo use it",
		Agent:          agent,
	}, &agentsvc.QueryOutput{}); err != nil {
		t.Fatalf("Query() error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(payloadDir, "llm-request-conv-narrow-tools.json"))
	if err != nil {
		t.Fatalf("read llm-request payload: %v", err)
	}
	var payload struct {
		Options struct {
			Tools []struct {
				Definition struct {
					Name string `json:"name"`
				} `json:"definition"`
			} `json:"tools"`
		} `json:"options"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	var names []string
	for _, item := range payload.Options.Tools {
		names = append(names, item.Definition.Name)
	}
	if !contains(names, "system_exec-execute") && !contains(names, "system/exec:execute") {
		t.Fatalf("expected exec tool in request, got %v", names)
	}
	if contains(names, "system_exec-start") || contains(names, "system/exec:start") || contains(names, "system_exec-cancel") || contains(names, "system/exec:cancel") {
		t.Fatalf("expected narrowed tool list, got %v", names)
	}
}

func TestRuntimeQuery_ExplicitSkillActivation_UsesConfiguredSkillsModel(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "demo", "Demo skill.", "", "# Demo Skill\nFollow the demo instructions.")
	payloadDir := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)
	t.Setenv("AGENTLY_DEBUG_TRACE_FILE", filepath.Join(t.TempDir(), "trace.jsonl"))
	t.Setenv("AGENTLY_DEBUG_PAYLOAD_DIR", payloadDir)
	agent := newTestAgent("demo")
	model := &skillSequenceModel{outcomes: []llm.GenerateResponse{
		{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}}},
	}}
	rt, err := executor.NewBuilder().
		WithDefaults(&execconfig.Defaults{
			Skills: execconfig.SkillsDefaults{
				Roots: []string{filepath.Join(root, "skills")},
				Model: "openai_gpt-5.4",
			},
		}).
		WithAgentFinder(&skillTestAgentFinder{agent: agent}).
		WithModelFinder(&skillTestModelFinder{model: model}).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	if err := rt.Agent.Query(context.Background(), &agentsvc.QueryInput{
		ConversationID: "conv-skill-model",
		AgentID:        "coder",
		UserId:         "tester",
		Query:          "/demo use it",
		DisplayQuery:   "/demo use it",
		Agent:          agent,
	}, &agentsvc.QueryOutput{}); err != nil {
		t.Fatalf("Query() error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(payloadDir, "llm-request-conv-skill-model.json"))
	if err != nil {
		t.Fatalf("read llm-request payload: %v", err)
	}
	var payload struct {
		Model   string `json:"model"`
		Options struct {
			Metadata map[string]interface{} `json:"metadata"`
		} `json:"options"`
		DebugContext map[string]interface{} `json:"debugContext"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Model != "openai_gpt-5.4" {
		t.Fatalf("expected skills model override, got %q", payload.Model)
	}
	if payload.Options.Metadata == nil || payload.Options.Metadata["activeSkillModel"] != "openai_gpt-5.4" {
		t.Fatalf("expected activeSkillModel metadata, got %#v", payload.Options.Metadata)
	}
	if payload.Options.Metadata["modelSource"] != "skills.model" {
		t.Fatalf("expected modelSource skills.model, got %#v", payload.Options.Metadata)
	}
}

func TestRuntimeQuery_ExplicitSkillActivation_UsesFrontmatterSkillModelOverride(t *testing.T) {
	root := t.TempDir()
	writeSkillWithFrontmatter(t, root, "demo", map[string]string{
		"description": "Demo skill.",
		"model":       "openai_gpt-5.4",
	}, "# Demo Skill\nFollow the demo instructions.")
	payloadDir := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)
	t.Setenv("AGENTLY_DEBUG_TRACE_FILE", filepath.Join(t.TempDir(), "trace.jsonl"))
	t.Setenv("AGENTLY_DEBUG_PAYLOAD_DIR", payloadDir)
	agent := newTestAgent("demo")
	model := &skillSequenceModel{outcomes: []llm.GenerateResponse{
		{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}}},
	}}
	rt, err := executor.NewBuilder().
		WithDefaults(&execconfig.Defaults{
			Skills: execconfig.SkillsDefaults{
				Roots: []string{filepath.Join(root, "skills")},
				Model: "openai_gpt-5.2",
			},
		}).
		WithAgentFinder(&skillTestAgentFinder{agent: agent}).
		WithModelFinder(&skillTestModelFinder{
			model: model,
			configByID: map[string]*provider.Config{
				"openai_gpt-5.4": {ID: "openai_gpt-5.4", Options: provider.Options{Provider: provider.ProviderOpenAI, Model: "gpt-5.4"}},
			},
		}).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	if err := rt.Agent.Query(context.Background(), &agentsvc.QueryInput{
		ConversationID: "conv-skill-frontmatter-model",
		AgentID:        "coder",
		UserId:         "tester",
		Query:          "/demo use it",
		DisplayQuery:   "/demo use it",
		Agent:          agent,
	}, &agentsvc.QueryOutput{}); err != nil {
		t.Fatalf("Query() error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(payloadDir, "llm-request-conv-skill-frontmatter-model.json"))
	if err != nil {
		t.Fatalf("read llm-request payload: %v", err)
	}
	var payload struct {
		Model   string `json:"model"`
		Options struct {
			Metadata map[string]interface{} `json:"metadata"`
		} `json:"options"`
		DebugContext map[string]interface{} `json:"debugContext"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Model != "openai_gpt-5.4" {
		t.Fatalf("expected frontmatter model override, got %q", payload.Model)
	}
	if payload.Options.Metadata["modelSource"] != "skill.frontmatter" {
		t.Fatalf("expected modelSource skill.frontmatter, got %#v", payload.Options.Metadata)
	}
}

func TestRuntimeQuery_ExplicitSkillActivation_FallbacksOnUnknownFrontmatterModel(t *testing.T) {
	root := t.TempDir()
	writeSkillWithFrontmatter(t, root, "demo", map[string]string{
		"description": "Demo skill.",
		"model":       "unknown-model",
	}, "# Demo Skill\nFollow the demo instructions.")
	payloadDir := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)
	t.Setenv("AGENTLY_DEBUG_TRACE_FILE", filepath.Join(t.TempDir(), "trace.jsonl"))
	t.Setenv("AGENTLY_DEBUG_PAYLOAD_DIR", payloadDir)
	agent := newTestAgent("demo")
	model := &skillSequenceModel{outcomes: []llm.GenerateResponse{
		{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}}},
	}}
	rt, err := executor.NewBuilder().
		WithDefaults(&execconfig.Defaults{
			Skills: execconfig.SkillsDefaults{
				Roots: []string{filepath.Join(root, "skills")},
				Model: "openai_gpt-5.4",
			},
		}).
		WithAgentFinder(&skillTestAgentFinder{agent: agent}).
		WithModelFinder(&skillTestModelFinder{
			model:      model,
			configByID: map[string]*provider.Config{},
			errByID: map[string]error{
				"unknown-model": fmt.Errorf("model not in finder registry"),
			},
		}).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	if err := rt.Agent.Query(context.Background(), &agentsvc.QueryInput{
		ConversationID: "conv-skill-frontmatter-fallback",
		AgentID:        "coder",
		UserId:         "tester",
		Query:          "/demo use it",
		DisplayQuery:   "/demo use it",
		Agent:          agent,
	}, &agentsvc.QueryOutput{}); err != nil {
		t.Fatalf("Query() error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(payloadDir, "llm-request-conv-skill-frontmatter-fallback.json"))
	if err != nil {
		t.Fatalf("read llm-request payload: %v", err)
	}
	var payload struct {
		Model   string `json:"model"`
		Options struct {
			Metadata map[string]interface{} `json:"metadata"`
		} `json:"options"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Model != "openai_gpt-5.4" {
		t.Fatalf("expected fallback to skills model, got %q", payload.Model)
	}
	if payload.Options.Metadata["modelSource"] != "skills.model" {
		t.Fatalf("expected modelSource skills.model, got %#v", payload.Options.Metadata)
	}
	if payload.Options.Metadata["modelSourceIntended"] != "skill.frontmatter" {
		t.Fatalf("expected modelSourceIntended, got %#v", payload.Options.Metadata)
	}
	if payload.Options.Metadata["modelSourceIntendedValue"] != "unknown-model" {
		t.Fatalf("expected modelSourceIntendedValue, got %#v", payload.Options.Metadata)
	}
}

func TestRuntimeQuery_ExplicitSkillActivation_AppliesFrontmatterOptions(t *testing.T) {
	root := t.TempDir()
	writeSkillWithFrontmatter(t, root, "demo", map[string]string{
		"description":        "Demo skill.",
		"temperature":        "0.2",
		"max-tokens":         "8000",
		"effort":             "high",
		"preprocess":         "true",
		"preprocess-timeout": "7",
	}, "# Demo Skill\nFollow the demo instructions.")
	payloadDir := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)
	t.Setenv("AGENTLY_DEBUG_TRACE_FILE", filepath.Join(t.TempDir(), "trace.jsonl"))
	t.Setenv("AGENTLY_DEBUG_PAYLOAD_DIR", payloadDir)
	agent := newTestAgent("demo")
	model := &skillSequenceModel{outcomes: []llm.GenerateResponse{
		{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}}},
	}}
	rt, err := executor.NewBuilder().
		WithAgentFinder(&skillTestAgentFinder{agent: agent}).
		WithModelFinder(&skillTestModelFinder{model: model}).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	if err := rt.Agent.Query(context.Background(), &agentsvc.QueryInput{
		ConversationID: "conv-skill-frontmatter-options",
		AgentID:        "coder",
		UserId:         "tester",
		Query:          "/demo use it",
		DisplayQuery:   "/demo use it",
		Agent:          agent,
	}, &agentsvc.QueryOutput{}); err != nil {
		t.Fatalf("Query() error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(payloadDir, "llm-request-conv-skill-frontmatter-options.json"))
	if err != nil {
		t.Fatalf("read llm-request payload: %v", err)
	}
	var payload struct {
		Options struct {
			Metadata map[string]interface{} `json:"metadata"`
		} `json:"options"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Options.Metadata["activeSkillTemperature"] != 0.2 {
		t.Fatalf("expected activeSkillTemperature 0.2, got %#v", payload.Options.Metadata)
	}
	if payload.Options.Metadata["activeSkillMaxTokens"] != float64(8000) {
		t.Fatalf("expected activeSkillMaxTokens 8000, got %#v", payload.Options.Metadata)
	}
	if payload.Options.Metadata["activeSkillReasoningEffort"] != "high" {
		t.Fatalf("expected activeSkillReasoningEffort high, got %#v", payload.Options.Metadata)
	}
	if payload.Options.Metadata["activeSkillPreprocess"] != true {
		t.Fatalf("expected activeSkillPreprocess true, got %#v", payload.Options.Metadata)
	}
	if payload.Options.Metadata["activeSkillPreprocessTimeoutSeconds"] != float64(7) {
		t.Fatalf("expected activeSkillPreprocessTimeoutSeconds 7, got %#v", payload.Options.Metadata)
	}
}

func TestRuntimeQuery_ExplicitSkillActivation_AppliesMetadataOptions(t *testing.T) {
	root := t.TempDir()
	writeSkillRaw(t, root, "demo", `---
name: demo
description: Demo skill.
metadata:
  agently-context: inline
  agently-temperature: "0.2"
  agently-max-tokens: "8000"
  agently-effort: high
  agently-preprocess: "true"
  agently-preprocess-timeout: "7"
allowed-tools: system/exec:execute
---

# Demo Skill
Follow the demo instructions.
`)
	payloadDir := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)
	t.Setenv("AGENTLY_DEBUG_TRACE_FILE", filepath.Join(t.TempDir(), "trace.jsonl"))
	t.Setenv("AGENTLY_DEBUG_PAYLOAD_DIR", payloadDir)
	agent := newTestAgent("demo")
	model := &skillSequenceModel{outcomes: []llm.GenerateResponse{
		{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}}},
	}}
	rt, err := executor.NewBuilder().
		WithAgentFinder(&skillTestAgentFinder{agent: agent}).
		WithModelFinder(&skillTestModelFinder{model: model}).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	if err := rt.Agent.Query(context.Background(), &agentsvc.QueryInput{
		ConversationID: "conv-skill-metadata-options",
		AgentID:        "coder",
		UserId:         "tester",
		Query:          "$demo use it",
		DisplayQuery:   "$demo use it",
		Agent:          agent,
	}, &agentsvc.QueryOutput{}); err != nil {
		t.Fatalf("Query() error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(payloadDir, "llm-request-conv-skill-metadata-options.json"))
	if err != nil {
		t.Fatalf("read llm-request payload: %v", err)
	}
	var payload struct {
		Options struct {
			Metadata map[string]interface{} `json:"metadata"`
		} `json:"options"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Options.Metadata["activeSkillTemperature"] != 0.2 {
		t.Fatalf("expected activeSkillTemperature 0.2, got %#v", payload.Options.Metadata)
	}
	if payload.Options.Metadata["activeSkillMaxTokens"] != float64(8000) {
		t.Fatalf("expected activeSkillMaxTokens 8000, got %#v", payload.Options.Metadata)
	}
	if payload.Options.Metadata["activeSkillReasoningEffort"] != "high" {
		t.Fatalf("expected activeSkillReasoningEffort high, got %#v", payload.Options.Metadata)
	}
	if payload.Options.Metadata["activeSkillPreprocess"] != true {
		t.Fatalf("expected activeSkillPreprocess true, got %#v", payload.Options.Metadata)
	}
	if payload.Options.Metadata["activeSkillPreprocessTimeoutSeconds"] != float64(7) {
		t.Fatalf("expected activeSkillPreprocessTimeoutSeconds 7, got %#v", payload.Options.Metadata)
	}
}

func TestRuntimeQuery_ExplicitSkillActivation_IgnoresInvisibleDollarSkill(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "demo", "Demo skill.", "", "# Demo Skill\nFollow the demo instructions.")
	payloadDir := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)
	t.Setenv("AGENTLY_DEBUG_TRACE_FILE", filepath.Join(t.TempDir(), "trace.jsonl"))
	t.Setenv("AGENTLY_DEBUG_PAYLOAD_DIR", payloadDir)
	agent := newTestAgent("other-skill")
	model := &skillSequenceModel{outcomes: []llm.GenerateResponse{
		{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}}},
	}}
	rt, err := executor.NewBuilder().
		WithAgentFinder(&skillTestAgentFinder{agent: agent}).
		WithModelFinder(&skillTestModelFinder{model: model}).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	if err := rt.Agent.Query(context.Background(), &agentsvc.QueryInput{
		ConversationID: "conv-skill-dollar-invisible",
		AgentID:        "coder",
		UserId:         "tester",
		Query:          "$demo use it",
		DisplayQuery:   "$demo use it",
		Agent:          agent,
	}, &agentsvc.QueryOutput{}); err != nil {
		t.Fatalf("Query() error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(payloadDir, "llm-request-conv-skill-dollar-invisible.json"))
	if err != nil {
		t.Fatalf("read llm-request payload: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "llm_skills-activate") || strings.Contains(text, "llm/skills:activate") {
		t.Fatalf("expected no skill activation for invisible skill, payload=%s", text)
	}
	if !strings.Contains(text, "$demo use it") {
		t.Fatalf("expected raw $demo query to pass through, payload=%s", text)
	}
}

func TestRuntimeQuery_ExplicitSkillActivation_IgnoresEscapedDollarSkill(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "demo", "Demo skill.", "", "# Demo Skill\nFollow the demo instructions.")
	payloadDir := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)
	t.Setenv("AGENTLY_DEBUG_TRACE_FILE", filepath.Join(t.TempDir(), "trace.jsonl"))
	t.Setenv("AGENTLY_DEBUG_PAYLOAD_DIR", payloadDir)
	agent := newTestAgent("demo")
	model := &skillSequenceModel{outcomes: []llm.GenerateResponse{
		{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}}},
	}}
	rt, err := executor.NewBuilder().
		WithAgentFinder(&skillTestAgentFinder{agent: agent}).
		WithModelFinder(&skillTestModelFinder{model: model}).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	if err := rt.Agent.Query(context.Background(), &agentsvc.QueryInput{
		ConversationID: "conv-skill-dollar-escaped",
		AgentID:        "coder",
		UserId:         "tester",
		Query:          "$$demo use it",
		DisplayQuery:   "$$demo use it",
		Agent:          agent,
	}, &agentsvc.QueryOutput{}); err != nil {
		t.Fatalf("Query() error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(payloadDir, "llm-request-conv-skill-dollar-escaped.json"))
	if err != nil {
		t.Fatalf("read llm-request payload: %v", err)
	}
	var payload struct {
		Messages []struct {
			Role    string `json:"role"`
			Name    string `json:"name"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	for _, msg := range payload.Messages {
		if msg.Role == "tool" && (msg.Name == "llm_skills-activate" || msg.Name == "llm/skills:activate") {
			t.Fatalf("expected no skill activation tool result for escaped token, payload=%#v", payload.Messages)
		}
	}
	foundRaw := false
	for _, msg := range payload.Messages {
		if strings.Contains(msg.Content, "$$demo use it") {
			foundRaw = true
			break
		}
	}
	if !foundRaw {
		t.Fatalf("expected raw $$demo query to pass through, payload=%#v", payload.Messages)
	}
}

func TestRuntimeQuery_ExplicitSkillActivation_PreprocessesBodyInLLMRequest(t *testing.T) {
	root := t.TempDir()
	writeSkillWithFrontmatter(t, root, "demo", map[string]string{
		"description":        "Demo skill.",
		"allowed-tools":      "Bash(echo:*) system/exec:execute",
		"preprocess":         "true",
		"preprocess-timeout": "7",
	}, "Before\n`!`echo hi`\nAfter")
	payloadDir := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)
	t.Setenv("AGENTLY_DEBUG_TRACE_FILE", filepath.Join(t.TempDir(), "trace.jsonl"))
	t.Setenv("AGENTLY_DEBUG_PAYLOAD_DIR", payloadDir)

	agent := newTestAgent("demo")
	model := &skillSequenceModel{outcomes: []llm.GenerateResponse{
		{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}}},
	}}
	rt, err := executor.NewBuilder().
		WithAgentFinder(&skillTestAgentFinder{agent: agent}).
		WithModelFinder(&skillTestModelFinder{model: model}).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	if err := rt.Agent.Query(context.Background(), &agentsvc.QueryInput{
		ConversationID: "conv-skill-preprocess-body",
		AgentID:        "coder",
		UserId:         "tester",
		Query:          "/demo use it",
		DisplayQuery:   "/demo use it",
		Agent:          agent,
	}, &agentsvc.QueryOutput{}); err != nil {
		t.Fatalf("Query() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(payloadDir, "llm-request-conv-skill-preprocess-body.json"))
	if err != nil {
		t.Fatalf("read llm-request payload: %v", err)
	}
	var payload struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
		Options struct {
			Metadata map[string]interface{} `json:"metadata"`
		} `json:"options"`
		DebugContext map[string]interface{} `json:"debugContext"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	var loadedSkill string
	for _, msg := range payload.Messages {
		if strings.Contains(msg.Content, `Loaded skill "demo"`) {
			loadedSkill = msg.Content
			break
		}
	}
	if loadedSkill == "" {
		t.Fatalf("expected loaded skill tool result in payload: %s", string(data))
	}
	if !strings.Contains(loadedSkill, "Before\nhi\nAfter") {
		t.Fatalf("expected preprocessed body in payload, got %q", loadedSkill)
	}
	if strings.Contains(loadedSkill, "`!`echo hi`") {
		t.Fatalf("expected raw preprocess placeholder to be removed, got %q", loadedSkill)
	}
	if payload.Options.Metadata["activeSkillPreprocess"] != true {
		t.Fatalf("expected activeSkillPreprocess metadata, got %#v", payload.Options.Metadata)
	}
	if payload.Options.Metadata["activeSkillPreprocessTimeoutSeconds"] != float64(7) {
		t.Fatalf("expected activeSkillPreprocessTimeoutSeconds 7, got %#v", payload.Options.Metadata)
	}
	if payload.DebugContext["activeSkillPreprocess"] != true {
		t.Fatalf("expected debugContext activeSkillPreprocess true, got %#v", payload.DebugContext)
	}
	if payload.DebugContext["activeSkillPreprocessTimeoutSeconds"] != float64(7) {
		t.Fatalf("expected debugContext activeSkillPreprocessTimeoutSeconds 7, got %#v", payload.DebugContext)
	}
}

func TestRuntimeQuery_QueryModelOverride_WinsOverSkillFrontmatterAndSkillsModel(t *testing.T) {
	root := t.TempDir()
	writeSkillWithFrontmatter(t, root, "demo", map[string]string{
		"description": "Demo skill.",
		"model":       "openai_gpt-5.4",
	}, "# Demo Skill\nFollow the demo instructions.")
	payloadDir := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)
	t.Setenv("AGENTLY_DEBUG_TRACE_FILE", filepath.Join(t.TempDir(), "trace.jsonl"))
	t.Setenv("AGENTLY_DEBUG_PAYLOAD_DIR", payloadDir)

	agent := newTestAgent("demo")
	model := &skillSequenceModel{outcomes: []llm.GenerateResponse{
		{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}}},
	}}
	rt, err := executor.NewBuilder().
		WithAgentFinder(&skillTestAgentFinder{agent: agent}).
		WithModelFinder(&skillTestModelFinder{
			model: model,
			byID: map[string]llm.Model{
				"openai_gpt-5.2": model,
				"openai_gpt-5.4": model,
			},
		}).
		WithDefaults(&execconfig.Defaults{
			Skills: execconfig.SkillsDefaults{Model: "openai_gpt-5.2"},
		}).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	if err := rt.Agent.Query(context.Background(), &agentsvc.QueryInput{
		ConversationID: "conv-query-model-override",
		AgentID:        "coder",
		UserId:         "tester",
		Query:          "$demo use it",
		DisplayQuery:   "$demo use it",
		ModelOverride:  "openai_gpt-5.2",
		Agent:          agent,
	}, &agentsvc.QueryOutput{}); err != nil {
		t.Fatalf("Query() error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(payloadDir, "llm-request-conv-query-model-override.json"))
	if err != nil {
		t.Fatalf("read llm-request payload: %v", err)
	}
	var payload struct {
		Model   string `json:"model"`
		Options struct {
			Metadata map[string]interface{} `json:"metadata"`
		} `json:"options"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Model != "openai_gpt-5.2" {
		t.Fatalf("expected query model override to win, got %q", payload.Model)
	}
	if payload.Options.Metadata["modelSource"] != "query.modelOverride" {
		t.Fatalf("expected modelSource query.modelOverride, got %#v", payload.Options.Metadata)
	}
}

func TestRuntimeQuery_SkillMetadataModelPreferences_SelectModelThroughExistingMatcher(t *testing.T) {
	root := t.TempDir()
	writeSkillRaw(t, root, "demo", `---
name: demo
description: Demo skill.
metadata:
  agently-context: inline
  model-preferences:
    hints:
      - name: openai_gpt-5.4
    intelligencePriority: 0.9
    speedPriority: 0.2
allowed-tools: system/exec:execute
---

# Demo Skill
Follow the demo instructions.
`)
	payloadDir := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)
	t.Setenv("AGENTLY_DEBUG_TRACE_FILE", filepath.Join(t.TempDir(), "trace.jsonl"))
	t.Setenv("AGENTLY_DEBUG_PAYLOAD_DIR", payloadDir)

	agent := newTestAgent("demo")
	model52 := &skillSequenceModel{outcomes: []llm.GenerateResponse{
		{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "from-5.2"}}}},
	}}
	model54 := &skillSequenceModel{outcomes: []llm.GenerateResponse{
		{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "from-5.4"}}}},
	}}
	rt, err := executor.NewBuilder().
		WithAgentFinder(&skillTestAgentFinder{agent: agent}).
		WithModelFinder(&skillTestModelFinder{
			model: model52,
			byID: map[string]llm.Model{
				"openai_gpt-5.2": model52,
				"openai_gpt-5.4": model54,
			},
		}).
		WithDefaults(&execconfig.Defaults{
			Skills: execconfig.SkillsDefaults{Model: "openai_gpt-5.2"},
		}).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	if err := rt.Agent.Query(context.Background(), &agentsvc.QueryInput{
		ConversationID: "conv-skill-model-preferences",
		AgentID:        "coder",
		UserId:         "tester",
		Query:          "$demo use it",
		DisplayQuery:   "$demo use it",
		Agent:          agent,
	}, &agentsvc.QueryOutput{}); err != nil {
		t.Fatalf("Query() error: %v", err)
	}
	if model54.calls == 0 {
		t.Fatalf("expected preferred model to be invoked, got model52.calls=%d model54.calls=%d", model52.calls, model54.calls)
	}
	data, err := os.ReadFile(filepath.Join(payloadDir, "llm-request-conv-skill-model-preferences.json"))
	if err != nil {
		t.Fatalf("read llm-request payload: %v", err)
	}
	var payload struct {
		Model   string `json:"model"`
		Options struct {
			Metadata map[string]interface{} `json:"metadata"`
		} `json:"options"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Options.Metadata["modelSource"] != "skill.metadata.modelPreferences" {
		t.Fatalf("expected modelSource skill.metadata.modelPreferences, got %#v", payload.Options.Metadata)
	}
	prefs, ok := payload.Options.Metadata["activeSkillModelPreferences"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected activeSkillModelPreferences metadata, got %#v", payload.Options.Metadata)
	}
	hints, ok := prefs["hints"].([]interface{})
	if !ok || len(hints) != 1 || hints[0] != "openai_gpt-5.4" {
		t.Fatalf("expected hints [openai_gpt-5.4], got %#v", prefs)
	}
}

func TestRuntimeQuery_ConversationDefaultModel_AppearsInLLMRequest(t *testing.T) {
	root := t.TempDir()
	payloadDir := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)
	t.Setenv("AGENTLY_DEBUG_TRACE_FILE", filepath.Join(t.TempDir(), "trace.jsonl"))
	t.Setenv("AGENTLY_DEBUG_PAYLOAD_DIR", payloadDir)

	agent := newTestAgent("demo")
	model := &skillSequenceModel{outcomes: []llm.GenerateResponse{
		{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "first"}}}},
		{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "second"}}}},
	}}
	rt, err := executor.NewBuilder().
		WithAgentFinder(&skillTestAgentFinder{agent: agent}).
		WithModelFinder(&skillTestModelFinder{
			model: model,
			byID: map[string]llm.Model{
				"openai_gpt-5.2": model,
				"openai_gpt-5.4": model,
			},
		}).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	conversationID := "conv-conversation-default-model"
	if err := rt.Agent.Query(context.Background(), &agentsvc.QueryInput{
		ConversationID: conversationID,
		AgentID:        "coder",
		UserId:         "tester",
		ModelOverride:  "openai_gpt-5.4",
		Query:          "first turn",
		DisplayQuery:   "first turn",
		Agent:          agent,
	}, &agentsvc.QueryOutput{}); err != nil {
		t.Fatalf("first Query() error: %v", err)
	}

	if err := rt.Agent.Query(context.Background(), &agentsvc.QueryInput{
		ConversationID: conversationID,
		AgentID:        "coder",
		UserId:         "tester",
		Query:          "second turn",
		DisplayQuery:   "second turn",
		Agent:          agent,
	}, &agentsvc.QueryOutput{}); err != nil {
		t.Fatalf("second Query() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(payloadDir, "llm-request-"+conversationID+".json"))
	if err != nil {
		t.Fatalf("read llm-request payload: %v", err)
	}
	var payload struct {
		Model   string `json:"model"`
		Options struct {
			Metadata map[string]interface{} `json:"metadata"`
		} `json:"options"`
		DebugContext map[string]interface{} `json:"debugContext"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Model != "openai_gpt-5.4" {
		t.Fatalf("expected conversation default model openai_gpt-5.4, got %q", payload.Model)
	}
	if payload.Options.Metadata["modelSource"] != "conversation.defaultModel" {
		t.Fatalf("expected modelSource conversation.defaultModel, got %#v", payload.Options.Metadata)
	}
	if payload.DebugContext["modelSource"] != "conversation.defaultModel" {
		t.Fatalf("expected debugContext modelSource conversation.defaultModel, got %#v", payload.DebugContext)
	}
}

func TestRuntimeQuery_SkillToolsAppearOnlyWhenAgentHasVisibleSkills(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "demo", "Demo skill.", "", "# Demo Skill\nFollow the demo instructions.")
	payloadDir := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)
	t.Setenv("AGENTLY_DEBUG_TRACE_FILE", filepath.Join(t.TempDir(), "trace.jsonl"))
	t.Setenv("AGENTLY_DEBUG_PAYLOAD_DIR", payloadDir)

	withSkills := newTestAgent("demo")
	model := &skillSequenceModel{outcomes: []llm.GenerateResponse{
		{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}}},
	}}
	rt, err := executor.NewBuilder().
		WithAgentFinder(&skillTestAgentFinder{agent: withSkills}).
		WithModelFinder(&skillTestModelFinder{model: model}).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	out := &agentsvc.QueryOutput{}
	if err := rt.Agent.Query(context.Background(), &agentsvc.QueryInput{
		ConversationID: "conv-visible-skills",
		AgentID:        "coder",
		UserId:         "tester",
		Query:          "hello",
		DisplayQuery:   "hello",
		Agent:          withSkills,
	}, out); err != nil {
		t.Fatalf("Query() error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(payloadDir, "llm-request-conv-visible-skills.json"))
	if err != nil {
		t.Fatalf("read llm-request payload: %v", err)
	}
	var payload struct {
		Options struct {
			Tools []struct {
				Definition struct {
					Name string `json:"name"`
				} `json:"definition"`
			} `json:"tools"`
		} `json:"options"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	var names []string
	for _, item := range payload.Options.Tools {
		names = append(names, item.Definition.Name)
	}
	if !contains(names, "llm/skills:list") || !contains(names, "llm/skills:activate") {
		t.Fatalf("expected skill tools in request, got %v", names)
	}

	noSkills := newTestAgent("demo")
	noSkills.Skills = nil
	model2 := &skillSequenceModel{outcomes: []llm.GenerateResponse{
		{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}}},
	}}
	rt2, err := executor.NewBuilder().
		WithAgentFinder(&skillTestAgentFinder{agent: noSkills}).
		WithModelFinder(&skillTestModelFinder{model: model2}).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	if err := rt2.Agent.Query(context.Background(), &agentsvc.QueryInput{
		ConversationID: "conv-no-skills",
		AgentID:        "coder",
		UserId:         "tester",
		Query:          "hello",
		DisplayQuery:   "hello",
		Agent:          noSkills,
	}, &agentsvc.QueryOutput{}); err != nil {
		t.Fatalf("Query() error: %v", err)
	}
	data2, err := os.ReadFile(filepath.Join(payloadDir, "llm-request-conv-no-skills.json"))
	if err != nil {
		t.Fatalf("read llm-request payload: %v", err)
	}
	var payload2 struct {
		Options struct {
			Tools []struct {
				Definition struct {
					Name string `json:"name"`
				} `json:"definition"`
			} `json:"tools"`
		} `json:"options"`
	}
	if err := json.Unmarshal(data2, &payload2); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	var names2 []string
	for _, item := range payload2.Options.Tools {
		names2 = append(names2, item.Definition.Name)
	}
	if contains(names2, "llm/skills:list") || contains(names2, "llm/skills:activate") {
		t.Fatalf("did not expect skill tools in request, got %v", names2)
	}
}

func TestRuntimeQuery_SameResponseSkillActivation_BlocksDisallowedExec(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "demo", "Demo skill.", "Bash(echo:*) system/exec:execute", "# Demo Skill\nOnly echo is allowed.")
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)
	agent := newTestAgent("demo")
	model := &skillSequenceModel{
		outcomes: []llm.GenerateResponse{
			{
				Choices: []llm.Choice{{
					Message: llm.Message{
						Role: llm.RoleAssistant,
						ToolCalls: []llm.ToolCall{
							{ID: "call-skill", Name: "llm/skills:activate", Arguments: map[string]interface{}{"name": "demo"}},
							{ID: "call-exec", Name: "system/exec:execute", Arguments: map[string]interface{}{"commands": []string{"date"}}},
						},
					},
				}},
			},
			{
				Choices: []llm.Choice{{
					Message: llm.Message{Role: llm.RoleAssistant, Content: "done"},
				}},
			},
		},
	}
	rt, err := executor.NewBuilder().
		WithAgentFinder(&skillTestAgentFinder{agent: agent}).
		WithModelFinder(&skillTestModelFinder{model: model}).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	out := &agentsvc.QueryOutput{}
	if err := rt.Agent.Query(context.Background(), &agentsvc.QueryInput{
		ConversationID: "conv-skill-barrier",
		AgentID:        "coder",
		UserId:         "tester",
		Query:          "run the task",
		DisplayQuery:   "run the task",
		Agent:          agent,
	}, out); err != nil {
		t.Fatalf("Query() error: %v", err)
	}
	conv, err := rt.Conversation.GetConversation(context.Background(), "conv-skill-barrier", apiconv.WithIncludeTranscript(true), apiconv.WithIncludeToolCall(true))
	if err != nil {
		t.Fatalf("GetConversation() error: %v", err)
	}
	if conv == nil {
		t.Fatal("expected conversation")
	}
	found := false
	for _, turn := range conv.GetTranscript() {
		for _, msg := range turn.Message {
			if msg == nil {
				continue
			}
			for _, tm := range msg.ToolMessage {
				if tm == nil || tm.ToolName == nil {
					continue
				}
				toolName := strings.TrimSpace(*tm.ToolName)
				if strings.Contains(toolName, "system/exec") {
					if msg.Content != nil && strings.Contains(strings.ToLower(*msg.Content), "not allowed by active skill constraints") {
						found = true
					}
					if tm.ToolCall != nil && tm.ToolCall.ErrorMessage != nil && strings.Contains(strings.ToLower(*tm.ToolCall.ErrorMessage), "not allowed by active skill constraints") {
						found = true
					}
				}
			}
		}
	}
	if !found {
		t.Fatalf("expected blocked system/exec result in transcript")
	}
}

func TestRuntimeQuery_MultipleActiveSkills_ExposeSourceSkillInLLMRequest(t *testing.T) {
	root := t.TempDir()
	writeSkillWithFrontmatter(t, root, "alpha", map[string]string{
		"description": "Alpha skill.",
		"model":       "openai_gpt-5.4",
		"temperature": "0.2",
	}, "# Alpha Skill\nUse alpha.")
	writeSkillWithFrontmatter(t, root, "beta", map[string]string{
		"description": "Beta skill.",
		"model":       "openai_gpt-5.2",
		"temperature": "0.8",
	}, "# Beta Skill\nUse beta.")
	payloadDir := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)
	t.Setenv("AGENTLY_DEBUG_TRACE_FILE", filepath.Join(t.TempDir(), "trace.jsonl"))
	t.Setenv("AGENTLY_DEBUG_PAYLOAD_DIR", payloadDir)

	agent := newTestAgent("alpha")
	agent.Skills = []string{"alpha", "beta"}
	model := &skillSequenceModel{
		outcomes: []llm.GenerateResponse{
			{
				Choices: []llm.Choice{{
					Message: llm.Message{
						Role: llm.RoleAssistant,
						ToolCalls: []llm.ToolCall{
							{ID: "call-alpha", Name: "llm/skills:activate", Arguments: map[string]interface{}{"name": "alpha"}},
							{ID: "call-beta", Name: "llm/skills:activate", Arguments: map[string]interface{}{"name": "beta"}},
						},
					},
				}},
			},
			{
				Choices: []llm.Choice{{
					Message: llm.Message{Role: llm.RoleAssistant, Content: "done"},
				}},
			},
		},
	}
	rt, err := executor.NewBuilder().
		WithAgentFinder(&skillTestAgentFinder{agent: agent}).
		WithModelFinder(&skillTestModelFinder{
			model: model,
			byID: map[string]llm.Model{
				"openai_gpt-5.2": model,
				"openai_gpt-5.4": model,
			},
		}).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	if err := rt.Agent.Query(context.Background(), &agentsvc.QueryInput{
		ConversationID: "conv-multi-skill-source",
		AgentID:        "coder",
		UserId:         "tester",
		Query:          "run the task",
		DisplayQuery:   "run the task",
		Agent:          agent,
	}, &agentsvc.QueryOutput{}); err != nil {
		t.Fatalf("Query() error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(payloadDir, "llm-request-conv-multi-skill-source.json"))
	if err != nil {
		t.Fatalf("read llm-request payload: %v", err)
	}
	var payload struct {
		Model   string `json:"model"`
		Options struct {
			Metadata map[string]interface{} `json:"metadata"`
		} `json:"options"`
		DebugContext map[string]interface{} `json:"debugContext"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	names, ok := payload.Options.Metadata["activeSkillNames"].([]interface{})
	if !ok || len(names) != 2 || names[0] != "alpha" || names[1] != "beta" {
		t.Fatalf("expected ordered activeSkillNames [alpha beta], got %#v", payload.Options.Metadata)
	}
	debugNames, ok := payload.DebugContext["activeSkillNames"].([]interface{})
	if !ok || len(debugNames) != 2 || debugNames[0] != "alpha" || debugNames[1] != "beta" {
		t.Fatalf("expected debugContext activeSkillNames [alpha beta], got %#v", payload.DebugContext)
	}
	if payload.Options.Metadata["activeSkillSourceName"] != "alpha" {
		t.Fatalf("expected activeSkillSourceName alpha, got %#v", payload.Options.Metadata)
	}
	if payload.DebugContext["activeSkillSourceName"] != "alpha" {
		t.Fatalf("expected debugContext activeSkillSourceName alpha, got %#v", payload.DebugContext)
	}
	if payload.DebugContext["activeSkillModel"] != "openai_gpt-5.4" {
		t.Fatalf("expected debugContext activeSkillModel openai_gpt-5.4, got %#v", payload.DebugContext)
	}
	if payload.Options.Metadata["modelSource"] != "skill.frontmatter" {
		t.Fatalf("expected modelSource skill.frontmatter, got %#v", payload.Options.Metadata)
	}
	if payload.Model != "openai_gpt-5.4" {
		t.Fatalf("expected alpha frontmatter model to win, got %q", payload.Model)
	}
	if payload.Options.Metadata["activeSkillTemperature"] != 0.2 {
		t.Fatalf("expected alpha temperature to win, got %#v", payload.Options.Metadata)
	}
}

func TestRuntimeQuery_ForkSkillActivation_RoutesLLMRequestThroughChildConversation(t *testing.T) {
	root := t.TempDir()
	writeSkillWithFrontmatter(t, root, "demo", map[string]string{
		"description": "Forked skill.",
		"context":     "fork",
	}, "# Demo Skill\nUse the forked instructions.")
	payloadDir := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)
	t.Setenv("AGENTLY_DEBUG_TRACE_FILE", filepath.Join(t.TempDir(), "trace.jsonl"))
	t.Setenv("AGENTLY_DEBUG_PAYLOAD_DIR", payloadDir)
	agent := newTestAgent("demo")
	model := &skillContextModel{
		firstResponse: llm.GenerateResponse{
			Choices: []llm.Choice{{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					ToolCalls: []llm.ToolCall{
						{ID: "call-skill", Name: "llm/skills:activate", Arguments: map[string]interface{}{"name": "demo", "args": "use it"}},
					},
				},
			}},
		},
		childContent:   "child final answer",
		parentFollowup: "parent final answer",
	}
	rt, err := executor.NewBuilder().
		WithAgentFinder(&skillTestAgentFinder{agent: agent}).
		WithModelFinder(&skillTestModelFinder{model: model}).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	out := &agentsvc.QueryOutput{}
	if err := rt.Agent.Query(context.Background(), &agentsvc.QueryInput{
		ConversationID: "conv-skill-fork",
		AgentID:        "coder",
		UserId:         "tester",
		Query:          "run fork skill",
		DisplayQuery:   "run fork skill",
		Agent:          agent,
	}, out); err != nil {
		t.Fatalf("Query() error: %v", err)
	}
	if out.Content != "parent final answer" {
		t.Fatalf("content = %q", out.Content)
	}
	files := waitForPayloadFiles(t, payloadDir, 2)
	foundChild := false
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read payload %s: %v", path, err)
		}
		if payloadContains(data, "# Demo Skill") && payloadContains(data, "Use the forked instructions.") {
			if payloadContains(data, "llm_skills-list") || payloadContains(data, "llm/skills:list") || payloadContains(data, "llm_skills-activate") || payloadContains(data, "llm/skills:activate") {
				t.Fatalf("expected fork child llm-request to avoid recursive skill tools: %s", string(data))
			}
			foundChild = true
			break
		}
	}
	if !foundChild {
		t.Fatalf("expected child llm-request payload to contain loaded fork skill body")
	}
	parentData, err := os.ReadFile(filepath.Join(payloadDir, "llm-request-conv-skill-fork.json"))
	if err != nil {
		t.Fatalf("read parent llm-request payload: %v", err)
	}
	if payloadContains(parentData, "activeSkillNames") {
		t.Fatalf("expected parent fork llm-request to avoid inline active skill state: %s", string(parentData))
	}
}

func TestRuntimeQuery_DetachSkillActivation_RoutesLLMRequestThroughChildConversation(t *testing.T) {
	root := t.TempDir()
	writeSkillWithFrontmatter(t, root, "demo", map[string]string{
		"description": "Detached skill.",
		"context":     "detach",
	}, "# Demo Skill\nUse the detached instructions.")
	payloadDir := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)
	t.Setenv("AGENTLY_DEBUG_TRACE_FILE", filepath.Join(t.TempDir(), "trace.jsonl"))
	t.Setenv("AGENTLY_DEBUG_PAYLOAD_DIR", payloadDir)
	agent := newTestAgent("demo")
	model := &skillContextModel{
		firstResponse: llm.GenerateResponse{
			Choices: []llm.Choice{{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					ToolCalls: []llm.ToolCall{
						{ID: "call-skill", Name: "llm/skills:activate", Arguments: map[string]interface{}{"name": "demo", "args": "use it"}},
					},
				},
			}},
		},
		childContent:   "child detached answer",
		parentFollowup: "parent detached answer",
	}
	rt, err := executor.NewBuilder().
		WithAgentFinder(&skillTestAgentFinder{agent: agent}).
		WithModelFinder(&skillTestModelFinder{model: model}).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	out := &agentsvc.QueryOutput{}
	if err := rt.Agent.Query(context.Background(), &agentsvc.QueryInput{
		ConversationID: "conv-skill-detach",
		AgentID:        "coder",
		UserId:         "tester",
		Query:          "run detach skill",
		DisplayQuery:   "run detach skill",
		Agent:          agent,
	}, out); err != nil {
		t.Fatalf("Query() error: %v", err)
	}
	if out.Content != "parent detached answer" {
		t.Fatalf("content = %q", out.Content)
	}
	files := waitForPayloadFiles(t, payloadDir, 2)
	foundChild := false
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read payload %s: %v", path, err)
		}
		if payloadContains(data, "# Demo Skill") && payloadContains(data, "Use the detached instructions.") {
			if payloadContains(data, "llm_skills-list") || payloadContains(data, "llm/skills:list") || payloadContains(data, "llm_skills-activate") || payloadContains(data, "llm/skills:activate") {
				t.Fatalf("expected detach child llm-request to avoid recursive skill tools: %s", string(data))
			}
			foundChild = true
			break
		}
	}
	if !foundChild {
		t.Fatalf("expected child llm-request payload to contain loaded detach skill body")
	}
	parentData, err := os.ReadFile(filepath.Join(payloadDir, "llm-request-conv-skill-detach.json"))
	if err != nil {
		t.Fatalf("read parent llm-request payload: %v", err)
	}
	if payloadContains(parentData, "activeSkillNames") {
		t.Fatalf("expected parent detach llm-request to avoid inline active skill state: %s", string(parentData))
	}
}

func TestRuntimeQuery_ContextJSON_IsGroupedAndTrimmedInLLMRequest(t *testing.T) {
	root := t.TempDir()
	payloadDir := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)
	t.Setenv("AGENTLY_DEBUG_TRACE_FILE", filepath.Join(t.TempDir(), "trace.jsonl"))
	t.Setenv("AGENTLY_DEBUG_PAYLOAD_DIR", payloadDir)

	agent := &agentmdl.Agent{
		Identity: agentmdl.Identity{ID: "coder", Name: "Coder"},
		ModelSelection: llm.ModelSelection{
			Model: "test-model",
		},
		Prompt: &binding.Prompt{Engine: "go", Text: "Context:\n{{ .ContextJSON }}\n\nTask:\n{{ .Task.Prompt }}"},
		Delegation: &agentmdl.Delegation{
			Enabled:  true,
			MaxDepth: 3,
		},
	}
	model := &skillSequenceModel{outcomes: []llm.GenerateResponse{
		{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}}},
	}}
	rt, err := executor.NewBuilder().
		WithAgentFinder(&skillTestAgentFinder{agent: agent}).
		WithModelFinder(&skillTestModelFinder{model: model}).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	ctx := runtimeprojection.WithState(context.Background())
	state, ok := runtimeprojection.StateFromContext(ctx)
	if !ok {
		t.Fatal("expected projection state")
	}
	state.SetScope("conversation")
	state.HideTurns("turn-1", "turn-2")
	state.HideMessages("msg-1", "msg-2", "msg-3")
	state.SetReason("projection active")
	state.AddTokensFreed(12)

	out := &agentsvc.QueryOutput{}
	err = rt.Agent.Query(ctx, &agentsvc.QueryInput{
		ConversationID: "conv-context-json",
		AgentID:        "coder",
		UserId:         "tester",
		Query:          "analyze repo",
		DisplayQuery:   "analyze repo",
		Agent:          agent,
		Context: map[string]interface{}{
			"workdir":          "/tmp/repo",
			"resolvedWorkdir":  "/tmp/repo",
			"client":           map[string]interface{}{"kind": "web", "platform": "web"},
			"DelegationDepths": map[string]interface{}{"coder": 1},
		},
	}, out)
	if err != nil {
		t.Fatalf("Query() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(payloadDir, "llm-request-conv-context-json.json"))
	if err != nil {
		t.Fatalf("read llm-request payload: %v", err)
	}
	var payload struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	joined := ""
	for _, msg := range payload.Messages {
		joined += msg.Content + "\n"
	}
	if !strings.Contains(joined, `"Runtime"`) {
		t.Fatalf("expected grouped Runtime context: %s", joined)
	}
	if !strings.Contains(joined, `"resolvedWorkdir": "`) {
		t.Fatalf("expected resolvedWorkdir in grouped context: %s", joined)
	}
	if !strings.Contains(joined, `"Delegation"`) {
		t.Fatalf("expected grouped Delegation context: %s", joined)
	}
	if !strings.Contains(joined, `"currentDepth": 1`) {
		t.Fatalf("expected delegation depth summary: %s", joined)
	}
	if !strings.Contains(joined, `"Client"`) {
		t.Fatalf("expected grouped Client context: %s", joined)
	}
	if !strings.Contains(joined, `"kind": "web"`) {
		t.Fatalf("expected client metadata: %s", joined)
	}
	if strings.Contains(joined, `"hiddenTurnIds"`) || strings.Contains(joined, `"hiddenMessageIds"`) {
		t.Fatalf("expected hidden projection ids removed from model-facing context: %s", joined)
	}
	if strings.Contains(joined, `"DelegationDepths"`) {
		t.Fatalf("expected raw delegation depths removed from model-facing context: %s", joined)
	}
}

func TestRuntime_ResourcesReadSkillFile_ActivatesVisibleSkill(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "demo", "Demo skill.", "", "# Demo Skill\nFollow the demo instructions.")
	workspace.SetRoot(root)
	agent := newTestAgent("demo")
	agent.Resources = []*agentmdl.Resource{{ID: "local", URI: root, Role: "user"}}
	model := &skillSequenceModel{outcomes: []llm.GenerateResponse{
		{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}}},
	}}
	rt, err := executor.NewBuilder().
		WithAgentFinder(&skillTestAgentFinder{agent: agent}).
		WithModelFinder(&skillTestModelFinder{model: model}).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	conv := apiconv.NewConversation()
	conv.SetId("conv-skill-read")
	agentID := "coder"
	conv.SetAgentId(agentID)
	if err := rt.Conversation.PatchConversations(context.Background(), conv); err != nil {
		t.Fatalf("PatchConversations() error: %v", err)
	}
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-skill-read")
	result, err := rt.Registry.Execute(ctx, "resources:read", map[string]interface{}{
		"rootId": "local",
		"path":   "skills/demo/SKILL.md",
		"mode":   "text",
	})
	if err != nil {
		t.Fatalf("Registry.Execute() error: %v", err)
	}
	var payload struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !strings.Contains(payload.Content, `Loaded skill "demo"`) {
		t.Fatalf("unexpected result: %q", result)
	}
}

func TestRuntimeQuery_ModelDrivenResourcesReadSkillFile_AffectsNextLLMRequest(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "demo", "Demo skill.", "Bash(echo:*) system/exec:execute", "# Demo Skill\nOnly echo is allowed.")
	payloadDir := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)
	t.Setenv("AGENTLY_DEBUG_TRACE_FILE", filepath.Join(t.TempDir(), "trace.jsonl"))
	t.Setenv("AGENTLY_DEBUG_PAYLOAD_DIR", payloadDir)
	agent := newTestAgent("demo")
	agent.Resources = []*agentmdl.Resource{{ID: "local", URI: root, Role: "user"}}
	model := &skillSequenceModel{
		outcomes: []llm.GenerateResponse{
			{
				Choices: []llm.Choice{{
					Message: llm.Message{
						Role: llm.RoleAssistant,
						ToolCalls: []llm.ToolCall{
							{
								ID:   "call-read-skill",
								Name: "resources:read",
								Arguments: map[string]interface{}{
									"rootId": "local",
									"path":   "skills/demo/SKILL.md",
									"mode":   "text",
								},
							},
						},
					},
				}},
			},
			{
				Choices: []llm.Choice{{
					Message: llm.Message{Role: llm.RoleAssistant, Content: "done"},
				}},
			},
		},
	}
	rt, err := executor.NewBuilder().
		WithAgentFinder(&skillTestAgentFinder{agent: agent}).
		WithModelFinder(&skillTestModelFinder{model: model}).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	if err := rt.Agent.Query(context.Background(), &agentsvc.QueryInput{
		ConversationID: "conv-model-read-skill",
		AgentID:        "coder",
		UserId:         "tester",
		Query:          "use the skill from resources",
		DisplayQuery:   "use the skill from resources",
		Agent:          agent,
	}, &agentsvc.QueryOutput{}); err != nil {
		t.Fatalf("Query() error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(payloadDir, "llm-request-conv-model-read-skill.json"))
	if err != nil {
		t.Fatalf("read llm-request payload: %v", err)
	}
	var payload struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
		Options struct {
			Tools []struct {
				Definition struct {
					Name string `json:"name"`
				} `json:"definition"`
			} `json:"tools"`
			Metadata map[string]interface{} `json:"metadata"`
		} `json:"options"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	foundBody := false
	for _, msg := range payload.Messages {
		if strings.Contains(msg.Content, `Loaded skill "demo"`) {
			foundBody = true
			break
		}
		var readPayload struct {
			Content   string `json:"content"`
			SkillName string `json:"skillName"`
		}
		if err := json.Unmarshal([]byte(msg.Content), &readPayload); err == nil {
			if readPayload.SkillName == "demo" && strings.Contains(readPayload.Content, `Loaded skill "demo"`) {
				foundBody = true
				break
			}
		}
	}
	if !foundBody {
		t.Fatalf("expected activated skill body in next llm-request: %s", string(data))
	}
	var names []string
	for _, item := range payload.Options.Tools {
		names = append(names, item.Definition.Name)
	}
	if !contains(names, "system_exec-execute") && !contains(names, "system/exec:execute") {
		t.Fatalf("expected exec tool in request, got %v", names)
	}
	if contains(names, "system_exec-start") || contains(names, "system/exec:start") || contains(names, "system_exec-cancel") || contains(names, "system/exec:cancel") {
		t.Fatalf("expected narrowed tool list after model-driven resources activation, got %v", names)
	}
	if payload.Options.Metadata == nil {
		t.Fatalf("expected metadata in payload")
	}
	if _, ok := payload.Options.Metadata["activeSkillNames"]; !ok {
		t.Fatalf("expected activeSkillNames in payload metadata: %#v", payload.Options.Metadata)
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
