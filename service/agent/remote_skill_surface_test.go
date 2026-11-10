package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	"github.com/viant/agently-core/genai/llm"
	base "github.com/viant/agently-core/genai/llm/provider/base"
	"github.com/viant/agently-core/internal/toolvalidate"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/protocol/mcpname"
	skillproto "github.com/viant/agently-core/protocol/skill"
	"github.com/viant/agently-core/protocol/tool"
	agruntime "github.com/viant/agently-core/runtime"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/core"
	"github.com/viant/agently-core/service/intake"
	"github.com/viant/agently-core/service/reactor"
	skillsvc "github.com/viant/agently-core/service/skill"
)

type remoteSurfaceModel struct {
	calls int
	ctx   context.Context
	tools []llm.Tool
}

func (m *remoteSurfaceModel) Find(context.Context, string) (llm.Model, error) { return m, nil }
func (*remoteSurfaceModel) Implements(feature string) bool                    { return feature == base.CanUseTools }
func (m *remoteSurfaceModel) Generate(ctx context.Context, req *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	m.calls++
	m.ctx = ctx
	m.tools = req.Options.Tools
	return &llm.GenerateResponse{Model: "mock", Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "Complete."}, FinishReason: "stop"}}}, nil
}

// Exercise the real agent loop and model request after a remote skill becomes
// active. Host retention rules and explicit selections are independent of the
// skill's origin; an ordinary cross-provider default is not.
func TestRemoteSkillPreservesIndependentToolsAtModelBoundary(t *testing.T) {
	for _, activation := range []string{"same_iteration", "runtime", "persisted"} {
		t.Run(activation, func(t *testing.T) {
			active := activation != "same_iteration"
			ctx := context.Background()
			store := convmem.New()
			conv := apiconv.NewConversation()
			conv.SetId("remote-surface")
			require.NoError(t, store.PatchConversations(ctx, conv))
			turn := apiconv.NewTurn()
			turn.SetId("turn")
			turn.SetConversationID(conv.Id)
			turn.SetStatus("running")
			require.NoError(t, store.PatchTurn(ctx, turn))
			s := selectionService()
			s.defaults = &config.Defaults{}
			s.conversation = store
			defs := []llm.ToolDefinition{
				{Name: "llm/tools:list", AlwaysInclude: true},
				{Name: "llm/tools:activate", Tier: llm.ToolTierCritical},
				{Name: "other:read", SourceMCPServer: "other"},
				{Name: "other:write", SourceMCPServer: "other"},
				{Name: "source:lookup", SourceMCPServer: "source"},
			}
			s.registry.(*fakeRegistry).defs = defs
			uri := (skillproto.Ref{ServerID: "source", Name: "review", ResourceURI: "skill://example.test/review/SKILL.md"}).URI()
			owner := &exactSkillControlCatalog{item: &skillproto.Skill{CatalogURI: uri, ServerID: "source", Body: "Review the result.", Frontmatter: skillproto.Frontmatter{Name: "review"}}}
			s.skillSvc = skillsvc.New(s.defaults, store, nil)
			s.skillSvc.SetDelivery(skillControlSources{}, owner)
			s.skillSvc.SetToolRegistry(s.registry)
			finder := &remoteSurfaceModel{}
			s.llm = core.New(finder, nil, store)
			s.orchestrator = reactor.New(s.llm, s.registry, store, nil, nil)
			in := selectionInput(&intake.ToolSelection{Names: []string{"other:read"}, Complete: true})
			in.ConversationID = conv.Id
			in.MessageID = "turn"
			in.Query = "Review the result."
			in.UserId = "user"
			in.Agent.Identity = agentmdl.Identity{ID: "agent"}
			in.Agent.ModelSelection = llm.ModelSelection{Model: "mock"}
			in.Agent.Prompt = &binding.Prompt{Text: in.Query}
			in.Agent.Skills = []string{uri}
			// Include a complete authorized working surface without relying on a catalog
			// wildcard: production discovery supplies these through host bundle rules.
			in.Agent.Tool.Items = []*llm.Tool{{Name: "llm/tools:list", AlwaysInclude: true}, {Name: "llm/tools:activate", Tier: llm.ToolTierCritical}, {Name: "other:*"}, {Name: "source:*"}}
			if activation == "runtime" {
				in.Runtime = &agruntime.Context{SkillActivation: &skillproto.ActivationContext{Name: uri, Body: "Review the result.", Mode: "inline"}}
			}
			if activation == "persisted" {
				prior := apiconv.NewTurn()
				prior.SetId("previous-turn")
				prior.SetConversationID(conv.Id)
				prior.SetStatus("completed")
				require.NoError(t, store.PatchTurn(ctx, prior))
				msg := apiconv.NewMessage()
				msg.SetId("prior-activation")
				msg.SetConversationID(conv.Id)
				msg.SetTurnID("previous-turn")
				msg.SetRole("tool")
				msg.SetType("tool_op")
				msg.SetToolName("llm/skills:activate")
				payload, err := json.Marshal(map[string]string{"name": uri, "body": "Review the result.", "mode": "inline"})
				require.NoError(t, err)
				msg.SetContent(string(payload))
				require.NoError(t, store.PatchMessage(ctx, msg))
				tc := apiconv.NewToolCall()
				tc.SetMessageID("prior-activation")
				tc.SetTurnID("previous-turn")
				tc.SetOpID("activate-op")
				tc.SetToolName("llm/skills:activate")
				tc.SetToolKind("general")
				tc.SetStatus("completed")
				require.NoError(t, store.PatchToolCall(ctx, tc))
			}
			ctx = requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{ConversationID: conv.Id, TurnID: "turn"})
			_, err := s.runPlanAndStatus(ctx, in, &QueryOutput{})
			require.NoError(t, err)
			require.Equal(t, 1, finder.calls)
			names := []string{}
			for _, item := range finder.tools {
				names = append(names, item.Definition.Name)
			}
			for _, name := range []string{"llm/tools:list", "llm/tools:activate", "other:read"} {
				require.Contains(t, names, mcpname.Canonical(name))
				require.NoError(t, toolvalidate.ValidateExecution(finder.ctx, nil, name, nil))
			}
			require.NotContains(t, names, mcpname.Canonical("other:write"))
			if active {
				require.Error(t, toolvalidate.ValidateExecution(finder.ctx, nil, "other:write", nil))
			}
			require.Error(t, toolvalidate.ValidateExecution(finder.ctx, &tool.Policy{BlockList: []string{"other:read"}}, "other:read", nil))
			if active {
				require.Error(t, toolvalidate.ValidateExecution(skillsvc.WithConstraints(finder.ctx, &skillsvc.Constraints{RemoteServers: []string{"source"}}), nil, "other:read", nil))
			}
			// If activation happens after the model request in this same iteration,
			// use the captured independent snapshot rather than all prior defaults.
			runtimeState, _ := skillsvc.RuntimeStateFromContext(finder.ctx)
			require.NotNil(t, runtimeState)
			runtimeState.Activate(uri)
			for _, name := range []string{"llm/tools:list", "llm/tools:activate", "other:read"} {
				require.NoError(t, toolvalidate.ValidateExecution(finder.ctx, nil, name, nil))
			}
			require.Error(t, toolvalidate.ValidateExecution(finder.ctx, nil, "other:write", nil))
			revoked := tool.WithResolvedToolNames(finder.ctx, nil)
			require.Error(t, toolvalidate.ValidateExecution(revoked, nil, "other:read", nil))
			owner.readErr = errors.New("synthetic skill authorization revoked")
			require.Error(t, toolvalidate.ValidateExecution(finder.ctx, nil, "other:read", nil))
			require.Error(t, toolvalidate.ValidateExecution(finder.ctx, nil, "llm/tools:list", nil))
		})
	}
}
