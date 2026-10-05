package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/genai/llm/provider"
	"github.com/viant/agently-core/genai/llm/provider/base"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	agentproto "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	messagetool "github.com/viant/agently-core/protocol/tool/service/message"
	"github.com/viant/agently-core/runtime/recovery"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	core "github.com/viant/agently-core/service/core"
	modelcall "github.com/viant/agently-core/service/core/modelcall"
	"github.com/viant/agently-core/service/reactor"
)

type proactiveTestModel struct {
	tokens, counts, calls, compactions int
	countErr                           error
	generationErr                      error
	stream                             bool
}

func (m *proactiveTestModel) CountInputTokens(context.Context, *llm.GenerateRequest) (int, error) {
	m.counts++
	return m.tokens, m.countErr
}
func (m *proactiveTestModel) Implements(feature string) bool {
	return feature == base.CanStream && m.stream
}
func (m *proactiveTestModel) Generate(ctx context.Context, req *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	m.calls++
	if scope := recovery.ProactiveScope(ctx); scope != nil {
		if req.Options == nil || req.Options.ToolChoice.Function == nil || req.Options.ToolChoice.Function.Name != "message-remove" {
			return nil, fmt.Errorf("proactive compaction must require the removal tool")
		}
		m.compactions++
		ids := []string{}
		for id := range scope.EligibleIDs {
			ids = append(ids, id)
		}
		call := llm.NewToolCall(fmt.Sprintf("compact-%d", m.compactions), "message-remove", map[string]interface{}{"tuples": []interface{}{map[string]interface{}{"messageIds": ids, "summary": "Preserved account 7180287 and completed operations; continue from checkpoint.", "role": "assistant"}}}, "")
		return &llm.GenerateResponse{ResponseID: "fresh", Choices: []llm.Choice{{Message: llm.NewAssistantMessageWithToolCalls(call), FinishReason: "tool_calls"}}}, nil
	}
	if m.generationErr != nil {
		return nil, m.generationErr
	}
	requireNoStale := req.PreviousResponseID
	if requireNoStale != "" {
		return nil, fmt.Errorf("stale continuation")
	}
	return &llm.GenerateResponse{ResponseID: "fresh", Choices: []llm.Choice{{Message: llm.NewTextMessage(llm.RoleAssistant, "continue correctly"), FinishReason: "stop"}}}, nil
}
func (m *proactiveTestModel) Stream(ctx context.Context, req *llm.GenerateRequest) (<-chan llm.StreamEvent, error) {
	response, err := m.Generate(ctx, req)
	if err != nil {
		return nil, err
	}
	if observer := modelcall.ObserverFromContext(ctx); observer != nil {
		var err error
		ctx, err = observer.OnCallStart(ctx, modelcall.Info{Provider: "mock", Model: "nano", LLMRequest: req, StartedAt: time.Now()})
		if err != nil {
			return nil, err
		}
		if err = observer.OnCallEnd(ctx, modelcall.Info{Provider: "mock", Model: "nano", LLMResponse: response, CompletedAt: time.Now(), FinishReason: response.Choices[0].FinishReason}); err != nil {
			return nil, err
		}
	}
	ch := make(chan llm.StreamEvent, 1)
	ch <- llm.StreamEvent{Response: response}
	close(ch)
	return ch, nil
}

type proactiveTestFinder struct {
	model    *proactiveTestModel
	capacity int
}

func (f proactiveTestFinder) Find(context.Context, string) (llm.Model, error) { return f.model, nil }
func (f proactiveTestFinder) ConfigByIDOrModel(string) *provider.Config {
	return &provider.Config{Options: provider.Options{ContextWindow: f.capacity}}
}

type proactiveTestRegistry struct {
	store   apiconv.Client
	effects int
}

func (r *proactiveTestRegistry) Definitions() []llm.ToolDefinition {
	return []llm.ToolDefinition{{Name: "message-remove"}}
}
func (r *proactiveTestRegistry) MatchDefinition(string) []*llm.ToolDefinition {
	return []*llm.ToolDefinition{{Name: "message-remove"}}
}
func (r *proactiveTestRegistry) GetDefinition(string) (*llm.ToolDefinition, bool) {
	return &llm.ToolDefinition{Name: "message-remove"}, true
}
func (r *proactiveTestRegistry) MustHaveTools([]string) ([]llm.Tool, error) { return nil, nil }
func (r *proactiveTestRegistry) SetDebugLogger(io.Writer)                   {}
func (r *proactiveTestRegistry) Initialize(context.Context)                 {}
func (r *proactiveTestRegistry) Execute(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	r.effects++
	raw, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	in := &messagetool.RemoveInput{}
	if err = json.Unmarshal(raw, in); err != nil {
		return "", err
	}
	run, err := messagetool.New(r.store).Method("remove")
	if err != nil {
		return "", err
	}
	out := &messagetool.RemoveOutput{}
	if err = run(ctx, in, out); err != nil {
		return "", err
	}
	raw, err = json.Marshal(out)
	return string(raw), err
}

func proactiveFixture(t *testing.T, stream bool, tokens, capacity int) (*Service, *QueryInput, *proactiveTestModel, *proactiveTestRegistry, context.Context, func() *core.GenerateInput) {
	t.Helper()
	store := convmem.New()
	ctx := requestctx.WithConversationID(context.Background(), "proactive-conv")
	meta := requestctx.TurnMeta{ConversationID: "proactive-conv", TurnID: "proactive-turn"}
	ctx = requestctx.WithTurnMeta(ctx, meta)
	conv := apiconv.NewConversation()
	conv.SetId(meta.ConversationID)
	require.NoError(t, store.PatchConversations(ctx, conv))
	turn := apiconv.NewTurn()
	turn.SetId(meta.TurnID)
	turn.SetConversationID(meta.ConversationID)
	require.NoError(t, store.PatchTurn(ctx, turn))
	_, err := apiconv.AddMessage(ctx, store, &meta, apiconv.WithRole("user"), apiconv.WithType("text"), apiconv.WithStatus("completed"), apiconv.WithContent("account 7180287 older facts"))
	require.NoError(t, err)
	_, err = apiconv.AddMessage(ctx, store, &meta, apiconv.WithRole("user"), apiconv.WithType("text"), apiconv.WithStatus("completed"), apiconv.WithContent("continue latest request"))
	require.NoError(t, err)
	model := &proactiveTestModel{tokens: tokens, stream: stream}
	finder := proactiveTestFinder{model: model, capacity: capacity}
	reg := &proactiveTestRegistry{store: store}
	llmSvc := core.New(finder, reg, store)
	svc := &Service{llm: llmSvc, conversation: store}
	makeInput := func() *core.GenerateInput {
		conv, err := store.GetConversation(ctx, meta.ConversationID, apiconv.WithIncludeToolCall(true))
		require.NoError(t, err)
		hist, err := svc.buildHistory(ctx, conv.GetTranscript())
		require.NoError(t, err)
		return &core.GenerateInput{ModelSelection: llm.ModelSelection{Model: "nano", Options: &llm.Options{}}, UserID: "test-user", Prompt: &binding.Prompt{Text: "continue latest request"}, Binding: &binding.Binding{History: hist}}
	}
	svc.orchestrator = reactor.New(llmSvc, reg, store, nil, func(context.Context, *apiconv.Conversation, string) (*core.GenerateInput, error) {
		return makeInput(), nil
	})
	percent := 1.0
	query := &QueryInput{ConversationID: meta.ConversationID, Agent: &agentproto.Agent{ContextCompactionPercent: &percent}}
	return svc, query, model, reg, ctx, makeInput
}

func TestProactiveCompaction_OptOutZeroCalls(t *testing.T) {
	svc, query, model, reg, ctx, makeInput := proactiveFixture(t, false, 9000, 10000)
	query.Agent.ContextCompactionPercent = nil
	gen := makeInput()
	before := len(gen.Message)
	_, _, err := svc.prepareProactiveCompaction(ctx, query, gen, nil)
	require.NoError(t, err)
	require.Zero(t, model.counts)
	require.Zero(t, model.calls)
	require.Zero(t, reg.effects)
	require.Equal(t, before, len(gen.Message))
	// Off even works without the counting/model/conversation services installed.
	_, _, err = (&Service{}).prepareProactiveCompaction(context.Background(), &QueryInput{Agent: &agentproto.Agent{}}, nil, nil)
	require.NoError(t, err)
}
func TestProactiveCompaction_BelowEqualAboveAndBothPipelines(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, tokens := range []int{99, 100, 101, 9000} {
			t.Run(fmt.Sprintf("stream_%v_tokens_%d", stream, tokens), func(t *testing.T) {
				svc, query, model, reg, ctx, makeInput := proactiveFixture(t, stream, tokens, 10000)
				gen := makeInput()
				next, _, err := svc.prepareProactiveCompaction(ctx, query, gen, nil)
				require.NoError(t, err)
				expected := 0
				if tokens >= 100 {
					expected = 1
				}
				require.Equal(t, expected, model.compactions)
				require.Equal(t, expected, reg.effects)
				if expected == 1 {
					require.Equal(t, 2, model.counts)
					require.True(t, recovery.FullHistoryRequired(next))
					require.Contains(t, fmt.Sprint(gen.Binding.History.LLMMessages()), "7180287")
				}
				// A percentage still above threshold is allowed after the bounded pass.
				_, _, err = svc.prepareProactiveCompaction(next, query, makeInput(), nil)
				require.NoError(t, err)
				require.Equal(t, expected, model.compactions)
				_, err = svc.orchestrator.Run(next, gen, &core.GenerateOutput{})
				require.NoError(t, err)
				require.Equal(t, expected+1, model.calls)
				require.Equal(t, expected, reg.effects)
			})
		}
	}
}
func TestProactiveCompaction_CapacityAndCountFailure(t *testing.T) {
	for _, capacity := range []int{0, -1} {
		svc, q, m, _, ctx, input := proactiveFixture(t, false, 100, capacity)
		_, _, err := svc.prepareProactiveCompaction(ctx, q, input(), nil)
		require.ErrorContains(t, err, "contextWindow")
		require.Zero(t, m.counts)
	}
	svc, q, m, r, ctx, input := proactiveFixture(t, false, 100, 10000)
	m.countErr = fmt.Errorf("count unavailable")
	_, _, err := svc.prepareProactiveCompaction(ctx, q, input(), nil)
	require.ErrorContains(t, err, "count unavailable")
	require.Zero(t, m.calls)
	require.Zero(t, r.effects)
}

func TestProactiveCompaction_NoEligibleHistorySkipsAndGrowthRearms(t *testing.T) {
	svc, q, m, r, ctx, input := proactiveFixture(t, false, 9000, 10000)
	_, _, err := svc.prepareProactiveCompaction(ctx, q, input(), nil)
	require.NoError(t, err)
	require.Equal(t, 1, m.compactions)
	// Only the latest user and handoff/recovery artifacts remain; immutable
	// instruction changes do not cause another recovery pass.
	gen := input()
	gen.SystemPrompt = &binding.Prompt{Text: "larger immutable prompt"}
	_, _, err = svc.prepareProactiveCompaction(ctx, q, gen, nil)
	require.NoError(t, err)
	require.Equal(t, 1, m.compactions)
	meta, _ := requestctx.TurnMetaFromContext(ctx)
	_, err = apiconv.AddMessage(ctx, r.store, &meta, apiconv.WithRole("assistant"), apiconv.WithType("text"), apiconv.WithStatus("completed"), apiconv.WithContent("new completed original facts"))
	require.NoError(t, err)
	_, _, err = svc.prepareProactiveCompaction(ctx, q, input(), nil)
	require.NoError(t, err)
	require.Equal(t, 2, m.compactions)
	require.Equal(t, 2, r.effects)
}

func TestCompactionHistoryArchivedParentDoesNotResurrectArchivedToolPair(t *testing.T) {
	archived := 1
	parent := &conversationmodel.MessageView{Id: "parent", ConversationId: "conv", Role: "assistant", Type: "text", Archived: &archived, Content: strPtr("old narration"), ToolMessage: []*conversationmodel.ToolMessageView{
		{Id: "removed", ToolCall: &conversationmodel.ToolCallView{OpId: "removed-op", ToolName: "lookup", Status: "succeeded", ResponsePayload: &conversationmodel.ModelCallStreamPayloadView{InlineBody: strPtr("completed result")}}},
		{Id: "pending", ToolCall: &conversationmodel.ToolCallView{OpId: "pending-op", ToolName: "lookup", Status: "running", ResponsePayload: &conversationmodel.ModelCallStreamPayloadView{InlineBody: strPtr("still running")}}},
	}}
	removed := &conversationmodel.MessageView{Id: "removed", ConversationId: "conv", Role: "tool", Type: "tool_op", Archived: &archived, Content: strPtr("completed result"), ToolMessage: parent.ToolMessage[:1]}
	pending := &conversationmodel.MessageView{Id: "pending", ConversationId: "conv", Role: "tool", Type: "tool_op", Content: strPtr("still running"), ToolMessage: parent.ToolMessage[1:]}
	conv := &apiconv.Conversation{Id: "conv", Transcript: []*conversationmodel.TranscriptView{{Id: "turn", Message: []*conversationmodel.MessageView{parent, removed, pending}}}}
	svc := &Service{}
	history, err := svc.buildHistory(context.Background(), conv.GetTranscript())
	require.NoError(t, err)
	calls, outputs := map[string]int{}, map[string]int{}
	for _, m := range history.LLMMessages() {
		for _, call := range m.ToolCalls {
			calls[call.ID]++
		}
		if m.ToolCallId != "" {
			outputs[m.ToolCallId]++
		}
	}
	require.Zero(t, calls["removed-op"])
	require.Zero(t, outputs["removed-op"])
	require.Equal(t, 1, calls["pending-op"])
	require.Equal(t, 1, outputs["pending-op"])
	replay := []*binding.Message{{ID: "removed", ToolOpID: "removed-op"}, {ID: "pending", ToolOpID: "pending-op"}, {ID: "not-persisted", ToolOpID: "in-flight"}}
	kept := filterArchivedCompactionReplay(conv, replay)
	require.Len(t, kept, 2)
	require.Equal(t, "pending", kept[0].ID)
	require.Equal(t, "not-persisted", kept[1].ID)
}

func TestProactiveCompactionFailureRequiresFullHistoryAfterRestart(t *testing.T) {
	svc, query, model, reg, ctx, makeInput := proactiveFixture(t, true, 9000, 10000)
	gen := makeInput()
	next, _, err := svc.prepareProactiveCompaction(ctx, query, gen, nil)
	require.NoError(t, err)
	model.generationErr = fmt.Errorf("forced fresh generation failure")
	_, err = svc.orchestrator.Run(next, gen, &core.GenerateOutput{})
	require.ErrorContains(t, err, "forced fresh generation failure")
	fresh, err := reg.store.GetConversation(ctx, query.ConversationID, apiconv.WithIncludeToolCall(true), apiconv.WithIncludeModelCall(true))
	require.NoError(t, err)
	require.True(t, recovery.NeedsFullHistory(fresh))
	// Discard both the in-memory attempt cache and context flag, like a restart.
	restarted := &Service{llm: svc.llm, conversation: reg.store, orchestrator: svc.orchestrator}
	model.tokens = 1 // Invalidation must apply even below the trigger threshold.
	model.generationErr = nil
	gen = makeInput()
	next, _, err = restarted.prepareProactiveCompaction(ctx, query, gen, nil)
	require.NoError(t, err)
	require.True(t, recovery.FullHistoryRequired(next))
	_, err = restarted.orchestrator.Run(next, gen, &core.GenerateOutput{})
	require.NoError(t, err)
	fresh, err = reg.store.GetConversation(ctx, query.ConversationID, apiconv.WithIncludeToolCall(true), apiconv.WithIncludeModelCall(true))
	require.NoError(t, err)
	require.False(t, recovery.NeedsFullHistory(fresh), "successful fresh response must durably clear the barrier")
	_, _, err = restarted.prepareProactiveCompaction(ctx, query, makeInput(), nil)
	require.NoError(t, err)
	require.Equal(t, 1, reg.effects)
}
