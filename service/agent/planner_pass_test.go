package agent

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	convmem "github.com/viant/agently-core/internal/service/conversation/memory"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/service/core"
	intakesvc "github.com/viant/agently-core/service/intake"
	toolexec "github.com/viant/agently-core/service/shared/toolexec"
	promptrepo "github.com/viant/agently-core/workspace/repository/prompt"
	tplrepo "github.com/viant/agently-core/workspace/repository/template"
	tplbundlerepo "github.com/viant/agently-core/workspace/repository/templatebundle"
	toolbundlerepo "github.com/viant/agently-core/workspace/repository/toolbundle"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
)

type plannerPassFinder struct {
	model *plannerPassModel
	last  string
}

func (f *plannerPassFinder) Find(_ context.Context, id string) (llm.Model, error) {
	f.last = id
	return f.model, nil
}

type plannerPassModel struct {
	requests []*llm.GenerateRequest
	content  []string
}

func (m *plannerPassModel) Generate(_ context.Context, request *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	m.requests = append(m.requests, request)
	content := `{"strategyFamily":"troubleshoot","baseProfiles":["repo_analysis"],"toolBundles":["analyst-tools"],"templateId":"dashboard","requiredEvidence":["baseline","confirmation"],"executionOrder":["baseline","confirmation"],"finalizationGuards":["confirm-before-final"],"parallelToolCalls":false}`
	if len(m.content) > 0 {
		content = m.content[0]
		m.content = m.content[1:]
	}
	return &llm.GenerateResponse{
		Choices: []llm.Choice{{
			Message: llm.Message{
				Role:    llm.RoleAssistant,
				Content: content,
			},
		}},
	}, nil
}

func (*plannerPassModel) Implements(string) bool { return false }

type plannerControlRegistry struct {
	calls []string
}

func (r *plannerControlRegistry) Definitions() []llm.ToolDefinition {
	return []llm.ToolDefinition{
		{
			Name:        "system/exec:execute",
			Description: "Execute shell commands",
			Parameters:  map[string]interface{}{"type": "object"},
			OutputSchema: map[string]interface{}{
				"type": "object",
			},
		},
	}
}
func (r *plannerControlRegistry) MatchDefinition(pattern string) []*llm.ToolDefinition {
	pattern = strings.TrimSpace(pattern)
	defs := r.Definitions()
	var out []*llm.ToolDefinition
	for i := range defs {
		def := defs[i]
		name := strings.TrimSpace(def.Name)
		canonical := mcpname.Canonical(name)
		switch {
		case pattern == "":
			copy := def
			out = append(out, &copy)
		case strings.EqualFold(pattern, name),
			strings.EqualFold(mcpname.Canonical(pattern), canonical),
			strings.EqualFold(pattern, "system/exec"),
			strings.EqualFold(pattern, "system/exec:*"):
			copy := def
			out = append(out, &copy)
		}
	}
	return out
}
func (r *plannerControlRegistry) GetDefinition(name string) (*llm.ToolDefinition, bool) {
	for _, def := range r.Definitions() {
		candidate := strings.TrimSpace(name)
		if candidate == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(def.Name), candidate) || strings.EqualFold(mcpname.Canonical(strings.TrimSpace(def.Name)), mcpname.Canonical(candidate)) {
			copy := def
			return &copy, true
		}
	}
	return nil, false
}
func (r *plannerControlRegistry) MustHaveTools([]string) ([]llm.Tool, error) { return nil, nil }
func (r *plannerControlRegistry) SetDebugLogger(io.Writer)                   {}
func (r *plannerControlRegistry) Initialize(context.Context)                 {}
func (r *plannerControlRegistry) Execute(_ context.Context, name string, args map[string]interface{}) (string, error) {
	r.calls = append(r.calls, strings.TrimSpace(name))
	switch strings.TrimSpace(name) {
	case "llm/agents:topology":
		return `{"items":[{"id":"steward","plannerAgentId":"steward_planner"}]}`, nil
	case "llm/agents:tool_details":
		var names []string
		if raw, ok := args["names"].([]string); ok {
			names = append(names, raw...)
		} else if raw, ok := args["names"].([]interface{}); ok {
			for _, item := range raw {
				if text, ok := item.(string); ok {
					names = append(names, text)
				}
			}
		}
		payload := map[string]any{"items": []map[string]any{}}
		for _, toolName := range names {
			payload["items"] = append(payload["items"].([]map[string]any), map[string]any{
				"name":        toolName,
				"description": "tool details for " + toolName,
			})
		}
		data, _ := json.Marshal(payload)
		return string(data), nil
	default:
		return "{}", nil
	}
}

func TestPlannerPass_PersistsStructuredGuidanceAndAppliesOutput(t *testing.T) {
	convClient := convmem.New()
	ctx := context.Background()

	conv := apiconv.NewConversation()
	conv.SetId("conv-planner")
	conv.SetAgentId("coder")
	require.NoError(t, convClient.PatchConversations(ctx, conv))

	turn := apiconv.NewTurn()
	turn.SetId("turn-planner")
	turn.SetConversationID("conv-planner")
	turn.SetStatus("running")
	require.NoError(t, convClient.PatchTurn(ctx, turn))

	root := t.TempDir()
	write := func(rel, body string) {
		path := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	write("prompts/repo_analysis.yaml", "id: repo_analysis\nname: Repo Analysis\ndescription: Analyze repository state\n")
	write("tools/bundles/analyst_tools.yaml", "id: analyst-tools\nmatch:\n  - name: system/exec\n")
	write("templates/dashboard.yaml", "id: dashboard\nname: dashboard\ndescription: Dashboard template\n")
	write("templates/bundles/analytics.yaml", "id: analytics-templates\ntemplates:\n  - dashboard\n")
	store := fsstore.New(root)

	model := &plannerPassModel{}
	llmSvc := core.New(&plannerPassFinder{model: model}, nil, convClient)
	svc := &Service{
		llm:                llmSvc,
		conversation:       convClient,
		defaults:           &config.Defaults{},
		registry:           &plannerControlRegistry{},
		promptRepo:         promptrepo.NewWithStore(store),
		toolBundleRepo:     toolbundlerepo.NewWithStore(store),
		templateRepo:       tplrepo.NewWithStore(store),
		templateBundleRepo: tplbundlerepo.NewWithStore(store),
	}

	input := &QueryInput{
		ConversationID: "conv-planner",
		MessageID:      "turn-planner",
		UserId:         "user-1",
		Query:          "take a creative multi-angle approach to this repo failure",
		Agent: &agentmdl.Agent{
			Identity:       agentmdl.Identity{ID: "coder"},
			ModelSelection: llm.ModelSelection{Model: "router-model"},
			Prompt:         &binding.Prompt{Text: "{{ .Task.Query }}"},
			Tool:           agentmdl.Tool{Bundles: []string{"analyst-tools"}},
			Prompts:        agentmdl.PromptAccess{Bundles: []string{"repo_analysis"}},
			Template:       agentmdl.Template{Bundles: []string{"analytics-templates"}},
		},
	}
	ctx = runtimerequestctx.WithTurnMeta(ctx, runtimerequestctx.TurnMeta{
		ConversationID: "conv-planner",
		TurnID:         "turn-planner",
		Assistant:      "coder",
	})
	ctx = runtimerequestctx.WithConversationID(ctx, "conv-planner")

	tc := &intakesvc.Context{
		Routing: intakesvc.RoutingContext{
			Mode:            intakesvc.ModePlanner,
			SelectedAgentID: "coder",
			Source:          intakesvc.SourceWorkspace,
		},
		Planner: intakesvc.PlannerContext{
			Trigger: "creative_phrase",
		},
	}
	out, pctx, err := svc.runPlannerPass(ctx, input, tc)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotNil(t, pctx)
	require.Equal(t, "troubleshoot", out.StrategyFamily)
	require.Equal(t, []string{"analyst-tools"}, out.ToolBundles)
	require.Equal(t, "dashboard", out.TemplateID)

	require.NotEmpty(t, model.requests)
	require.Empty(t, model.requests[0].Options.Tools)
	require.Equal(t, "none", strings.ToLower(strings.TrimSpace(model.requests[0].Options.ToolChoice.Type)))
	require.NotEmpty(t, model.requests[0].Messages)
	require.Contains(t, model.requests[0].Messages[0].Content, "planner mode")
	require.Contains(t, model.requests[0].Messages[0].Content, "Available scenario priors:")
	require.Contains(t, model.requests[0].Messages[0].Content, "repo_analysis")
	joinedPlannerRequest := collectRequestText(model.requests[0])
	require.Contains(t, joinedPlannerRequest, "Planner Control Tool Result")
	require.Contains(t, joinedPlannerRequest, "llm/agents:topology")
	require.Contains(t, joinedPlannerRequest, "llm/agents:tool_details")
	require.True(t, model.requests[0].Options.JSONMode)

	svc.applyPlannerOutput(input, out, pctx)
	require.Equal(t, []string{"analyst-tools"}, input.ToolBundles)
	require.Equal(t, "dashboard", input.TemplateId)
	require.NotNil(t, input.ParallelToolCalls)
	require.False(t, *input.ParallelToolCalls)
	payloadID, err := svc.persistPlannerOutputPayload(ctx, out)
	require.NoError(t, err)
	require.NotEmpty(t, payloadID)

	runTurn := runtimerequestctx.TurnMeta{
		ConversationID: "conv-planner",
		TurnID:         "turn-planner",
		Assistant:      "coder",
	}
	require.NoError(t, svc.persistPlannerGuidance(ctx, &runTurn, input, out, pctx, payloadID))

	bindingState, err := svc.BuildBinding(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, bindingState)
	require.NotEmpty(t, bindingState.SystemDocuments.Items)

	var strategyFound, evidenceFound, guardsFound bool
	for _, doc := range bindingState.SystemDocuments.Items {
		if doc == nil {
			continue
		}
		switch {
		case strings.Contains(doc.PageContent, "StrategyFamily: troubleshoot"):
			strategyFound = true
		case strings.Contains(doc.PageContent, "RequiredEvidence: baseline, confirmation"):
			evidenceFound = true
		case strings.Contains(doc.PageContent, "FinalizationGuards: confirm-before-final"):
			guardsFound = true
		}
	}
	require.True(t, strategyFound)
	require.True(t, evidenceFound)
	require.True(t, guardsFound)

	convView, err := convClient.GetConversation(ctx, "conv-planner", apiconv.WithIncludeTranscript(true))
	require.NoError(t, err)
	require.NotNil(t, convView)
	var persisted *apiconv.Message
	for _, transcriptTurn := range convView.GetTranscript() {
		if transcriptTurn == nil {
			continue
		}
		for _, msg := range transcriptTurn.GetMessages() {
			if msg == nil || msg.Content == nil {
				continue
			}
			if strings.Contains(*msg.Content, "StrategyFamily: troubleshoot") {
				persisted = msg
				break
			}
		}
	}
	require.NotNil(t, persisted)
	require.NotNil(t, persisted.Mode)
	require.Equal(t, toolexec.SystemDocumentMode, *persisted.Mode)
	require.NotNil(t, persisted.Tags)
	require.Contains(t, *persisted.Tags, toolexec.SystemDocumentTag)

	payload, err := convClient.GetPayload(ctx, payloadID)
	require.NoError(t, err)
	require.NotNil(t, payload)
	require.Equal(t, "planner_output", payload.Kind)
	require.Equal(t, "application/json", payload.MimeType)
}

func TestPlannerPass_UsesDedicatedPlannerAgentWhenConfigured(t *testing.T) {
	convClient := convmem.New()
	ctx := context.Background()

	conv := apiconv.NewConversation()
	conv.SetId("conv-planner-agent")
	conv.SetAgentId("coder")
	require.NoError(t, convClient.PatchConversations(ctx, conv))

	turn := apiconv.NewTurn()
	turn.SetId("turn-planner-agent")
	turn.SetConversationID("conv-planner-agent")
	turn.SetStatus("running")
	require.NoError(t, convClient.PatchTurn(ctx, turn))

	root := t.TempDir()
	write := func(rel, body string) {
		path := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	write("prompts/repo_analysis.yaml", "id: repo_analysis\nname: Repo Analysis\ndescription: Analyze repository state\n")
	write("tools/bundles/analyst_tools.yaml", "id: analyst-tools\nmatch:\n  - name: system/exec\n")
	write("templates/dashboard.yaml", "id: dashboard\nname: dashboard\ndescription: Dashboard template\n")
	write("templates/bundles/analytics.yaml", "id: analytics-templates\ntemplates:\n  - dashboard\n")
	store := fsstore.New(root)

	model := &plannerPassModel{}
	finder := &plannerPassFinder{model: model}
	llmSvc := core.New(finder, nil, convClient)
	svc := &Service{
		llm:                llmSvc,
		conversation:       convClient,
		defaults:           &config.Defaults{},
		registry:           &plannerControlRegistry{},
		promptRepo:         promptrepo.NewWithStore(store),
		toolBundleRepo:     toolbundlerepo.NewWithStore(store),
		templateRepo:       tplrepo.NewWithStore(store),
		templateBundleRepo: tplbundlerepo.NewWithStore(store),
		agentFinder: &allAgentFinder{
			items: []*agentmdl.Agent{
				{
					Identity:       agentmdl.Identity{ID: "steward_planner", Name: "Steward Planner"},
					ModelSelection: llm.ModelSelection{Model: "planner-model"},
					Prompt:         &binding.Prompt{Text: "{{ .Task.Query }}"},
					SystemPrompt:   &binding.Prompt{Text: "PLANNER AGENT GUIDANCE"},
					Prompts:        agentmdl.PromptAccess{Bundles: []string{"repo_analysis"}},
					Template:       agentmdl.Template{Bundles: []string{"analytics-templates"}},
				},
			},
		},
	}

	input := &QueryInput{
		ConversationID: "conv-planner-agent",
		MessageID:      "turn-planner-agent",
		UserId:         "user-1",
		Query:          "plan this with the dedicated planner",
		Agent: &agentmdl.Agent{
			Identity:       agentmdl.Identity{ID: "coder"},
			ModelSelection: llm.ModelSelection{Model: "executor-model"},
			Prompt:         &binding.Prompt{Text: "{{ .Task.Query }}"},
			SystemPrompt:   &binding.Prompt{Text: "EXECUTOR GUIDANCE"},
			Tool:           agentmdl.Tool{Bundles: []string{"analyst-tools"}},
			Prompts:        agentmdl.PromptAccess{Bundles: []string{"repo_analysis"}},
			Template:       agentmdl.Template{Bundles: []string{"analytics-templates"}},
			Intake:         agentmdl.Intake{PlannerAgentID: "steward_planner"},
		},
	}
	ctx = runtimerequestctx.WithTurnMeta(ctx, runtimerequestctx.TurnMeta{
		ConversationID: "conv-planner-agent",
		TurnID:         "turn-planner-agent",
		Assistant:      "coder",
	})
	ctx = runtimerequestctx.WithConversationID(ctx, "conv-planner-agent")

	tc := &intakesvc.Context{
		Routing: intakesvc.RoutingContext{
			Mode:            intakesvc.ModePlanner,
			SelectedAgentID: "coder",
			Source:          intakesvc.SourceWorkspace,
		},
		Planner: intakesvc.PlannerContext{
			Trigger: "creative_phrase",
			AgentID: "steward_planner",
		},
	}
	_, _, err := svc.runPlannerPass(ctx, input, tc)
	require.NoError(t, err)
	require.NotEmpty(t, model.requests)
	require.Equal(t, "planner-model", finder.last)

	joined := collectRequestText(model.requests[0])
	require.Contains(t, joined, "PLANNER AGENT GUIDANCE")
	require.NotContains(t, joined, "EXECUTOR GUIDANCE")
	require.Contains(t, joined, "llm/agents:topology")
	require.Contains(t, joined, "llm/agents:tool_details")
}

func TestPlannerPass_RetriesWithValidationFeedback(t *testing.T) {
	convClient := convmem.New()
	ctx := context.Background()

	conv := apiconv.NewConversation()
	conv.SetId("conv-retry")
	conv.SetAgentId("coder")
	require.NoError(t, convClient.PatchConversations(ctx, conv))

	turn := apiconv.NewTurn()
	turn.SetId("turn-retry")
	turn.SetConversationID("conv-retry")
	turn.SetStatus("running")
	require.NoError(t, convClient.PatchTurn(ctx, turn))

	root := t.TempDir()
	write := func(rel, body string) {
		path := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	write("prompts/repo_analysis.yaml", "id: repo_analysis\nname: Repo Analysis\ndescription: Analyze repository state\n")
	store := fsstore.New(root)

	model := &plannerPassModel{
		content: []string{
			`{"strategyFamily":"troubleshoot","baseProfiles":["missing"]}`,
			`{"strategyFamily":"troubleshoot","baseProfiles":["repo_analysis"]}`,
		},
	}
	llmSvc := core.New(&plannerPassFinder{model: model}, nil, convClient)
	svc := &Service{
		llm:          llmSvc,
		conversation: convClient,
		defaults:     &config.Defaults{},
		registry:     &plannerControlRegistry{},
		promptRepo:   promptrepo.NewWithStore(store),
	}

	input := &QueryInput{
		ConversationID: "conv-retry",
		MessageID:      "turn-retry",
		UserId:         "user-1",
		Query:          "plan this",
		Agent: &agentmdl.Agent{
			Identity:       agentmdl.Identity{ID: "coder"},
			ModelSelection: llm.ModelSelection{Model: "router-model"},
			Prompt:         &binding.Prompt{Text: "{{ .Task.Query }}"},
			Prompts:        agentmdl.PromptAccess{Bundles: []string{"repo_analysis"}},
		},
	}
	ctx = runtimerequestctx.WithTurnMeta(ctx, runtimerequestctx.TurnMeta{
		ConversationID: "conv-retry",
		TurnID:         "turn-retry",
		Assistant:      "coder",
	})
	ctx = runtimerequestctx.WithConversationID(ctx, "conv-retry")

	tc := &intakesvc.Context{
		Routing: intakesvc.RoutingContext{
			Mode:            intakesvc.ModePlanner,
			SelectedAgentID: "coder",
			Source:          intakesvc.SourceWorkspace,
		},
		Planner: intakesvc.PlannerContext{Trigger: "low_confidence"},
	}
	out, _, err := svc.runPlannerPass(ctx, input, tc)
	require.NoError(t, err)
	require.Equal(t, "troubleshoot", out.StrategyFamily)
	require.Len(t, model.requests, 2)
	require.Contains(t, model.requests[1].Messages[0].Content, "unknown profile")
}

func TestPlannerPass_ClarifyFailurePublishesAssistantMessage(t *testing.T) {
	convClient := convmem.New()
	ctx := context.Background()

	conv := apiconv.NewConversation()
	conv.SetId("conv-fail")
	conv.SetAgentId("coder")
	require.NoError(t, convClient.PatchConversations(ctx, conv))

	turn := apiconv.NewTurn()
	turn.SetId("turn-fail")
	turn.SetConversationID("conv-fail")
	turn.SetStatus("running")
	require.NoError(t, convClient.PatchTurn(ctx, turn))

	root := t.TempDir()
	write := func(rel, body string) {
		path := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	write("prompts/repo_analysis.yaml", "id: repo_analysis\nname: Repo Analysis\ndescription: Analyze repository state\n")
	store := fsstore.New(root)

	model := &plannerPassModel{
		content: []string{
			`{"strategyFamily":"troubleshoot","baseProfiles":["missing"]}`,
			`{"strategyFamily":"troubleshoot","baseProfiles":["missing"]}`,
		},
	}
	llmSvc := core.New(&plannerPassFinder{model: model}, nil, convClient)
	svc := &Service{
		llm:          llmSvc,
		conversation: convClient,
		defaults:     &config.Defaults{},
		registry:     &plannerControlRegistry{},
		promptRepo:   promptrepo.NewWithStore(store),
	}

	input := &QueryInput{
		ConversationID: "conv-fail",
		MessageID:      "turn-fail",
		UserId:         "user-1",
		Query:          "plan this",
		Agent: &agentmdl.Agent{
			Identity:       agentmdl.Identity{ID: "coder"},
			ModelSelection: llm.ModelSelection{Model: "router-model"},
			Prompt:         &binding.Prompt{Text: "{{ .Task.Query }}"},
			Prompts:        agentmdl.PromptAccess{Bundles: []string{"repo_analysis"}},
			Intake:         agentmdl.Intake{PlannerSecondFailurePolicy: "clarify"},
		},
	}
	ctx = runtimerequestctx.WithTurnMeta(ctx, runtimerequestctx.TurnMeta{
		ConversationID: "conv-fail",
		TurnID:         "turn-fail",
		Assistant:      "coder",
	})
	ctx = runtimerequestctx.WithConversationID(ctx, "conv-fail")

	tc := &intakesvc.Context{
		Routing: intakesvc.RoutingContext{
			Mode:            intakesvc.ModePlanner,
			SelectedAgentID: "coder",
			Source:          intakesvc.SourceWorkspace,
		},
		Planner: intakesvc.PlannerContext{Trigger: "low_confidence"},
	}
	_, _, err := svc.runPlannerPass(ctx, input, tc)
	var handled *plannerHandledError
	require.ErrorAs(t, err, &handled)
	require.Equal(t, "planner.clarify", handled.status)
	require.Contains(t, handled.content, "I need clarification")
	require.Len(t, model.requests, 2)

	convView, convErr := convClient.GetConversation(ctx, "conv-fail", apiconv.WithIncludeTranscript(true))
	require.NoError(t, convErr)
	require.NotNil(t, convView)
	var strategyFound, policyFound bool
	for _, transcriptTurn := range convView.GetTranscript() {
		if transcriptTurn == nil {
			continue
		}
		for _, msg := range transcriptTurn.GetMessages() {
			if msg == nil || msg.Content == nil {
				continue
			}
			switch {
			case strings.Contains(*msg.Content, "Status: failed"):
				strategyFound = true
			case strings.Contains(*msg.Content, "SecondPolicy: clarify"):
				policyFound = true
			}
		}
	}
	require.True(t, strategyFound)
	require.True(t, policyFound)
}

func TestMaybeRunPlannerPass_EmitsEventsAndPayload(t *testing.T) {
	convClient := convmem.New()
	ctx := context.Background()

	conv := apiconv.NewConversation()
	conv.SetId("conv-events")
	conv.SetAgentId("coder")
	require.NoError(t, convClient.PatchConversations(ctx, conv))

	turn := apiconv.NewTurn()
	turn.SetId("turn-events")
	turn.SetConversationID("conv-events")
	turn.SetStatus("running")
	require.NoError(t, convClient.PatchTurn(ctx, turn))

	root := t.TempDir()
	write := func(rel, body string) {
		path := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	write("prompts/repo_analysis.yaml", "id: repo_analysis\nname: Repo Analysis\ndescription: Analyze repository state\n")
	write("tools/bundles/analyst_tools.yaml", "id: analyst-tools\nmatch:\n  - name: system/exec\n")
	write("templates/dashboard.yaml", "id: dashboard\nname: dashboard\ndescription: Dashboard template\n")
	write("templates/bundles/analytics.yaml", "id: analytics-templates\ntemplates:\n  - dashboard\n")
	store := fsstore.New(root)

	model := &plannerPassModel{}
	llmSvc := core.New(&plannerPassFinder{model: model}, nil, convClient)
	bus := streaming.NewMemoryBus(8)
	sub, err := bus.Subscribe(context.Background(), nil)
	require.NoError(t, err)
	defer sub.Close()

	svc := &Service{
		llm:                llmSvc,
		conversation:       convClient,
		defaults:           &config.Defaults{},
		registry:           &plannerControlRegistry{},
		promptRepo:         promptrepo.NewWithStore(store),
		toolBundleRepo:     toolbundlerepo.NewWithStore(store),
		templateRepo:       tplrepo.NewWithStore(store),
		templateBundleRepo: tplbundlerepo.NewWithStore(store),
		streamPub:          bus,
	}

	input := &QueryInput{
		ConversationID: "conv-events",
		MessageID:      "turn-events",
		UserId:         "user-1",
		Query:          "plan this",
		Agent: &agentmdl.Agent{
			Identity:       agentmdl.Identity{ID: "coder"},
			ModelSelection: llm.ModelSelection{Model: "router-model"},
			Prompt:         &binding.Prompt{Text: "{{ .Task.Query }}"},
			Prompts:        agentmdl.PromptAccess{Bundles: []string{"repo_analysis"}},
			Template:       agentmdl.Template{Bundles: []string{"analytics-templates"}},
		},
		Context: map[string]any{
			intakesvc.ContextKey: &intakesvc.Context{
				Routing: intakesvc.RoutingContext{
					Mode:            intakesvc.ModePlanner,
					SelectedAgentID: "coder",
					Source:          intakesvc.SourceWorkspace,
				},
				Planner: intakesvc.PlannerContext{
					Trigger: "creative_phrase",
				},
			},
		},
	}
	ctx = runtimerequestctx.WithTurnMeta(ctx, runtimerequestctx.TurnMeta{
		ConversationID: "conv-events",
		TurnID:         "turn-events",
		Assistant:      "coder",
	})
	ctx = runtimerequestctx.WithConversationID(ctx, "conv-events")

	require.NoError(t, svc.maybeRunPlannerPass(ctx, input))

	events := collectPlannerEvents(t, sub, 3)
	require.Equal(t, streaming.EventTypePlannerSelected, events[0].Type)
	require.Equal(t, "creative_phrase", events[0].PlannerTrigger)
	require.Equal(t, streaming.EventTypePlannerValidated, events[1].Type)
	require.NotNil(t, events[1].PlannerValidated)
	require.True(t, *events[1].PlannerValidated)
	require.Equal(t, streaming.EventTypePlannerOutput, events[2].Type)
	require.Equal(t, "troubleshoot", events[2].PlannerStrategyFamily)
	require.NotEmpty(t, events[2].PlannerOutputPayloadID)

	payload, err := convClient.GetPayload(ctx, events[2].PlannerOutputPayloadID)
	require.NoError(t, err)
	require.NotNil(t, payload)
	require.Equal(t, "planner_output", payload.Kind)
}

func collectPlannerEvents(t *testing.T, sub streaming.Subscription, count int) []*streaming.Event {
	t.Helper()
	var events []*streaming.Event
	timeout := time.After(2 * time.Second)
	for len(events) < count {
		select {
		case ev := <-sub.C():
			if ev != nil {
				events = append(events, ev)
			}
		case <-timeout:
			t.Fatalf("timed out waiting for planner events: got %d want %d", len(events), count)
		}
	}
	return events
}

func collectRequestText(req *llm.GenerateRequest) string {
	if req == nil {
		return ""
	}
	parts := make([]string, 0, len(req.Messages)+1)
	if text := strings.TrimSpace(req.Instructions); text != "" {
		parts = append(parts, text)
	}
	for _, msg := range req.Messages {
		if text := strings.TrimSpace(msg.Content); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}
