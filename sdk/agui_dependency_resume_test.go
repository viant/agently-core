package sdk

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/agently-core/genai/llm"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	runmodel "github.com/viant/agently-core/model/run"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/runtime/clienttool"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/agently-core/service/core"
)

type graphCheckpointFinder struct{ calls atomic.Int32 }

func (f *graphCheckpointFinder) Find(context.Context, string) (llm.Model, error) {
	return graphCheckpointModel{f}, nil
}

type graphCheckpointModel struct{ finder *graphCheckpointFinder }

func (m graphCheckpointModel) Implements(string) bool { return false }
func (m graphCheckpointModel) Generate(context.Context, *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	m.finder.calls.Add(1)
	return &llm.GenerateResponse{Model: "parent-model", Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "parent final"}, FinishReason: "stop"}}}, nil
}

type graphCheckpointAgents struct{ agent *agentmdl.Agent }

func (f graphCheckpointAgents) Find(context.Context, string) (*agentmdl.Agent, error) {
	return f.agent, nil
}
func (f graphCheckpointAgents) All() []*agentmdl.Agent { return []*agentmdl.Agent{f.agent} }

func TestAGUIDependencyGraphUsesCompletedChildCheckpointWithoutReexecution(t *testing.T) {
	ctx := recoveryContext()
	server, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(ctx)) })
	conv, err := convservice.New(ctx, server)
	require.NoError(t, err)
	database := data.NewService(server)
	for _, id := range []string{"graph-root", "graph-child"} {
		row := conversation.NewConversation()
		row.SetId(id)
		row.SetCreatedByUserID("owner")
		row.SetVisibility("private")
		if id == "graph-child" {
			row.SetConversationParentId("graph-root")
			row.SetConversationParentTurnId("root-turn")
		}
		require.NoError(t, conv.PatchConversations(ctx, row))
	}
	root := conversation.NewTurn()
	root.SetId("root-turn")
	root.SetConversationID("graph-root")
	root.SetStatus("waiting_for_user")
	root.SetAgentIDUsed("parent")
	require.NoError(t, conv.PatchTurn(ctx, root))
	child := conversation.NewTurn()
	child.SetId("child-turn")
	child.SetConversationID("graph-child")
	child.SetStatus("succeeded")
	require.NoError(t, conv.PatchTurn(ctx, child))
	final := conversation.NewMessage()
	final.SetId("child-answer")
	final.SetConversationID("graph-child")
	final.SetTurnID("child-turn")
	final.SetRole("assistant")
	final.SetType("text")
	final.SetContent("child already finished")
	final.SetCreatedAt(time.Now())
	require.NoError(t, conv.PatchMessage(ctx, final))
	callMessage := conversation.NewMessage()
	callMessage.SetId("original-parent-tool")
	callMessage.SetConversationID("graph-root")
	callMessage.SetTurnID("root-turn")
	callMessage.SetRole("tool")
	callMessage.SetType("tool_op")
	callMessage.SetStatus("waiting_for_user")
	require.NoError(t, conv.PatchMessage(ctx, callMessage))
	call := conversation.NewToolCall()
	call.SetMessageID("original-parent-tool")
	call.SetTurnID("root-turn")
	call.SetOpID("delegate")
	call.SetToolName("llm/agents:run")
	call.SetToolKind("general")
	call.SetStatus("waiting_for_user")
	require.NoError(t, conv.PatchToolCall(ctx, call))
	run := &runmodel.MutableRunView{}
	run.SetId("root-turn")
	run.SetTurnID("root-turn")
	run.SetConversationID("graph-root")
	run.SetStatus("completed")
	run.SetAttempt(1)
	run.SetIteration(1)
	run.SetAgentID("parent")
	run.SetEffectiveUserID("owner")
	run.SetLeaseOwner("old-owner")
	run.SetCompletedAt(time.Now())
	_, err = database.PatchRuns(ctx, []*runmodel.MutableRunView{run})
	require.NoError(t, err)
	finder := &graphCheckpointFinder{}
	llmService := core.New(finder, nil, conv)
	agent := &agentmdl.Agent{Identity: agentmdl.Identity{ID: "parent"}, ModelSelection: llm.ModelSelection{Model: "parent-model"}, Prompt: &binding.Prompt{Text: "finish"}}
	service := agentsvc.New(llmService, graphCheckpointAgents{agent}, nil, nil, &config.Defaults{}, conv, agentsvc.WithDataService(database))
	backend := &backendClient{agent: service, conv: conv, data: database}
	dependency := clienttool.Dependency{ID: "child-turn", ParentCall: clienttool.PendingCall{ID: "delegate", Name: "llm/agents:run", ToolMessageID: "original-parent-tool", ConversationID: "graph-root", TurnID: "root-turn"}, ChildConversationID: "graph-child", ChildTurnID: "child-turn", ChildAgentID: "child", ResultAdapter: clienttool.AgentRunResultV1, Waiting: true}
	session, err := clienttool.NewSession(nil)
	require.NoError(t, err)
	ctx = clienttool.WithSession(ctx, session)
	output := &agentsvc.QueryOutput{}
	require.NoError(t, backend.aguiResumeGraph(ctx, &agentsvc.QueryInput{ConversationID: "graph-root", UserId: "owner"}, "root-turn", aguiPending{Dependencies: []clienttool.Dependency{dependency}}, output))
	require.Equal(t, "parent final", output.Content)
	require.EqualValues(t, 1, finder.calls.Load())
	persisted, err := conv.GetMessage(ctx, "original-parent-tool", conversation.WithIncludeToolCall(true))
	require.NoError(t, err)
	require.Equal(t, "completed", persisted.MessageToolCall.Status)
	require.Contains(t, persisted.GetContent(), "child already finished")
	require.NotContains(t, persisted.GetContent(), "agent")
	// Simulate replay after native root completion but before protocol finalization.
	again := &agentsvc.QueryOutput{}
	require.NoError(t, backend.aguiResumeGraph(ctx, &agentsvc.QueryInput{ConversationID: "graph-root", UserId: "owner"}, "root-turn", aguiPending{Dependencies: []clienttool.Dependency{dependency}}, again))
	require.Equal(t, "parent final", again.Content)
	require.EqualValues(t, 1, finder.calls.Load())
}
