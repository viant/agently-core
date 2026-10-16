package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	convmem "github.com/viant/agently-core/internal/service/conversation/memory"
	registry "github.com/viant/agently-core/internal/tool/registry"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/agently-core/protocol/mcpname"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	toolsvc "github.com/viant/agently-core/protocol/tool/service"
	toolservice "github.com/viant/agently-core/protocol/tool/service/llm/tools"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/toolselection"
	"github.com/viant/agently-core/service/intake"
	"github.com/viant/agently-core/service/reactor"
)

type longListToolDescriptions struct{ *discoveryFixture }

func (s *longListToolDescriptions) Methods() toolsvc.Signatures {
	methods := s.discoveryFixture.Methods()
	for i := range methods {
		if methods[i].Name == "report" {
			methods[i].Description = strings.Repeat("Public report metadata. ", 100)
		}
	}
	return methods
}

func TestToolListSelectsUnbundledPageForNextModelAndDurableContinuation(t *testing.T) {
	service, ctx, provider := discoveryService(t)
	require.NoError(t, service.registry.(*registry.Registry).AddInternalService(&longListToolDescriptions{provider}))
	service.toolBundles = func(context.Context) ([]*toolbundle.Bundle, error) {
		return []*toolbundle.Bundle{{ID: "base", Match: []llm.Tool{{Name: "llm/tools:list"}}}}, nil
	}
	store := convmem.New()
	seedConversation(t, store, "conversation")
	assistant := apiconv.NewMessage()
	assistant.SetId("list-parent")
	assistant.SetConversationID("conversation")
	assistant.SetTurnID("turn")
	assistant.SetRole("assistant")
	assistant.SetType("text")
	assistant.SetContent("Discover the report tool")
	require.NoError(t, store.PatchMessage(ctx, assistant))
	ctx = requestctx.WithModelMessageID(ctx, "list-parent")
	service.conversation, service.defaults = store, &config.Defaults{}
	input := selectionInput(&intake.ToolSelection{Names: []string{"llm/tools:list"}, Complete: true})
	input.ConversationID, input.MessageID = "conversation", "turn"
	before, err := service.BuildBinding(ctx, input)
	require.NoError(t, err)
	require.NotContains(t, signatureNames(before.Tools.Signatures), "operations-report")
	runner := reactor.New(nil, service.registry, store, nil, nil)
	plan := runner.PlanFromResponse(&llm.GenerateResponse{Choices: []llm.Choice{{Message: llm.Message{ToolCalls: []llm.ToolCall{{ID: "list-page", Name: "llm/tools:list", Arguments: map[string]interface{}{"query": "operations-report", "limit": 1}}}}}}})
	_, err = runner.ResumePlan(ctx, plan, nil)
	require.NoError(t, err)
	results := runner.TurnToolResults("turn")
	require.Len(t, results, 1)
	require.Empty(t, results[0].Error)
	require.NotEmpty(t, results[0].ResultMessageID)
	require.Greater(t, len(results[0].Result), 200)
	service.defaults.PreviewSettings.ToolResultLimit = 64
	state, _ := toolselection.FromContext(ctx)
	require.Equal(t, []string{"operations-report"}, state.SelectedNames())
	for _, next := range []context.Context{ctx, toolselection.WithState(ctx, nil)} {
		after, err := service.BuildBinding(next, input)
		require.NoError(t, err)
		var previewed bool
		for _, message := range after.History.Current.Messages {
			if message.ID == results[0].ResultMessageID {
				previewed = true
				require.NotEqual(t, results[0].Result, message.Content)
				require.Empty(t, toolSelectionResultNames(mcpname.Canonical("llm/tools:list"), message.Content), "the prompt preview alone cannot prove the returned page")
			}
		}
		require.True(t, previewed)
		require.Contains(t, signatureNames(after.Tools.Signatures), "operations-report", "the returned unbundled tool is available without activate")
		require.NotContains(t, signatureNames(after.Tools.Signatures), "operations-otherReport")
		state, _ := toolselection.FromContext(next)
		require.Equal(t, []string{"operations-report"}, state.SelectedNames())
	}
	require.Zero(t, provider.calls, "list selection and recovery never execute the discovered tool")
	require.Len(t, runner.TurnToolResults("turn"), 1)
}

func selectionHistoryFixture() (context.Context, *binding.History, apiconv.Transcript) {
	ctx := requestctx.WithTurnMeta(context.Background(), requestctx.TurnMeta{TurnID: "turn", ConversationID: "conversation"})
	ctx = toolselection.WithState(ctx, nil)
	turnID := "turn"
	content := `{"names":["catalog-extra","revoked-tool"]}`
	raw := &conversationmodel.MessageView{Id: "activation-message", ConversationId: "conversation", TurnId: &turnID, Role: "tool", Type: "tool_op", Content: &content, CreatedAt: time.Now(), MessageToolCall: &conversationmodel.MessageToolCallView{MessageId: "activation-message", OpId: "activation-op", ToolName: "llm/tools:activate", Status: "completed"}}
	history := &binding.History{CurrentTurnID: "turn", Current: &binding.Turn{ID: "turn", Messages: []*binding.Message{{ID: raw.Id, Kind: binding.MessageKindToolResult, ToolName: "llm/tools:activate", ToolOpID: "activation-op", Content: content}}}}
	return ctx, history, apiconv.Transcript{&apiconv.Turn{Id: "turn", Message: []*conversationmodel.MessageView{raw}}}
}

func TestToolSelectionHistoryRestoresOnlyTrustedCurrentSuccess(t *testing.T) {
	for _, method := range []string{"activate", "list"} {
		t.Run(method, func(t *testing.T) {
			for _, tc := range []struct {
				name   string
				modify func(*binding.History, apiconv.Transcript)
			}{
				{"completed", func(*binding.History, apiconv.Transcript) {}},
				{"failed", func(_ *binding.History, tr apiconv.Transcript) { tr[0].Message[0].MessageToolCall.Status = "failed" }},
				{"pending", func(_ *binding.History, tr apiconv.Transcript) { tr[0].Message[0].MessageToolCall.Status = "running" }},
				{"error", func(_ *binding.History, tr apiconv.Transcript) {
					err := "failed"
					tr[0].Message[0].MessageToolCall.ErrorMessage = &err
				}},
				{"priorTurn", func(h *binding.History, tr apiconv.Transcript) { tr[0].Id = "past"; h.Current.ID = "past" }},
				{"otherConversation", func(_ *binding.History, tr apiconv.Transcript) { tr[0].Message[0].ConversationId = "other" }},
				{"unpersisted", func(h *binding.History, _ apiconv.Transcript) { h.Current.Messages[0].ID = "" }},
				{"wrongOperation", func(h *binding.History, _ apiconv.Transcript) { h.Current.Messages[0].ToolOpID = "different" }},
				{"argsOnly", func(h *binding.History, _ apiconv.Transcript) {
					h.Current.Messages[0].Content = "{}"
					h.Current.Messages[0].ToolArgs = map[string]interface{}{"names": []string{"catalog-extra"}}
				}},
				{"malformed", func(h *binding.History, _ apiconv.Transcript) { h.Current.Messages[0].Content = "{truncated" }},
			} {
				t.Run(tc.name, func(t *testing.T) {
					ctx, h, tr := selectionHistoryFixture()
					if method == "list" {
						content := `{"tools":[{"name":"catalog-extra","description":"Visible returned page"},{"name":"revoked-tool"}],"total":999,"hasMore":true,"nextCursor":"unseen-page"}`
						tr[0].Message[0].Content = &content
						tr[0].Message[0].MessageToolCall.ToolName = "llm/tools:list"
						h.Current.Messages[0].ToolName = "llm/tools:list"
						h.Current.Messages[0].Content = content
					}
					tc.modify(h, tr)
					if tc.name == "argsOnly" || tc.name == "malformed" {
						tr[0].Message[0].Content = &h.Current.Messages[0].Content
					}
					ctx = withToolSelectionHistory(ctx, h, tr)
					state, _ := toolselection.FromContext(ctx)
					state.SetCandidates([]llm.Tool{{Definition: llm.ToolDefinition{Name: "catalog-extra"}}})
					restoreToolSelection(ctx, &QueryInput{}, state)
					if tc.name == "completed" {
						require.Equal(t, []string{"catalog-extra"}, state.SelectedNames())
					} else {
						require.Empty(t, state.SelectedNames())
					}
				})
			}
		})
	}
}

func TestToolListRecoveryDoesNotInferSelectionFromTruncatedRawOutput(t *testing.T) {
	ctx, history, transcript := selectionHistoryFixture()
	broken := `{"tools":[`
	transcript[0].Message[0].Content = &broken
	transcript[0].Message[0].MessageToolCall.ToolName = "llm/tools:list"
	history.Current.Messages[0].ToolName = "llm/tools:list"
	history.Current.Messages[0].Content = `{"tools":[{"name":"catalog-extra"}],"total":99}`
	history.Current.Messages[0].ToolArgs = map[string]interface{}{"query": "catalog-extra"}
	ctx = withToolSelectionHistory(ctx, history, transcript)
	state, _ := toolselection.FromContext(ctx)
	state.SetCandidates([]llm.Tool{{Definition: llm.ToolDefinition{Name: "catalog-extra"}}})
	restoreToolSelection(ctx, &QueryInput{}, state)
	require.Empty(t, state.SelectedNames(), "rediscover a complete page; never infer names from args, total or an unverifiable snapshot")
}

func TestToolSelectionHistoryBuildBindingRestoresContinuationActivation(t *testing.T) {
	ctx, _, tr := selectionHistoryFixture()
	s := selectionService()
	s.defaults = &config.Defaults{}
	s.conversation = &bindingCountingClient{stubConversationClient: &stubConversationClient{}, conv: &apiconv.Conversation{Id: "conversation", Transcript: []*conversationmodel.TranscriptView{(*conversationmodel.TranscriptView)(tr[0])}}}
	in := selectionInput(&intake.ToolSelection{Names: []string{"catalog:read"}, Complete: true})
	in.ConversationID = "conversation"
	in.MessageID = "turn"
	bound, err := s.BuildBinding(ctx, in)
	require.NoError(t, err)
	require.Contains(t, signatureNames(bound.Tools.Signatures), mcpname.Canonical("catalog:extra"))
	state, _ := toolselection.FromContext(ctx)
	require.Equal(t, []string{mcpname.Canonical("catalog:extra")}, state.SelectedNames())
}

func TestToolSelectionHistoryPendingActivationResumesThroughRealExecutor(t *testing.T) {
	ctx := requestctx.WithTurnMeta(context.Background(), requestctx.TurnMeta{TurnID: "turn", ConversationID: "conversation"})
	ctx = toolselection.WithState(ctx, nil)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	reg, err := registry.NewWithManager(mgr)
	require.NoError(t, err)
	require.NoError(t, reg.AddInternalService(&exposureFixture{}))
	require.NoError(t, reg.AddInternalService(toolservice.New()))
	s := &Service{registry: reg}
	// Restart recovery has no intake hint; ordinary authorized binding still
	// seeds candidates before the pending helper enters the existing executor.
	_, err = s.buildToolSignatures(ctx, &QueryInput{Agent: &agentmdl.Agent{Tool: agentmdl.Tool{Items: []*llm.Tool{{Name: "exposure:*"}}}}})
	require.NoError(t, err)
	store := convmem.New()
	seedConversation(t, store, "conversation")
	assistant := apiconv.NewMessage()
	assistant.SetId("assistant")
	assistant.SetConversationID("conversation")
	assistant.SetTurnID("turn")
	assistant.SetRole("assistant")
	assistant.SetType("text")
	assistant.SetContent("activate another tool")
	require.NoError(t, store.PatchMessage(ctx, assistant))
	ctx = requestctx.WithModelMessageID(ctx, "assistant")
	runner := reactor.New(nil, reg, store, nil, nil)
	plan := runner.PlanFromResponse(&llm.GenerateResponse{Choices: []llm.Choice{{Message: llm.Message{ToolCalls: []llm.ToolCall{{ID: "pending-activation", Name: "llm/tools:activate", Arguments: map[string]interface{}{"names": []string{mcpname.Canonical("exposure:read")}}}}}}}})
	_, err = runner.ResumePlan(ctx, plan, nil)
	require.NoError(t, err)
	results := runner.TurnToolResults("turn")
	require.Len(t, results, 1)
	require.Empty(t, results[0].Error)
	state, _ := toolselection.FromContext(ctx)
	require.Equal(t, []string{mcpname.Canonical("exposure:read")}, state.SelectedNames())
	// A fresh continuation context rehydrates from the result written by that
	// real executor, without replaying the completed helper operation.
	fresh := toolselection.WithState(ctx, nil)
	s.conversation, s.defaults = store, &config.Defaults{}
	in := &QueryInput{ConversationID: "conversation", MessageID: "turn", Agent: &agentmdl.Agent{Tool: agentmdl.Tool{Items: []*llm.Tool{{Name: "exposure:*"}, {Name: "llm/tools:list"}}}}, Context: map[string]interface{}{intake.ContextKey: &intake.Context{Prompting: intake.PromptingContext{ToolSelection: &intake.ToolSelection{Names: []string{"llm/tools:list"}, Complete: true}}}}}
	bound, err := s.BuildBinding(fresh, in)
	require.NoError(t, err)
	require.Contains(t, signatureNames(bound.Tools.Signatures), mcpname.Canonical("exposure:read"))
	state, _ = toolselection.FromContext(fresh)
	require.Equal(t, []string{mcpname.Canonical("exposure:read")}, state.SelectedNames())
	require.Len(t, runner.TurnToolResults("turn"), 1)
}
