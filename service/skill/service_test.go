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
	conv      *apiconv.Conversation
	convs     map[string]*apiconv.Conversation
	messages  []*apiconv.MutableMessage
	toolCalls []*apiconv.MutableToolCall
	payloads  []*apiconv.MutablePayload
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
func (c *testConversationClient) PatchPayload(_ context.Context, payload *apiconv.MutablePayload) error {
	if c != nil {
		c.payloads = append(c.payloads, payload)
	}
	return nil
}
func (c *testConversationClient) PatchMessage(_ context.Context, message *apiconv.MutableMessage) error {
	if c != nil {
		c.messages = append(c.messages, message)
	}
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
func (c *testConversationClient) PatchToolCall(ctx context.Context, call *apiconv.MutableToolCall) error {
	if c != nil {
		c.toolCalls = append(c.toolCalls, call)
	}
	return nil
}
func (c *testConversationClient) PatchTurn(context.Context, *apiconv.MutableTurn) error { return nil }
func (c *testConversationClient) DeleteConversation(context.Context, string) error      { return nil }
func (c *testConversationClient) DeleteMessage(context.Context, string, string) error   { return nil }

func stringPtr(value string) *string { return &value }
func ptrValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

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
	if meta[0].ExecutionMode != "inline" {
		t.Fatalf("execution mode = %q, want inline", meta[0].ExecutionMode)
	}
	if !strings.Contains(prompt, "llm/skills:list") || !strings.Contains(prompt, "llm/skills:activate") {
		t.Fatalf("prompt = %q", prompt)
	}
	if !strings.Contains(prompt, "playwright-cli (inline)") {
		t.Fatalf("prompt missing execution mode: %q", prompt)
	}
	if !strings.Contains(prompt, "Do not set `input.mode` unless you intentionally need to override") {
		t.Fatalf("prompt missing mode override guidance: %q", prompt)
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
	content := "---\nname: demo\ndescription: Demo skill.\ncontext: inline\n---\n\nbody\n"
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
	content := "---\nname: demo\ndescription: Demo skill.\ncontext: inline\npreprocess: true\nallowed-tools: Bash(echo:*) system/exec:execute\n---\n\n`!`echo hi`\n"
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
	if got := calls[0].Args["agentId"]; got != "coder/demo" {
		t.Fatalf("agentId = %#v", got)
	}
	agentArg, ok := calls[0].Args["agent"].(*agentmdl.Agent)
	if !ok || agentArg == nil {
		t.Fatalf("agent = %#v", calls[0].Args["agent"])
	}
	if agentArg.ID != "coder/demo" {
		t.Fatalf("derived agent id = %q", agentArg.ID)
	}
	if !strings.Contains(agentArg.SystemPrompt.Text, "\"demo\" skill") || !strings.Contains(agentArg.SystemPrompt.Text, "body") {
		t.Fatalf("derived system prompt = %q", agentArg.SystemPrompt.Text)
	}
	if got := calls[0].Args["executionMode"]; got != "detach" {
		t.Fatalf("executionMode = %#v", got)
	}
	if got, _ := calls[0].Args["objective"].(string); !strings.Contains(got, "/demo arg1") || !strings.Contains(got, "Runtime date context:") {
		t.Fatalf("objective = %#v", got)
	}
}

func TestService_ActivateForConversation_DetachUsesSkillAgentID(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skills", "demo")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: demo\ndescription: Demo skill.\ncontext: detach\nagent-id: forecast-skill\n---\n\nbody\n"
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
	if got := calls[0].Args["agentId"]; got != "forecast-skill" {
		t.Fatalf("agentId = %#v", got)
	}
	agentArg, ok := calls[0].Args["agent"].(*agentmdl.Agent)
	if !ok || agentArg == nil {
		t.Fatalf("agent = %#v", calls[0].Args["agent"])
	}
	if agentArg.ID != "forecast-skill" {
		t.Fatalf("derived agent id = %q", agentArg.ID)
	}
}

func TestService_ActivateForConversation_DetachPersistsNestedAgentsStartAndEmitsEvents(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skills", "demo")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: demo\ndescription: Demo skill.\ncontext: detach\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	bus := streaming.NewMemoryBus(16)
	sub, err := bus.Subscribe(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	agent := &agentmdl.Agent{Identity: agentmdl.Identity{ID: "coder"}, Skills: []string{"demo"}}
	conv := &apiconv.Conversation{Id: "conv-skill"}
	agentID := "coder"
	conv.AgentId = &agentID
	rec := &testConversationClient{conv: conv}
	svc := New(&execconfig.Defaults{Skills: execconfig.SkillsDefaults{Roots: []string{filepath.Join(root, "skills")}}}, rec, &testFinder{agent: agent})
	svc.SetStreamPublisher(bus)
	if err := svc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	prev := ExecFn
	defer func() { ExecFn = prev }()
	ExecFn = func(ctx context.Context, name string, args map[string]interface{}) (string, error) {
		if name != "llm/agents:start" {
			t.Fatalf("unexpected tool call %q", name)
		}
		return `{"conversationId":"child-1","status":"running"}`, nil
	}
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-skill")
	ctx = runtimerequestctx.WithTurnMeta(ctx, runtimerequestctx.TurnMeta{
		ConversationID:  "conv-skill",
		TurnID:          "turn-1",
		ParentMessageID: "assistant-msg-1",
	})
	ctx = runtimerequestctx.WithToolMessageID(ctx, "skill-tool-msg-1")
	ctx = runtimerequestctx.WithModelMessageID(ctx, "assistant-msg-1")
	body, err := svc.ActivateForConversation(ctx, "conv-skill", "demo", "arg1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `child-1`) {
		t.Fatalf("body = %q", body)
	}
	var nestedMsg *apiconv.MutableMessage
	for _, msg := range rec.messages {
		if msg == nil {
			continue
		}
		if strings.EqualFold(msg.Type, "tool_op") && ptrValue(msg.ParentMessageID) == "skill-tool-msg-1" {
			nestedMsg = msg
			break
		}
	}
	if nestedMsg == nil {
		t.Fatalf("expected persisted nested tool_op message, got %#v", rec.messages)
	}
	if ptrValue(nestedMsg.ParentMessageID) != "skill-tool-msg-1" {
		t.Fatalf("nested parent message = %q", ptrValue(nestedMsg.ParentMessageID))
	}
	var nestedCall *apiconv.MutableToolCall
	for _, call := range rec.toolCalls {
		if call == nil {
			continue
		}
		if strings.TrimSpace(call.MessageID) == strings.TrimSpace(nestedMsg.Id) && strings.TrimSpace(call.OpID) != "" {
			nestedCall = call
		}
	}
	if nestedCall == nil {
		t.Fatalf("expected persisted nested tool call, got %#v", rec.toolCalls)
	}
	if len(rec.payloads) < 2 {
		t.Fatalf("expected request/response payload persistence, got %#v", rec.payloads)
	}
	var sawStarted, sawCompleted, sawLinked bool
	deadline := time.After(2 * time.Second)
	for !(sawStarted && sawCompleted && sawLinked) {
		select {
		case ev := <-sub.C():
			if ev == nil || strings.TrimSpace(ev.ToolCallID) != strings.TrimSpace(nestedCall.OpID) {
				continue
			}
			switch ev.Type {
			case streaming.EventTypeToolCallStarted:
				if ev.ParentMessageID == "skill-tool-msg-1" && ev.ToolMessageID == nestedMsg.Id {
					sawStarted = true
				}
			case streaming.EventTypeToolCallCompleted:
				if ev.ParentMessageID == "skill-tool-msg-1" && ev.ToolMessageID == nestedMsg.Id {
					sawCompleted = true
				}
			case streaming.EventTypeLinkedConversationAttached:
				if ev.ParentMessageID == "skill-tool-msg-1" && ev.ToolMessageID == nestedMsg.Id && ev.LinkedConversationID == "child-1" {
					sawLinked = true
				}
			}
		case <-deadline:
			t.Fatalf("missing nested events started=%v completed=%v linked=%v", sawStarted, sawCompleted, sawLinked)
		}
	}
}

func TestService_activate_DetachReturnsStructuredChildState(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skills", "demo")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: demo\ndescription: Demo skill.\ncontext: detach\nagent-id: forecast-skill\n---\n\nbody\n"
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
	ExecFn = func(ctx context.Context, name string, args map[string]interface{}) (string, error) {
		if name != "llm/agents:start" {
			t.Fatalf("unexpected tool call %q", name)
		}
		return `{"conversationId":"child-1","status":"running"}`, nil
	}
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-skill")
	in := &ActivateInput{Name: "demo"}
	out := &ActivateOutput{}
	if err := svc.activate(ctx, in, out); err != nil {
		t.Fatal(err)
	}
	if out.Mode != "detach" {
		t.Fatalf("mode = %q", out.Mode)
	}
	if !out.Started {
		t.Fatalf("started = %#v", out.Started)
	}
	if out.Terminal {
		t.Fatalf("terminal = %#v", out.Terminal)
	}
	if out.Status != "running" {
		t.Fatalf("status = %q", out.Status)
	}
	if out.ChildConversationID != "child-1" {
		t.Fatalf("childConversationId = %q", out.ChildConversationID)
	}
	if out.ChildAgentID != "forecast-skill" {
		t.Fatalf("childAgentId = %q", out.ChildAgentID)
	}
}

func TestService_activate_ModeOverride_InlineWinsOverDetachSkill(t *testing.T) {
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
	ExecFn = func(ctx context.Context, name string, args map[string]interface{}) (string, error) {
		t.Fatalf("unexpected tool call %q", name)
		return "", nil
	}
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-skill")
	out := &ActivateOutput{}
	if err := svc.activate(ctx, &ActivateInput{Name: "demo", Mode: "inline"}, out); err != nil {
		t.Fatal(err)
	}
	if out.Mode != "inline" {
		t.Fatalf("mode = %q", out.Mode)
	}
	if !out.Terminal {
		t.Fatalf("terminal = %#v", out.Terminal)
	}
	if out.ChildConversationID != "" {
		t.Fatalf("childConversationId = %q", out.ChildConversationID)
	}
}

func TestService_activate_ModelToolCallCannotOverrideDetachSkillToInline(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skills", "demo")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: demo\ndescription: Demo skill.\ncontext: detach\nagent-id: forecast-skill\n---\n\nbody\n"
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
	ExecFn = func(ctx context.Context, name string, args map[string]interface{}) (string, error) {
		if name != "llm/agents:start" {
			t.Fatalf("unexpected tool call %q", name)
		}
		return `{"conversationId":"child-1","status":"running"}`, nil
	}
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-skill")
	ctx = runtimerequestctx.WithToolMessageID(ctx, "tool-msg-1")
	out := &ActivateOutput{}
	if err := svc.activate(ctx, &ActivateInput{Name: "demo", Mode: "inline"}, out); err != nil {
		t.Fatal(err)
	}
	if out.Mode != "detach" {
		t.Fatalf("mode = %q", out.Mode)
	}
	if !out.Started {
		t.Fatalf("started = %#v", out.Started)
	}
	if out.ChildConversationID != "child-1" {
		t.Fatalf("childConversationId = %q", out.ChildConversationID)
	}
}

func TestService_activate_ForkFallsBackToParentUserTaskWhenArgsMissing(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skills", "demo")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: demo\ndescription: Demo skill.\ncontext: fork\nagent-id: forecast-skill\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	agent := &agentmdl.Agent{Identity: agentmdl.Identity{ID: "coder"}, Skills: []string{"demo"}}
	agentID := "coder"
	conv := &apiconv.Conversation{
		Id:      "conv-skill",
		AgentId: &agentID,
		Transcript: []*agconv.TranscriptView{{
			Id: "turn-1",
			Message: []*agconv.MessageView{{
				Id:      "user-1",
				Role:    "user",
				Type:    "text",
				Content: stringPtr("run forecast for audience 7268995"),
			}},
		}},
	}
	svc := New(&execconfig.Defaults{Skills: execconfig.SkillsDefaults{Roots: []string{filepath.Join(root, "skills")}}}, &testConversationClient{conv: conv}, &testFinder{agent: agent})
	if err := svc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	prev := ExecFn
	defer func() { ExecFn = prev }()
	var startObjective string
	ExecFn = func(ctx context.Context, name string, args map[string]interface{}) (string, error) {
		switch name {
		case "llm/agents:start":
			startObjective, _ = args["objective"].(string)
			return `{"conversationId":"child-1","status":"running"}`, nil
		case "llm/agents:status":
			return `{"conversationId":"child-1","status":"succeeded","terminal":true}`, nil
		default:
			t.Fatalf("unexpected tool call %q", name)
			return "", nil
		}
	}
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-skill")
	ctx = runtimerequestctx.WithToolMessageID(ctx, "tool-msg-1")
	out := &ActivateOutput{}
	if err := svc.activate(ctx, &ActivateInput{Name: "demo"}, out); err != nil {
		t.Fatal(err)
	}
	if out.Mode != "fork" {
		t.Fatalf("mode = %q", out.Mode)
	}
	if strings.TrimSpace(startObjective) != "run forecast for audience 7268995" {
		t.Fatalf("objective = %q", startObjective)
	}
}

func TestService_activate_ModeOverride_ForkWinsOverInlineSkill(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skills", "demo")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: demo\ndescription: Demo skill.\ncontext: inline\n---\n\nbody\n"
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
	var names []string
	ExecFn = func(ctx context.Context, name string, args map[string]interface{}) (string, error) {
		names = append(names, name)
		switch name {
		case "llm/agents:start":
			return `{"conversationId":"child-1","status":"running"}`, nil
		case "llm/agents:status":
			return `{"conversationId":"child-1","status":"succeeded","terminal":true}`, nil
		default:
			t.Fatalf("unexpected tool call %q", name)
			return "", nil
		}
	}
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-skill")
	out := &ActivateOutput{}
	if err := svc.activate(ctx, &ActivateInput{Name: "demo", Mode: "fork"}, out); err != nil {
		t.Fatal(err)
	}
	if out.Mode != "fork" {
		t.Fatalf("mode = %q", out.Mode)
	}
	if !out.Terminal {
		t.Fatalf("terminal = %#v", out.Terminal)
	}
	if out.ChildConversationID != "child-1" {
		t.Fatalf("childConversationId = %q", out.ChildConversationID)
	}
	if strings.Join(names, ",") != "llm/agents:start,llm/agents:status" {
		t.Fatalf("callNames = %#v", names)
	}
}

func TestService_activate_ModeOverride_DetachWinsOverInlineSkill(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skills", "demo")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: demo\ndescription: Demo skill.\ncontext: inline\n---\n\nbody\n"
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
	ExecFn = func(ctx context.Context, name string, args map[string]interface{}) (string, error) {
		if name != "llm/agents:start" {
			t.Fatalf("unexpected tool call %q", name)
		}
		return `{"conversationId":"child-1","status":"running"}`, nil
	}
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-skill")
	out := &ActivateOutput{}
	if err := svc.activate(ctx, &ActivateInput{Name: "demo", Mode: "detach"}, out); err != nil {
		t.Fatal(err)
	}
	if out.Mode != "detach" {
		t.Fatalf("mode = %q", out.Mode)
	}
	if !out.Started {
		t.Fatalf("started = %#v", out.Started)
	}
	if out.Terminal {
		t.Fatalf("terminal = %#v", out.Terminal)
	}
	if out.ChildConversationID != "child-1" {
		t.Fatalf("childConversationId = %q", out.ChildConversationID)
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
	if err := os.WriteFile(filepath.Join(alphaRoot, "SKILL.md"), []byte("---\nname: alpha\ndescription: Inline.\ncontext: inline\n---\n\nalpha\n"), 0o644); err != nil {
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
	got := InlineActiveSkillsFromHistory(history, svc, agent, "", "")
	if len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("inline active skills = %#v", got)
	}
}

func TestInlineActiveSkillsFromHistory_RespectsInlineOverride(t *testing.T) {
	root := t.TempDir()
	betaRoot := filepath.Join(root, "skills", "beta")
	if err := os.MkdirAll(betaRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(betaRoot, "SKILL.md"), []byte("---\nname: beta\ndescription: Fork by default.\ncontext: fork\n---\n\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := New(&execconfig.Defaults{Skills: execconfig.SkillsDefaults{Roots: []string{filepath.Join(root, "skills")}}}, nil, nil)
	if err := svc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	agent := &agentmdl.Agent{Skills: []string{"beta"}}
	history := &binding.History{
		Current: &binding.Turn{
			Messages: []*binding.Message{
				{Kind: binding.MessageKindToolResult, ToolName: "llm/skills:activate", ToolArgs: map[string]interface{}{"name": "beta"}},
			},
		},
	}
	got := InlineActiveSkillsFromHistory(history, svc, agent, "beta", "inline")
	if len(got) != 1 || got[0] != "beta" {
		t.Fatalf("inline active skills with override = %#v", got)
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
