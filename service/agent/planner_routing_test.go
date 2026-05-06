package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	"github.com/viant/agently-core/genai/llm"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/service/core"
	intakesvc "github.com/viant/agently-core/service/intake"
)

type plannerRoutingFinder struct {
	model llm.Model
}

func (f *plannerRoutingFinder) Find(context.Context, string) (llm.Model, error) {
	return f.model, nil
}

type plannerRoutingModel struct {
	content string
}

func (m plannerRoutingModel) Generate(context.Context, *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	return &llm.GenerateResponse{
		Choices: []llm.Choice{{
			Message: llm.Message{
				Role:    llm.RoleAssistant,
				Content: m.content,
			},
		}},
	}, nil
}

func (plannerRoutingModel) Implements(string) bool { return false }

func TestResolveTurnRouting_PlannerCarriesWorkspaceTurnContext(t *testing.T) {
	svc := &Service{
		llm: core.New(&plannerRoutingFinder{
			model: plannerRoutingModel{content: `{"action":"planner","agentId":"coder","plannerTrigger":"creative_phrase"}`},
		}, nil, nil),
		agentFinder: &allAgentFinder{
			items: []*agentmdl.Agent{
				{
					Identity:    agentmdl.Identity{ID: "coder", Name: "Coder"},
					Description: "Code agent",
					Profile: &agentmdl.Profile{
						Publish:     true,
						Name:        "Coder",
						Description: "Repository analysis, debugging, and code changes",
					},
					Intake: agentmdl.Intake{PlannerEnabled: true, PlannerAgentID: "steward_planner"},
				},
			},
		},
		defaults: &config.Defaults{
			AgentAutoSelection: config.AgentAutoSelectionDefaults{Model: "router-model"},
		},
	}

	dec, err := svc.resolveTurnRouting(context.Background(), nil, "auto", "take a creative multi-angle approach", "")
	require.NoError(t, err)
	require.NotNil(t, dec)
	require.Equal(t, "coder", dec.AgentID)
	require.Equal(t, "llm_router_planner", dec.RoutingReason)
	require.NotNil(t, dec.WorkspaceTurnContext)
	require.Equal(t, intakesvc.ModePlanner, dec.WorkspaceTurnContext.Mode)
	require.Equal(t, "creative_phrase", dec.WorkspaceTurnContext.PlannerTrigger)
	require.Equal(t, "steward_planner", dec.WorkspaceTurnContext.PlannerAgentID)
	require.Equal(t, intakesvc.SourceWorkspace, dec.WorkspaceTurnContext.Source)
	require.Equal(t, "coder", dec.WorkspaceTurnContext.SelectedAgentID)
}

func TestEnsureAgent_PersistsWorkspacePlannerTurnContext(t *testing.T) {
	svc := &Service{
		llm: core.New(&plannerRoutingFinder{
			model: plannerRoutingModel{content: `{"action":"planner","agentId":"coder","plannerTrigger":"creative_phrase"}`},
		}, nil, nil),
		agentFinder: &allAgentFinder{
			items: []*agentmdl.Agent{
				{
					Identity:    agentmdl.Identity{ID: "coder", Name: "Coder"},
					Description: "Code agent",
					Profile: &agentmdl.Profile{
						Publish:     true,
						Name:        "Coder",
						Description: "Repository analysis, debugging, and code changes",
					},
					Intake: agentmdl.Intake{PlannerEnabled: true, PlannerAgentID: "steward_planner"},
				},
			},
		},
		defaults: &config.Defaults{
			AgentAutoSelection: config.AgentAutoSelectionDefaults{Model: "router-model"},
		},
	}

	input := &QueryInput{
		AgentID: "auto",
		Query:   "take a creative multi-angle approach to repository analysis and debugging",
	}
	require.NoError(t, svc.ensureAgent(context.Background(), input))
	tc := intakesvc.FromContext(input.Context)
	require.NotNil(t, tc)
	require.Equal(t, intakesvc.ModePlanner, tc.Mode)
	require.Equal(t, "creative_phrase", tc.PlannerTrigger)
	require.Equal(t, "steward_planner", tc.PlannerAgentID)
	require.Equal(t, intakesvc.SourceWorkspace, tc.Source)
	require.Equal(t, "coder", tc.SelectedAgentID)
}

func TestEnsureAgent_PlannerDisabledRejectsUnsupportedPlannerAction(t *testing.T) {
	svc := &Service{
		llm: core.New(&plannerRoutingFinder{
			model: plannerRoutingModel{content: `{"action":"planner","agentId":"coder","plannerTrigger":"creative_phrase"}`},
		}, nil, nil),
		agentFinder: &allAgentFinder{
			items: []*agentmdl.Agent{
				{
					Identity:    agentmdl.Identity{ID: "coder", Name: "Coder"},
					Description: "Code agent",
					Profile: &agentmdl.Profile{
						Publish:     true,
						Name:        "Coder",
						Description: "Repository analysis, debugging, and code changes",
					},
					Intake: agentmdl.Intake{PlannerEnabled: false},
				},
			},
		},
		defaults: &config.Defaults{
			AgentAutoSelection: config.AgentAutoSelectionDefaults{Model: "router-model"},
		},
	}

	input := &QueryInput{
		AgentID: "auto",
		Query:   "take a creative multi-angle approach",
	}
	err := svc.ensureAgent(context.Background(), input)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to resolve agent")
	tc := intakesvc.FromContext(input.Context)
	require.Nil(t, tc)
}
