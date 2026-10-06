package reactor

import (
	"context"
	"errors"
	"fmt"
	modelcall "github.com/viant/agently-core/service/core/modelcall"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/genai/llm/provider/base"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/runtime/clienttool"
	memory "github.com/viant/agently-core/runtime/requestctx"
	core2 "github.com/viant/agently-core/service/core"
)

type failingInitialToolMessageClient struct {
	apiconv.Client
	mu      sync.Mutex
	failure error
	failed  bool
}

func (c *failingInitialToolMessageClient) PatchMessage(ctx context.Context, msg *apiconv.MutableMessage) error {
	c.mu.Lock()
	fail := msg.Role == "tool" && c.failure != nil && !c.failed
	if fail {
		c.failed = true
	}
	c.mu.Unlock()
	if fail {
		return c.failure
	}
	return c.Client.PatchMessage(ctx, msg)
}

type plannedCallsModel struct {
	calls    int
	stream   bool
	response *llm.GenerateResponse
}

func (m *plannedCallsModel) Generate(context.Context, *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	m.calls++
	return m.response, nil
}
func (m *plannedCallsModel) Implements(feature string) bool {
	return m.stream && feature == base.CanStream
}
func (m *plannedCallsModel) Stream(ctx context.Context, request *llm.GenerateRequest) (<-chan llm.StreamEvent, error) {
	m.calls++
	if observer := modelcall.ObserverFromContext(ctx); observer != nil {
		var err error
		ctx, err = observer.OnCallStart(ctx, modelcall.Info{Provider: "mock", Model: "mock", LLMRequest: request, StartedAt: time.Now()})
		if err != nil {
			return nil, err
		}
		if err = observer.OnCallEnd(ctx, modelcall.Info{Provider: "mock", Model: "mock", LLMResponse: m.response, CompletedAt: time.Now(), FinishReason: "tool_calls"}); err != nil {
			return nil, err
		}
	}
	ch := make(chan llm.StreamEvent, 1)
	ch <- llm.StreamEvent{Response: m.response}
	close(ch)
	return ch, nil
}

type plannedCallsFinder struct{ model llm.Model }

func (f plannedCallsFinder) Find(context.Context, string) (llm.Model, error) { return f.model, nil }

func TestRun_ParallelToolsRequireDurableResults(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream_%v_initial_message_failure_%v", stream, fail), func(t *testing.T) {
				store := convmem.New()
				ctx := memory.WithConversationID(context.Background(), "integrity-conv")
				ctx = memory.WithTurnMeta(ctx, memory.TurnMeta{ConversationID: "integrity-conv", TurnID: "integrity-turn"})
				conv := apiconv.NewConversation()
				conv.SetId("integrity-conv")
				require.NoError(t, store.PatchConversations(ctx, conv))
				cause := errors.New("initial tool message store unavailable")
				client := &failingInitialToolMessageClient{Client: store}
				if fail {
					client.failure = cause
				}
				calls := make([]llm.ToolCall, 12)
				for i := range calls {
					calls[i] = llm.ToolCall{ID: fmt.Sprintf("call-%d", i), Name: "forecast/cube", Arguments: map[string]interface{}{"index": i}}
				}
				model := &plannedCallsModel{stream: stream, response: &llm.GenerateResponse{ResponseID: "response", Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, ToolCalls: calls}, FinishReason: "tool_calls"}}}}
				reg := &reactorScriptedRegistry{script: []reactorScriptedResult{{result: "forecast result"}}}
				svc := New(core2.New(plannedCallsFinder{model: model}, reg, client), reg, client, nil, nil)
				input := &core2.GenerateInput{ModelSelection: llm.ModelSelection{Model: "mock"}, UserID: "test-user", Prompt: &binding.Prompt{Text: "forecast"}, Binding: &binding.Binding{}}
				plan, err := svc.Run(ctx, input, &core2.GenerateOutput{})
				require.NotNil(t, plan, "Run failed before returning a plan: %v", err)
				require.Len(t, plan.Steps, 12)
				require.Equal(t, 1, model.calls, "a failed tool batch must never cause another model request")
				expected := 12
				if fail {
					expected = 11
					require.ErrorIs(t, err, cause)
				} else {
					require.NoError(t, err)
				}
				results := svc.TurnToolResults("integrity-turn")
				require.Len(t, results, expected)
				seen := map[string]bool{}
				for _, result := range results {
					require.False(t, seen[result.ID])
					seen[result.ID] = true
					require.NotEmpty(t, result.ResultMessageID)
					message, getErr := store.GetMessage(ctx, result.ResultMessageID, apiconv.WithIncludeToolCall(true))
					require.NoError(t, getErr)
					require.NotNil(t, message)
					require.Equal(t, "forecast result", message.GetContent())
					require.Len(t, message.ToolMessage, 1)
					require.Equal(t, result.ID, message.ToolMessage[0].ToolCall.OpId)
					require.Equal(t, "completed", message.ToolMessage[0].ToolCall.Status)
				}
				require.Equal(t, expected, reg.calls)
			})
		}
	}
}

type canceledPreflightRegistry struct{ reactorScriptedRegistry }

func TestRun_ToolFailureRemainsPairedAndReplayable(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream_%v", stream), func(t *testing.T) {
			store := convmem.New()
			ctx := memory.WithConversationID(context.Background(), "tool-error-conv")
			ctx = memory.WithTurnMeta(ctx, memory.TurnMeta{ConversationID: "tool-error-conv", TurnID: "tool-error-turn"})
			conv := apiconv.NewConversation()
			conv.SetId("tool-error-conv")
			require.NoError(t, store.PatchConversations(ctx, conv))
			cause := errors.New("Datly forecast query rejected the supplied filter")
			reg := &reactorScriptedRegistry{script: []reactorScriptedResult{{err: cause}}}
			model := &plannedCallsModel{stream: stream, response: &llm.GenerateResponse{ResponseID: "failed-tool-response", Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "failed-tool-call", Name: "forecast/cube"}}}, FinishReason: "tool_calls"}}}}
			svc := New(core2.New(plannedCallsFinder{model: model}, reg, store), reg, store, nil, nil)
			input := &core2.GenerateInput{ModelSelection: llm.ModelSelection{Model: "mock"}, UserID: "test-user", Prompt: &binding.Prompt{Text: "forecast"}, Binding: &binding.Binding{}}
			_, err := svc.Run(ctx, input, &core2.GenerateOutput{})
			require.NoError(t, err, "a persisted tool error must remain available to the next model iteration")
			require.Equal(t, 1, reg.calls, "an ordinary tool error must not cause an execution retry")
			results := svc.TurnToolResults("tool-error-turn")
			require.Len(t, results, 1)
			require.Equal(t, "failed-tool-call", results[0].ID)
			require.Equal(t, cause.Error(), results[0].Result)
			message, err := store.GetMessage(ctx, results[0].ResultMessageID, apiconv.WithIncludeToolCall(true))
			require.NoError(t, err)
			require.Equal(t, cause.Error(), message.GetContent())
			require.Len(t, message.ToolMessage, 1)
			require.Equal(t, "failed-tool-call", message.ToolMessage[0].ToolCall.OpId)
			require.Equal(t, "failed", message.ToolMessage[0].ToolCall.Status)
			require.NotNil(t, message.ToolMessage[0].ToolCall.ResponsePayloadId)
		})
	}
}

func (r *canceledPreflightRegistry) PreflightCredential(ctx context.Context, _ string) error {
	return ctx.Err()
}

func TestResumePlan_CancellationPreservesPartialPlan(t *testing.T) {
	ctx, cancel := context.WithCancel(memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "cancel-conv", TurnID: "cancel-turn"}))
	cancel()
	reg := &canceledPreflightRegistry{}
	svc := &Service{registry: reg, turnToolResults: map[string][]llm.ToolCall{}}
	plan := svc.PlanFromResponse(&llm.GenerateResponse{Choices: []llm.Choice{{Message: llm.Message{ToolCalls: []llm.ToolCall{{ID: "cancel-call", Name: "forecast/cube"}}}}}})
	partial, err := svc.ResumePlan(ctx, plan, nil)
	require.NoError(t, err)
	require.Same(t, plan, partial)
	require.Empty(t, svc.TurnToolResults("cancel-turn"))
	require.Zero(t, reg.calls)
}

func TestResumePlan_ClientToolSuspendsWithoutTerminalOutput(t *testing.T) {
	session, err := clienttool.NewSession([]llm.ToolDefinition{{Name: "frontend/lookup"}})
	require.NoError(t, err)
	store := convmem.New()
	ctx := clienttool.WithSession(memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "suspend-conv", TurnID: "suspend-turn"}), session)
	conv := apiconv.NewConversation()
	conv.SetId("suspend-conv")
	require.NoError(t, store.PatchConversations(ctx, conv))
	reg := &reactorScriptedRegistry{}
	svc := &Service{registry: reg, convClient: store, turnToolResults: map[string][]llm.ToolCall{}}
	plan := svc.PlanFromResponse(&llm.GenerateResponse{Choices: []llm.Choice{{Message: llm.Message{ToolCalls: []llm.ToolCall{{ID: "suspend-call", Name: "frontend/lookup"}}}}}})
	_, err = svc.ResumePlan(ctx, plan, nil)
	require.NoError(t, err)
	require.Len(t, session.Pending(), 1)
	require.Zero(t, reg.calls)
	message, err := store.GetMessage(ctx, session.Pending()[0].ToolMessageID, apiconv.WithIncludeToolCall(true))
	require.NoError(t, err)
	require.Equal(t, "waiting_for_user", message.ToolMessage[0].ToolCall.Status)
	require.Nil(t, message.ToolMessage[0].ToolCall.CompletedAt)
	require.Nil(t, message.ToolMessage[0].ToolCall.ResponsePayloadId)
}

func TestResumePlan_RejectsReplayWithoutMatchingIdentity(t *testing.T) {
	svc := &Service{turnToolResults: map[string][]llm.ToolCall{}}
	_, err := svc.ResumePlan(context.Background(), nil, map[string]llm.ToolCall{"original": {ID: "other", Name: "forecast/cube", Result: "result"}})
	require.ErrorContains(t, err, "invalid replay result")
}
