package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	convmem "github.com/viant/agently-core/internal/service/conversation/memory"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/service/core"
	intakesvc "github.com/viant/agently-core/service/intake"
	planner "github.com/viant/agently-core/service/planner"
	"github.com/viant/agently-core/service/reactor"
	promptrepo "github.com/viant/agently-core/workspace/repository/prompt"
	tplrepo "github.com/viant/agently-core/workspace/repository/template"
	tplbundlerepo "github.com/viant/agently-core/workspace/repository/templatebundle"
	toolbundlerepo "github.com/viant/agently-core/workspace/repository/toolbundle"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
)

type sequenceFinder struct {
	model *sequenceModel
}

func (f *sequenceFinder) Find(context.Context, string) (llm.Model, error) {
	return f.model, nil
}

type sequenceModel struct {
	mu       sync.Mutex
	requests []*llm.GenerateRequest
	content  []string
}

func (m *sequenceModel) Generate(_ context.Context, request *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, request)
	content := ""
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
			FinishReason: "stop",
		}},
		Model: "mock-model",
	}, nil
}

func (*sequenceModel) Implements(string) bool { return false }

func makePlannerRepos(t *testing.T) (*fsstore.Store, *promptrepo.Repository, *toolbundlerepo.Repository, *tplrepo.Repository, *tplbundlerepo.Repository) {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		path := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	write("prompts/repo_analysis.yaml", "id: repo_analysis\nname: Repo Analysis\ndescription: Analyze repository state\nappliesTo: [repo, debug]\n")
	write("tools/bundles/analyst_tools.yaml", "id: analyst-tools\nmatch:\n  - name: system/exec\n")
	write("templates/dashboard.yaml", "id: dashboard\nname: dashboard\ndescription: Dashboard template\n")
	write("templates/bundles/analytics.yaml", "id: analytics-templates\ntemplates:\n  - dashboard\n")
	store := fsstore.New(root)
	return store,
		promptrepo.NewWithStore(store),
		toolbundlerepo.NewWithStore(store),
		tplrepo.NewWithStore(store),
		tplbundlerepo.NewWithStore(store)
}

func TestQuery_PlannerSuccessRunsTwoModelPassesAndCarriesPlannerDocs(t *testing.T) {
	convClient := convmem.New()
	ctx := context.Background()

	conv := apiconv.NewConversation()
	conv.SetId("conv-query-success")
	require.NoError(t, convClient.PatchConversations(ctx, conv))

	_, promptRepo, toolRepo, templateRepo, templateBundleRepo := makePlannerRepos(t)
	model := &sequenceModel{
		content: []string{
			`{"strategyFamily":"troubleshoot","baseProfiles":["repo_analysis"],"toolBundles":["analyst-tools"],"templateId":"dashboard","requiredEvidence":["baseline","confirmation"],"executionOrder":["baseline","confirmation"],"finalizationGuards":["confirm-before-final"],"parallelToolCalls":false}`,
			`final answer`,
		},
	}
	llmSvc := core.New(&sequenceFinder{model: model}, nil, convClient)
	svc := &Service{
		llm:                llmSvc,
		conversation:       convClient,
		orchestrator:       reactor.New(llmSvc, nil, convClient, nil, nil),
		defaults:           &config.Defaults{},
		registry:           &plannerControlRegistry{},
		promptRepo:         promptRepo,
		toolBundleRepo:     toolRepo,
		templateRepo:       templateRepo,
		templateBundleRepo: templateBundleRepo,
	}

	input := &QueryInput{
		ConversationID: "conv-query-success",
		MessageID:      "turn-query-success",
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
		Context: map[string]any{
			intakesvc.ContextKey: &intakesvc.TurnContext{
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
	output := &QueryOutput{}
	require.NoError(t, svc.Query(ctx, input, output))
	require.Equal(t, "final answer", output.Content)

	require.Len(t, model.requests, 2)
	require.Empty(t, model.requests[0].Options.Tools)
	var firstMessages []string
	for _, msg := range model.requests[0].Messages {
		firstMessages = append(firstMessages, msg.Content)
	}
	firstJoined := strings.Join(firstMessages, "\n")
	require.Contains(t, firstJoined, "planner mode")
	require.Contains(t, firstJoined, "Available scenario priors:")
	require.Contains(t, firstJoined, "repo_analysis")
	require.Contains(t, firstJoined, "llm/agents:topology")
	require.Contains(t, firstJoined, "llm/agents:tool_details")
	pc := planner.FromQueryInput(input)
	require.NotNil(t, pc)
	require.Equal(t, planner.Trigger("creative_phrase"), pc.Trigger)

	var secondMessages []string
	for _, msg := range model.requests[1].Messages {
		secondMessages = append(secondMessages, msg.Content)
	}
	joined := strings.Join(secondMessages, "\n")
	require.Contains(t, joined, "StrategyFamily: troubleshoot")
	require.Contains(t, joined, "RequiredEvidence: baseline, confirmation")

	payload, err := convClient.GetPayload(ctx, "planner-output:conv-query-success:turn-query-success")
	require.NoError(t, err)
	require.NotNil(t, payload)
	require.Equal(t, "planner_output", payload.Kind)
}

func TestQuery_PlannerClarifyShortCircuitsBeforeExecutionPass(t *testing.T) {
	convClient := convmem.New()
	ctx := context.Background()

	conv := apiconv.NewConversation()
	conv.SetId("conv-query-clarify")
	require.NoError(t, convClient.PatchConversations(ctx, conv))

	_, promptRepo, _, _, _ := makePlannerRepos(t)
	model := &sequenceModel{
		content: []string{
			`{"strategyFamily":"troubleshoot","baseProfiles":["missing"]}`,
			`{"strategyFamily":"troubleshoot","baseProfiles":["missing"]}`,
		},
	}
	llmSvc := core.New(&sequenceFinder{model: model}, nil, convClient)
	svc := &Service{
		llm:          llmSvc,
		conversation: convClient,
		orchestrator: reactor.New(llmSvc, nil, convClient, nil, nil),
		defaults:     &config.Defaults{},
		registry:     &plannerControlRegistry{},
		promptRepo:   promptRepo,
	}

	input := &QueryInput{
		ConversationID: "conv-query-clarify",
		MessageID:      "turn-query-clarify",
		UserId:         "user-1",
		Query:          "plan this",
		Agent: &agentmdl.Agent{
			Identity:       agentmdl.Identity{ID: "coder"},
			ModelSelection: llm.ModelSelection{Model: "router-model"},
			Prompt:         &binding.Prompt{Text: "{{ .Task.Query }}"},
			Tool:           agentmdl.Tool{Bundles: []string{"analyst-tools"}},
			Prompts:        agentmdl.PromptAccess{Bundles: []string{"repo_analysis"}},
			Intake:         agentmdl.Intake{PlannerSecondFailurePolicy: "clarify"},
		},
		Context: map[string]any{
			intakesvc.ContextKey: &intakesvc.TurnContext{
				Routing: intakesvc.RoutingContext{
					Mode:            intakesvc.ModePlanner,
					SelectedAgentID: "coder",
					Source:          intakesvc.SourceWorkspace,
				},
				Planner: intakesvc.PlannerContext{
					Trigger: "low_confidence",
				},
			},
		},
	}
	output := &QueryOutput{}
	require.NoError(t, svc.Query(ctx, input, output))
	require.Contains(t, output.Content, "I need clarification")
	require.Len(t, model.requests, 2, "planner should retry once, then stop without an execution pass")

	convView, err := convClient.GetConversation(ctx, "conv-query-clarify", apiconv.WithIncludeTranscript(true))
	require.NoError(t, err)
	require.NotNil(t, convView)
	found := false
	for _, turn := range convView.GetTranscript() {
		if turn == nil {
			continue
		}
		for _, msg := range turn.GetMessages() {
			if msg == nil || msg.Content == nil || msg.Status == nil {
				continue
			}
			if strings.Contains(*msg.Content, "I need clarification") && *msg.Status == "planner.clarify" {
				found = true
			}
		}
	}
	require.True(t, found)
}
