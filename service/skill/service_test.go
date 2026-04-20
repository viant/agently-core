package skill

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	execconfig "github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
)

type testFinder struct{ agent *agentmdl.Agent }

func (f *testFinder) Find(context.Context, string) (*agentmdl.Agent, error) { return f.agent, nil }

type testConversationClient struct {
	conv  *apiconv.Conversation
	convs map[string]*apiconv.Conversation
}

func (c *testConversationClient) GetConversation(_ context.Context, id string, _ ...apiconv.Option) (*apiconv.Conversation, error) {
	if c != nil && len(c.convs) > 0 {
		if conv, ok := c.convs[id]; ok {
			return conv, nil
		}
	}
	return c.conv, nil
}
func (c *testConversationClient) GetConversations(context.Context, *apiconv.Input) ([]*apiconv.Conversation, error) {
	return nil, nil
}
func (c *testConversationClient) PatchConversations(context.Context, *apiconv.MutableConversation) error {
	return nil
}
func (c *testConversationClient) GetPayload(context.Context, string) (*apiconv.Payload, error) {
	return nil, nil
}
func (c *testConversationClient) PatchPayload(context.Context, *apiconv.MutablePayload) error {
	return nil
}
func (c *testConversationClient) PatchMessage(context.Context, *apiconv.MutableMessage) error {
	return nil
}
func (c *testConversationClient) GetMessage(context.Context, string, ...apiconv.Option) (*apiconv.Message, error) {
	return nil, nil
}
func (c *testConversationClient) GetMessageByElicitation(context.Context, string, string) (*apiconv.Message, error) {
	return nil, nil
}
func (c *testConversationClient) PatchModelCall(context.Context, *apiconv.MutableModelCall) error {
	return nil
}
func (c *testConversationClient) PatchToolCall(context.Context, *apiconv.MutableToolCall) error {
	return nil
}
func (c *testConversationClient) PatchTurn(context.Context, *apiconv.MutableTurn) error { return nil }
func (c *testConversationClient) DeleteConversation(context.Context, string) error      { return nil }
func (c *testConversationClient) DeleteMessage(context.Context, string, string) error   { return nil }

func stringPtr(value string) *string { return &value }

func TestService_LoadVisibleAndActivate(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skills", "playwright-cli")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `---
name: playwright-cli
description: Automate browser interactions.
allowed-tools: system/exec:execute
---

# Browser Automation
Use Playwright CLI.
`
	if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := New(&execconfig.Defaults{Skills: execconfig.SkillsDefaults{Roots: []string{filepath.Join(root, "skills")}}}, nil, nil)
	if err := svc.Load(context.Background()); err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	agent := &agentmdl.Agent{Skills: []string{"playwright-cli"}}
	meta, prompt := svc.Visible(agent)
	if len(meta) != 1 || meta[0].Name != "playwright-cli" {
		t.Fatalf("visible skills = %#v", meta)
	}
	if !strings.Contains(prompt, "llm/skills:list") || !strings.Contains(prompt, "llm/skills:activate") {
		t.Fatalf("prompt = %q", prompt)
	}
	if strings.Contains(prompt, skillRoot) {
		t.Fatalf("prompt leaked skill path: %q", prompt)
	}
	body, err := svc.Activate(agent, "playwright-cli", "https://example.com")
	if err != nil {
		t.Fatalf("Activate() error: %v", err)
	}
	if !strings.Contains(body, `Loaded skill "playwright-cli"`) || !strings.Contains(body, "https://example.com") {
		t.Fatalf("activate body = %q", body)
	}
}

func TestService_EmitsRegistryAndActivationEvents(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skills", "demo")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: demo\ndescription: Demo skill.\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	bus := streaming.NewMemoryBus(8)
	sub, err := bus.Subscribe(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	agent := &agentmdl.Agent{Identity: agentmdl.Identity{ID: "coder"}, Skills: []string{"demo"}}
	conv := &apiconv.Conversation{Id: "conv-skill"}
	agentID := "coder"
	conv.AgentId = &agentID
	svc := New(&execconfig.Defaults{Skills: execconfig.SkillsDefaults{Roots: []string{filepath.Join(root, "skills")}}}, &testConversationClient{conv: conv}, &testFinder{agent: agent})
	svc.SetStreamPublisher(bus)
	if err := svc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-skill")
	if _, err := svc.ActivateForConversation(ctx, "conv-skill", "demo", "arg1"); err != nil {
		t.Fatal(err)
	}
	var sawRegistry, sawStarted, sawCompleted bool
	deadline := time.After(2 * time.Second)
	for !(sawRegistry && sawStarted && sawCompleted) {
		select {
		case ev := <-sub.C():
			if ev == nil {
				t.Fatal("subscription closed unexpectedly")
			}
			if ev.Type == streaming.EventTypeSkillRegistryUpdated {
				sawRegistry = true
			}
			if ev.Type == streaming.EventTypeSkillStarted && ev.SkillName == "demo" {
				sawStarted = true
			}
			if ev.Type == streaming.EventTypeSkillCompleted && ev.SkillName == "demo" {
				sawCompleted = true
			}
		case <-deadline:
			t.Fatalf("missing events registry=%v started=%v completed=%v", sawRegistry, sawStarted, sawCompleted)
		}
	}
}

func TestService_EmitsPreprocessStatsOnSkillCompleted(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skills", "demo")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: demo\ndescription: Demo skill.\npreprocess: true\nallowed-tools: Bash(echo:*) system/exec:execute\n---\n\n`!`echo hi`\n"
	if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	bus := streaming.NewMemoryBus(8)
	sub, err := bus.Subscribe(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	agent := &agentmdl.Agent{Identity: agentmdl.Identity{ID: "coder"}, Skills: []string{"demo"}}
	conv := &apiconv.Conversation{Id: "conv-skill"}
	agentID := "coder"
	conv.AgentId = &agentID
	svc := New(&execconfig.Defaults{Skills: execconfig.SkillsDefaults{Roots: []string{filepath.Join(root, "skills")}}}, &testConversationClient{conv: conv}, &testFinder{agent: agent})
	svc.SetStreamPublisher(bus)
	if err := svc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	prev := ExecFn
	ExecFn = func(ctx context.Context, name string, args map[string]interface{}) (string, error) {
		return `{"stdout":"hi","status":0}`, nil
	}
	defer func() { ExecFn = prev }()
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-skill")
	if _, err := svc.ActivateForConversation(ctx, "conv-skill", "demo", ""); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-sub.C():
			if ev == nil {
				t.Fatal("subscription closed unexpectedly")
			}
			if ev.Type == streaming.EventTypeSkillCompleted && ev.SkillName == "demo" {
				pre, _ := ev.Arguments["preprocess"].(map[string]interface{})
				if pre == nil {
					t.Fatalf("expected preprocess arguments, got %#v", ev.Arguments)
				}
				if pre["commandsRun"] != 1 || pre["bytesExpanded"] != 2 {
					t.Fatalf("unexpected preprocess stats %#v", pre)
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for skill_completed preprocess stats")
		}
	}
}

func TestActiveSkillsFromHistory_RecognizesUnderscoreToolName(t *testing.T) {
	history := &binding.History{
		Current: &binding.Turn{
			Messages: []*binding.Message{
				{
					Kind:     binding.MessageKindToolResult,
					ToolName: "llm_skills-activate",
					ToolArgs: map[string]interface{}{"name": "playwright-cli"},
				},
			},
		},
	}
	got := ActiveSkillsFromHistory(history)
	if len(got) != 1 || got[0] != "playwright-cli" {
		t.Fatalf("active skills = %#v", got)
	}
}

func TestService_ActivateForConversation_DetachLaunchesChildConversation(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skills", "demo")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: demo\ndescription: Demo skill.\ncontext: detach\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	agent := &agentmdl.Agent{Identity: agentmdl.Identity{ID: "coder"}, Skills: []string{"demo"}}
	conv := &apiconv.Conversation{Id: "conv-skill"}
	agentID := "coder"
	conv.AgentId = &agentID
	svc := New(&execconfig.Defaults{Skills: execconfig.SkillsDefaults{Roots: []string{filepath.Join(root, "skills")}}}, &testConversationClient{conv: conv}, &testFinder{agent: agent})
	if err := svc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	prev := ExecFn
	defer func() { ExecFn = prev }()
	var calls []struct {
		Name string
		Args map[string]interface{}
	}
	ExecFn = func(ctx context.Context, name string, args map[string]interface{}) (string, error) {
		calls = append(calls, struct {
			Name string
			Args map[string]interface{}
		}{Name: name, Args: args})
		if name != "llm/agents:start" {
			t.Fatalf("unexpected tool call %q", name)
		}
		return `{"conversationId":"child-1","status":"running"}`, nil
	}
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-skill")
	body, err := svc.ActivateForConversation(ctx, "conv-skill", "demo", "arg1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `child-1`) || !strings.Contains(body, `llm/agents:status`) {
		t.Fatalf("body = %q", body)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %#v", calls)
	}
	if got := calls[0].Args["agentId"]; got != "coder" {
		t.Fatalf("agentId = %#v", got)
	}
	if got := calls[0].Args["executionMode"]; got != "detach" {
		t.Fatalf("executionMode = %#v", got)
	}
	if got := calls[0].Args["objective"]; got != "/demo arg1" {
		t.Fatalf("objective = %#v", got)
	}
}

func TestService_ActivateForConversation_ForkWaitsForChildStatus(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skills", "demo")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: demo\ndescription: Demo skill.\ncontext: fork\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	agent := &agentmdl.Agent{Identity: agentmdl.Identity{ID: "coder"}, Skills: []string{"demo"}}
	conv := &apiconv.Conversation{Id: "conv-skill"}
	agentID := "coder"
	conv.AgentId = &agentID
	svc := New(&execconfig.Defaults{Skills: execconfig.SkillsDefaults{Roots: []string{filepath.Join(root, "skills")}}}, &testConversationClient{conv: conv}, &testFinder{agent: agent})
	if err := svc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	prev := ExecFn
	defer func() { ExecFn = prev }()
	var callNames []string
	ExecFn = func(ctx context.Context, name string, args map[string]interface{}) (string, error) {
		callNames = append(callNames, name)
		switch name {
		case "llm/agents:start":
			return `{"conversationId":"child-1","status":"running"}`, nil
		case "llm/agents:status":
			if got := args["conversationId"]; got != "child-1" {
				t.Fatalf("status conversationId = %#v", got)
			}
			return `{"conversationId":"child-1","status":"succeeded","terminal":true}`, nil
		default:
			t.Fatalf("unexpected tool call %q", name)
			return "", nil
		}
	}
	svc.conv = &testConversationClient{convs: map[string]*apiconv.Conversation{
		"conv-skill": conv,
		"child-1": {
			Id: "child-1",
			Transcript: []*agconv.TranscriptView{
				{
					Message: []*agconv.MessageView{
						{
							Role:    "assistant",
							Type:    "text",
							Interim: 0,
							Content: stringPtr("final child answer"),
						},
					},
				},
			},
		},
	}}
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-skill")
	body, err := svc.ActivateForConversation(ctx, "conv-skill", "demo", "arg1")
	if err != nil {
		t.Fatal(err)
	}
	if body != "final child answer" {
		t.Fatalf("body = %q", body)
	}
	if strings.Join(callNames, ",") != "llm/agents:start,llm/agents:status" {
		t.Fatalf("callNames = %#v", callNames)
	}
}

func TestService_ActivateForConversation_ForkPollsStatusUntilTerminal(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skills", "demo")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: demo\ndescription: Demo skill.\ncontext: fork\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	agent := &agentmdl.Agent{Identity: agentmdl.Identity{ID: "coder"}, Skills: []string{"demo"}}
	conv := &apiconv.Conversation{Id: "conv-skill"}
	agentID := "coder"
	conv.AgentId = &agentID
	svc := New(&execconfig.Defaults{Skills: execconfig.SkillsDefaults{Roots: []string{filepath.Join(root, "skills")}}}, &testConversationClient{conv: conv}, &testFinder{agent: agent})
	if err := svc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	prev := ExecFn
	defer func() { ExecFn = prev }()
	statusCalls := 0
	ExecFn = func(ctx context.Context, name string, args map[string]interface{}) (string, error) {
		switch name {
		case "llm/agents:start":
			return `{"conversationId":"child-1","status":"running"}`, nil
		case "llm/agents:status":
			statusCalls++
			if statusCalls == 1 {
				return `{"conversationId":"child-1","status":"running","terminal":false}`, nil
			}
			return `{"conversationId":"child-1","status":"succeeded","terminal":true}`, nil
		default:
			t.Fatalf("unexpected tool call %q", name)
			return "", nil
		}
	}
	svc.conv = &testConversationClient{convs: map[string]*apiconv.Conversation{
		"conv-skill": conv,
		"child-1": {
			Id: "child-1",
			Transcript: []*agconv.TranscriptView{
				{
					Message: []*agconv.MessageView{
						{
							Role:    "assistant",
							Type:    "text",
							Interim: 0,
							Content: stringPtr("terminal child answer"),
						},
					},
				},
			},
		},
	}}
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-skill")
	body, err := svc.ActivateForConversation(ctx, "conv-skill", "demo", "arg1")
	if err != nil {
		t.Fatal(err)
	}
	if body != "terminal child answer" {
		t.Fatalf("body = %q", body)
	}
	if statusCalls != 2 {
		t.Fatalf("statusCalls = %d", statusCalls)
	}
}

func TestInlineActiveSkillsFromHistory_FiltersNonInlineSkills(t *testing.T) {
	root := t.TempDir()
	alphaRoot := filepath.Join(root, "skills", "alpha")
	betaRoot := filepath.Join(root, "skills", "beta")
	if err := os.MkdirAll(alphaRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(betaRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(alphaRoot, "SKILL.md"), []byte("---\nname: alpha\ndescription: Inline.\n---\n\nalpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(betaRoot, "SKILL.md"), []byte("---\nname: beta\ndescription: Detached.\ncontext: detach\n---\n\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := New(&execconfig.Defaults{Skills: execconfig.SkillsDefaults{Roots: []string{filepath.Join(root, "skills")}}}, nil, nil)
	if err := svc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	agent := &agentmdl.Agent{Skills: []string{"alpha", "beta"}}
	history := &binding.History{
		Current: &binding.Turn{
			Messages: []*binding.Message{
				{Kind: binding.MessageKindToolResult, ToolName: "llm/skills:activate", ToolArgs: map[string]interface{}{"name": "alpha"}},
				{Kind: binding.MessageKindToolResult, ToolName: "llm/skills:activate", ToolArgs: map[string]interface{}{"name": "beta"}},
			},
		},
	}
	got := InlineActiveSkillsFromHistory(history, svc, agent)
	if len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("inline active skills = %#v", got)
	}
}

func TestService_ActivateForConversation_DetachPublishesChildConversationArguments(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skills", "demo")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: demo\ndescription: Demo skill.\ncontext: detach\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	bus := streaming.NewMemoryBus(8)
	sub, err := bus.Subscribe(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	agent := &agentmdl.Agent{Identity: agentmdl.Identity{ID: "coder"}, Skills: []string{"demo"}}
	conv := &apiconv.Conversation{Id: "conv-skill"}
	agentID := "coder"
	conv.AgentId = &agentID
	svc := New(&execconfig.Defaults{Skills: execconfig.SkillsDefaults{Roots: []string{filepath.Join(root, "skills")}}}, &testConversationClient{conv: conv}, &testFinder{agent: agent})
	svc.SetStreamPublisher(bus)
	if err := svc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	prev := ExecFn
	defer func() { ExecFn = prev }()
	ExecFn = func(ctx context.Context, name string, args map[string]interface{}) (string, error) {
		return `{"conversationId":"child-1","status":"running"}`, nil
	}
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-skill")
	if _, err := svc.ActivateForConversation(ctx, "conv-skill", "demo", ""); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-sub.C():
			if ev == nil {
				t.Fatal("subscription closed unexpectedly")
			}
			if ev.Type != streaming.EventTypeSkillCompleted || ev.SkillName != "demo" {
				continue
			}
			raw, err := json.Marshal(ev.Arguments)
			if err != nil {
				t.Fatal(err)
			}
			text := string(raw)
			if !strings.Contains(text, `"executionMode":"detach"`) || !strings.Contains(text, `"childConversationId":"child-1"`) {
				t.Fatalf("arguments = %s", text)
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for detach completion event")
		}
	}
}

func TestService_Activate_PreprocessOptInAndAllowedTools(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skills", "demo")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `---
name: demo
description: Demo skill.
preprocess: true
preprocess-timeout: 10
allowed-tools: Bash(echo:*) system/exec:execute
---

Before
` + "`!`echo hi`" + `
After`
	if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := New(&execconfig.Defaults{Skills: execconfig.SkillsDefaults{Roots: []string{filepath.Join(root, "skills")}}}, nil, nil)
	if err := svc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	agent := &agentmdl.Agent{Skills: []string{"demo"}}
	prev := ExecFn
	ExecFn = func(ctx context.Context, name string, args map[string]interface{}) (string, error) {
		return `{"stdout":"hi","status":0}`, nil
	}
	defer func() { ExecFn = prev }()

	body, err := svc.Activate(agent, "demo", "")
	if err != nil {
		t.Fatalf("Activate() error: %v", err)
	}
	if !strings.Contains(body, "hi") {
		t.Fatalf("expected substituted output, got %q", body)
	}
}

func TestService_Activate_PreprocessDeniedByAllowedTools(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skills", "demo")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `---
name: demo
description: Demo skill.
preprocess: true
allowed-tools: Bash(git:*) system/exec:execute
---

` + "`!`gh pr list`" + `
`
	if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := New(&execconfig.Defaults{Skills: execconfig.SkillsDefaults{Roots: []string{filepath.Join(root, "skills")}}}, nil, nil)
	if err := svc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	agent := &agentmdl.Agent{Skills: []string{"demo"}}
	prev := ExecFn
	called := false
	ExecFn = func(ctx context.Context, name string, args map[string]interface{}) (string, error) {
		called = true
		return `{"stdout":"should not run","status":0}`, nil
	}
	defer func() { ExecFn = prev }()

	body, err := svc.Activate(agent, "demo", "")
	if err != nil {
		t.Fatalf("Activate() error: %v", err)
	}
	if called {
		t.Fatalf("expected executor not to be called")
	}
	if !strings.Contains(body, "preprocess: denied by allowed-tools") {
		t.Fatalf("expected denial marker, got %q", body)
	}
}
