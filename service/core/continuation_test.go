package core

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	"github.com/viant/agently-core/protocol/binding"
	memory "github.com/viant/agently-core/runtime/requestctx"
)

func TestBuildContinuationRequest_IncludesAssistantToolCalls(t *testing.T) {
	svc := &Service{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1"})
	history := &binding.History{
		Traces:       map[string]*binding.Trace{},
		LastResponse: &binding.Trace{ID: "resp-123", At: time.Now()},
	}
	toolKey := binding.KindToolCall.Key("call-1")
	history.Traces[toolKey] = &binding.Trace{ID: "resp-123"}
	toolKey2 := binding.KindToolCall.Key("call-2")
	history.Traces[toolKey2] = &binding.Trace{ID: "resp-other"}

	req := &llm.GenerateRequest{}
	req.Messages = append(req.Messages,
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "toolA"}, {ID: "call-2", Name: "toolB"}}},
		llm.Message{Role: llm.RoleTool, ToolCallId: "call-1"},
		llm.Message{Role: llm.RoleTool, ToolCallId: "call-2"},
	)

	cont := svc.BuildContinuationRequest(ctx, req, history)
	if assert.NotNil(t, cont) {
		assert.Equal(t, "resp-123", cont.PreviousResponseID)
		if assert.Len(t, cont.Messages, 2) {
			assistantMsg := cont.Messages[0]
			toolMsg := cont.Messages[1]
			assert.Equal(t, llm.RoleAssistant, assistantMsg.Role)
			if assert.Len(t, assistantMsg.ToolCalls, 1) {
				assert.Equal(t, "call-1", assistantMsg.ToolCalls[0].ID)
			}
			assert.Equal(t, "call-1", toolMsg.ToolCallId)
		}
	}
}

func TestBuildContinuationRequest_BackfillsModeFromContextWhenRequestOptionsEmpty(t *testing.T) {
	svc := &Service{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1"})
	ctx = memory.WithRequestMode(ctx, "chain")
	history := &binding.History{
		Traces:       map[string]*binding.Trace{},
		LastResponse: &binding.Trace{ID: "resp-123", At: time.Now()},
	}
	toolKey := binding.KindToolCall.Key("call-1")
	history.Traces[toolKey] = &binding.Trace{ID: "resp-123", Kind: binding.KindToolCall}

	req := &llm.GenerateRequest{
		Options: &llm.Options{},
	}
	req.Messages = append(req.Messages,
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "toolA"}}},
		llm.Message{Role: llm.RoleTool, ToolCallId: "call-1"},
	)

	cont := svc.BuildContinuationRequest(ctx, req, history)
	if assert.NotNil(t, cont) {
		if assert.NotNil(t, cont.Options) {
			assert.Equal(t, "chain", cont.Options.Mode)
		}
	}
}

func TestBuildContinuationRequest_PreservesInstructionsAndPromptCacheKey(t *testing.T) {
	svc := &Service{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1"})
	history := &binding.History{
		Traces:       map[string]*binding.Trace{},
		LastResponse: &binding.Trace{ID: "resp-123", At: time.Now()},
	}
	toolKey := binding.KindToolCall.Key("call-1")
	history.Traces[toolKey] = &binding.Trace{ID: "resp-123", Kind: binding.KindToolCall}

	req := &llm.GenerateRequest{
		Instructions:   "Preserve this instruction block.",
		PromptCacheKey: "conv-1",
	}
	req.Messages = append(req.Messages,
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "toolA"}}},
		llm.Message{Role: llm.RoleTool, ToolCallId: "call-1"},
	)

	cont := svc.BuildContinuationRequest(ctx, req, history)
	if assert.NotNil(t, cont) {
		assert.Equal(t, "Preserve this instruction block.", cont.Instructions)
		assert.Equal(t, "conv-1", cont.PromptCacheKey)
	}
}

// TestBuildContinuationRequest_AllowsMultiToolAnchor verifies that multi-tool
// continuations succeed when all tool results are present.
func TestBuildContinuationRequest_AllowsMultiToolAnchor(t *testing.T) {
	svc := &Service{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1"})
	history := &binding.History{
		Traces:       map[string]*binding.Trace{},
		LastResponse: &binding.Trace{ID: "resp-123", At: time.Now()},
	}
	history.Traces[binding.KindToolCall.Key("call-1")] = &binding.Trace{ID: "resp-123", Kind: binding.KindToolCall}
	history.Traces[binding.KindToolCall.Key("call-2")] = &binding.Trace{ID: "resp-123", Kind: binding.KindToolCall}

	req := &llm.GenerateRequest{}
	req.Messages = append(req.Messages,
		// toolResultLLMMessages creates separate assistant messages per tool call
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "toolA"}}},
		llm.Message{Role: llm.RoleTool, ToolCallId: "call-1", Content: `{"ok":true}`},
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-2", Name: "toolB"}}},
		llm.Message{Role: llm.RoleTool, ToolCallId: "call-2", Content: `{"ok":true}`},
	)

	cont := svc.BuildContinuationRequest(ctx, req, history)
	if assert.NotNil(t, cont, "multi-tool continuation should succeed when all results present") {
		assert.Equal(t, "resp-123", cont.PreviousResponseID)
		assert.Len(t, cont.Messages, 4, "should include 2 assistant+tool pairs")
	}
}

func TestBuildContinuationRequest_DedupesRepeatedToolReplayPairs(t *testing.T) {
	svc := &Service{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1"})
	history := &binding.History{
		Traces:       map[string]*binding.Trace{},
		LastResponse: &binding.Trace{ID: "resp-123", At: time.Now()},
	}
	history.Traces[binding.KindToolCall.Key("call-1")] = &binding.Trace{ID: "resp-123", Kind: binding.KindToolCall}

	req := &llm.GenerateRequest{}
	req.Messages = append(req.Messages,
		llm.Message{ID: "assistant-1", Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "message-show"}}},
		llm.Message{ID: "tool-1", Role: llm.RoleTool, ToolCallId: "call-1", Content: `{"content":"ok"}`},
		llm.Message{ID: "assistant-1", Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "message-show"}}},
		llm.Message{ID: "tool-1", Role: llm.RoleTool, ToolCallId: "call-1", Content: `{"content":"ok"}`},
	)

	cont := svc.BuildContinuationRequest(ctx, req, history)
	if assert.NotNil(t, cont, "duplicate replay pairs should still produce continuation") {
		assert.Equal(t, "resp-123", cont.PreviousResponseID)
		if assert.Len(t, cont.Messages, 2) {
			assert.Equal(t, llm.RoleAssistant, cont.Messages[0].Role)
			if assert.Len(t, cont.Messages[0].ToolCalls, 1) {
				assert.Equal(t, "call-1", cont.Messages[0].ToolCalls[0].ID)
			}
			assert.Equal(t, llm.RoleTool, cont.Messages[1].Role)
			assert.Equal(t, "call-1", cont.Messages[1].ToolCallId)
		}
	}
}

func TestBuildContinuationRequest_AllowsSystemMessagesWhenToolReplayIsComplete(t *testing.T) {
	svc := &Service{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1"})
	history := &binding.History{
		Traces:       map[string]*binding.Trace{},
		LastResponse: &binding.Trace{ID: "resp-123", At: time.Now()},
	}
	history.Traces[binding.KindToolCall.Key("call-1")] = &binding.Trace{ID: "resp-123", Kind: binding.KindToolCall}

	req := &llm.GenerateRequest{}
	req.Messages = append(req.Messages,
		llm.NewSystemMessage("You are Steward."),
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "forecasting-Total"}}},
		llm.Message{Role: llm.RoleTool, ToolCallId: "call-1", Content: `{"jobStatus":"WAITING"}`},
		llm.NewSystemMessage("Use the existing system knowledge bundle."),
	)

	cont := svc.BuildContinuationRequest(ctx, req, history)
	if assert.NotNil(t, cont, "continuation should not be disabled by system messages when tool replay is complete") {
		assert.Equal(t, "resp-123", cont.PreviousResponseID)
		assert.Len(t, cont.Messages, 2)
	}
}

func TestBuildContinuationRequest_IncludesAssistantMessagesAfterAnchor(t *testing.T) {
	svc := &Service{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1"})
	baseTime := time.Now()
	history := &binding.History{
		Traces: map[string]*binding.Trace{
			binding.KindToolCall.Key("call-1"):          {ID: "resp-123", Kind: binding.KindToolCall, At: baseTime},
			binding.KindContent.Key("PRELIMINARY NOTE"): {ID: "", Kind: binding.KindContent, At: baseTime.Add(time.Second)},
		},
		LastResponse: &binding.Trace{ID: "resp-123", At: baseTime, Kind: binding.KindResponse},
	}

	req := &llm.GenerateRequest{}
	req.Messages = append(req.Messages,
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "message-add"}}},
		llm.Message{Role: llm.RoleTool, ToolCallId: "call-1", Content: `{"messageId":"msg-1"}`},
		llm.Message{Role: llm.RoleAssistant, Content: "PRELIMINARY NOTE"},
	)

	cont := svc.BuildContinuationRequest(ctx, req, history)
	if assert.NotNil(t, cont) {
		assert.Equal(t, "resp-123", cont.PreviousResponseID)
		if assert.Len(t, cont.Messages, 3) {
			assert.Equal(t, llm.RoleAssistant, cont.Messages[2].Role)
			assert.Equal(t, "PRELIMINARY NOTE", cont.Messages[2].Content)
		}
	}
}

// TestBuildContinuationRequest_ThreeIterations simulates three sequential
// model iterations to verify continuation works across all of them:
//   - Iteration 1: full request (no anchor) → model produces resp_A with op-1
//   - Iteration 2: continuation from resp_A → model produces resp_B with op-2
//   - Iteration 3: continuation from resp_B → should select op-2 messages
//
// This reproduces the bug where iteration 3+ falls back to full transcript
// because the anchor advances but tool call traces are not correctly matched.
func TestBuildContinuationRequest_ThreeIterations(t *testing.T) {
	svc := &Service{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1"})
	baseTime := time.Now().Add(-5 * time.Second)

	// -- Iteration 2 scenario --
	// After iteration 1 completed (resp_A + op-1 tool call done), the binding
	// sets LastResponse=resp_A and traces include op-1→resp_A.
	t.Run("iteration2_continuation_from_resp_A", func(t *testing.T) {
		history := &binding.History{
			Traces:       map[string]*binding.Trace{},
			LastResponse: &binding.Trace{ID: "resp_A", At: baseTime, Kind: binding.KindResponse},
		}
		// Trace: op-1 was requested by resp_A
		history.Traces[binding.KindToolCall.Key("op-1")] = &binding.Trace{ID: "resp_A", Kind: binding.KindToolCall, At: baseTime}
		// Response trace
		history.Traces[binding.KindResponse.Key("resp_A")] = &binding.Trace{ID: "resp_A", Kind: binding.KindResponse, At: baseTime}

		// Full LLM request messages for iteration 2
		req := &llm.GenerateRequest{}
		req.Messages = append(req.Messages,
			llm.Message{Role: llm.RoleUser, Content: "analyze /Users/awitas/go/src/github.com/viant/xdatly"},
			// synthetic assistant+tool pair from toolResultLLMMessages for op-1
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "op-1", Name: "orchestration-updatePlan"}}},
			llm.Message{Role: llm.RoleTool, ToolCallId: "op-1", Content: `{"status":"ok"}`},
		)

		cont := svc.BuildContinuationRequest(ctx, req, history)
		if assert.NotNil(t, cont, "iteration 2 should produce continuation") {
			assert.Equal(t, "resp_A", cont.PreviousResponseID)
			assert.Len(t, cont.Messages, 2, "should include assistant+tool for op-1")
		}
	})

	// -- Iteration 3 scenario --
	// After iteration 2 completed (resp_B + op-2 tool call done), the binding
	// sets LastResponse=resp_B and traces include op-1→resp_A and op-2→resp_B.
	t.Run("iteration3_continuation_from_resp_B", func(t *testing.T) {
		history := &binding.History{
			Traces:       map[string]*binding.Trace{},
			LastResponse: &binding.Trace{ID: "resp_B", At: baseTime.Add(2 * time.Second), Kind: binding.KindResponse},
		}
		// Traces from both iterations
		history.Traces[binding.KindToolCall.Key("op-1")] = &binding.Trace{ID: "resp_A", Kind: binding.KindToolCall, At: baseTime}
		history.Traces[binding.KindToolCall.Key("op-2")] = &binding.Trace{ID: "resp_B", Kind: binding.KindToolCall, At: baseTime.Add(2 * time.Second)}
		history.Traces[binding.KindResponse.Key("resp_A")] = &binding.Trace{ID: "resp_A", Kind: binding.KindResponse, At: baseTime}
		history.Traces[binding.KindResponse.Key("resp_B")] = &binding.Trace{ID: "resp_B", Kind: binding.KindResponse, At: baseTime.Add(2 * time.Second)}

		// Full LLM request messages for iteration 3 (includes all prior tool results)
		req := &llm.GenerateRequest{}
		req.Messages = append(req.Messages,
			llm.Message{Role: llm.RoleUser, Content: "analyze /Users/awitas/go/src/github.com/viant/xdatly"},
			// from iteration 1: op-1 → resp_A
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "op-1", Name: "orchestration-updatePlan"}}},
			llm.Message{Role: llm.RoleTool, ToolCallId: "op-1", Content: `{"status":"ok"}`},
			// from iteration 2: op-2 → resp_B
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "op-2", Name: "resources-list"}}},
			llm.Message{Role: llm.RoleTool, ToolCallId: "op-2", Content: `{"files":["a.go","b.go"]}`},
		)

		cont := svc.BuildContinuationRequest(ctx, req, history)
		if assert.NotNil(t, cont, "iteration 3 should produce continuation from resp_B") {
			assert.Equal(t, "resp_B", cont.PreviousResponseID)
			if assert.Len(t, cont.Messages, 2, "should include only op-2 assistant+tool") {
				assert.Equal(t, llm.RoleAssistant, cont.Messages[0].Role)
				if assert.Len(t, cont.Messages[0].ToolCalls, 1) {
					assert.Equal(t, "op-2", cont.Messages[0].ToolCalls[0].ID)
				}
				assert.Equal(t, "op-2", cont.Messages[1].ToolCallId)
			}
		}
	})

	// -- Iteration 3 scenario with MISSING TraceId --
	// This reproduces the suspected root cause: tool calls from iteration 2
	// have empty TraceId in the transcript (step.ResponseID was empty,
	// TurnTrace fallback also missed). This causes filterToolCallsByAnchor
	// to fail because the trace's ID is "" instead of "resp_B".
	t.Run("iteration3_fails_when_trace_id_empty", func(t *testing.T) {
		history := &binding.History{
			Traces:       map[string]*binding.Trace{},
			LastResponse: &binding.Trace{ID: "resp_B", At: baseTime.Add(2 * time.Second), Kind: binding.KindResponse},
		}
		// op-1 has correct trace, but op-2 has EMPTY trace ID (bug scenario)
		history.Traces[binding.KindToolCall.Key("op-1")] = &binding.Trace{ID: "resp_A", Kind: binding.KindToolCall, At: baseTime}
		history.Traces[binding.KindToolCall.Key("op-2")] = &binding.Trace{ID: "", Kind: binding.KindToolCall, At: baseTime.Add(2 * time.Second)} // BUG: empty ID
		history.Traces[binding.KindResponse.Key("resp_A")] = &binding.Trace{ID: "resp_A", Kind: binding.KindResponse, At: baseTime}
		history.Traces[binding.KindResponse.Key("resp_B")] = &binding.Trace{ID: "resp_B", Kind: binding.KindResponse, At: baseTime.Add(2 * time.Second)}

		req := &llm.GenerateRequest{}
		req.Messages = append(req.Messages,
			llm.Message{Role: llm.RoleUser, Content: "analyze /Users/awitas/go/src/github.com/viant/xdatly"},
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "op-1", Name: "orchestration-updatePlan"}}},
			llm.Message{Role: llm.RoleTool, ToolCallId: "op-1", Content: `{"status":"ok"}`},
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "op-2", Name: "resources-list"}}},
			llm.Message{Role: llm.RoleTool, ToolCallId: "op-2", Content: `{"files":["a.go","b.go"]}`},
		)

		cont := svc.BuildContinuationRequest(ctx, req, history)
		// With empty trace ID, continuation falls back to full — this IS the bug
		assert.Nil(t, cont, "continuation should fail when tool call trace ID is empty (the bug)")
	})
}

func TestTryGenerateContinuationByAnchor_ReplaysAllToolOutputsForSharedParentMessage(t *testing.T) {
	respID := "resp-123"
	call1 := "call-1"
	call2 := "call-2"
	convView := &agconv.ConversationView{
		Id: "conv-1",
		Transcript: []*agconv.TranscriptView{{
			Id:             "turn-1",
			ConversationId: "conv-1",
			Message: []*agconv.MessageView{{
				Id:             "assistant-1",
				ConversationId: "conv-1",
				Role:           "assistant",
				Type:           "text",
				CreatedAt:      time.Now(),
				ModelCall: &agconv.ModelCallView{
					TraceId: &respID,
					Status:  "completed",
				},
				ToolMessage: []*agconv.ToolMessageView{
					{Id: "tool-msg-1", ToolCall: &agconv.ToolCallView{MessageId: "tool-msg-1", OpId: call1, TraceId: &respID, Status: "completed"}},
					{Id: "tool-msg-2", ToolCall: &agconv.ToolCallView{MessageId: "tool-msg-2", OpId: call2, TraceId: &respID, Status: "completed"}},
				},
			}},
		}},
	}
	conv := (*apiconv.Conversation)(convView)
	model := &continuationRecordingModel{
		response: &llm.GenerateResponse{ResponseID: "resp-next"},
	}
	svc := &Service{convClient: continuationConversationClient{conversation: conv}}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1"})

	request := &llm.GenerateRequest{
		Messages: []llm.Message{
			{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{
					{ID: call1, Name: "toolA"},
					{ID: call2, Name: "toolB"},
				},
			},
			{Role: llm.RoleTool, ToolCallId: call1, Content: `{"ok":true}`},
			{Role: llm.RoleTool, ToolCallId: call2, Content: `{"ok":true}`},
		},
	}

	resp, used, err := svc.tryGenerateContinuationByAnchor(ctx, model, request)
	if err != nil {
		t.Fatalf("tryGenerateContinuationByAnchor() error: %v", err)
	}
	if !used {
		t.Fatalf("expected continuation-by-anchor to be used")
	}
	if resp == nil || resp.ResponseID != "resp-next" {
		t.Fatalf("unexpected response: %#v", resp)
	}
	if len(model.requests) != 1 {
		t.Fatalf("expected one continuation subcall, got %d", len(model.requests))
	}
	got := model.requests[0]
	if got.PreviousResponseID != respID {
		t.Fatalf("expected previous_response_id=%q, got %q", respID, got.PreviousResponseID)
	}
	if len(got.Messages) != 3 {
		t.Fatalf("expected assistant tool-call message plus two tool results, got %#v", got.Messages)
	}
	if len(got.Messages[0].ToolCalls) != 2 {
		t.Fatalf("expected both tool calls to stay visible in anchored replay, got %#v", got.Messages[0].ToolCalls)
	}
}

type continuationConversationClient struct {
	conversation *apiconv.Conversation
}

func (c continuationConversationClient) GetConversation(context.Context, string, ...apiconv.Option) (*apiconv.Conversation, error) {
	return c.conversation, nil
}

func (c continuationConversationClient) GetConversations(context.Context, *apiconv.Input) ([]*apiconv.Conversation, error) {
	return nil, fmt.Errorf("unexpected GetConversations call")
}

func (c continuationConversationClient) PatchConversations(context.Context, *apiconv.MutableConversation) error {
	return fmt.Errorf("unexpected PatchConversations call")
}

func (c continuationConversationClient) GetPayload(context.Context, string) (*apiconv.Payload, error) {
	return nil, fmt.Errorf("unexpected GetPayload call")
}

func (c continuationConversationClient) PatchPayload(context.Context, *apiconv.MutablePayload) error {
	return fmt.Errorf("unexpected PatchPayload call")
}

func (c continuationConversationClient) PatchMessage(context.Context, *apiconv.MutableMessage) error {
	return fmt.Errorf("unexpected PatchMessage call")
}

func (c continuationConversationClient) GetMessage(context.Context, string, ...apiconv.Option) (*apiconv.Message, error) {
	return nil, fmt.Errorf("unexpected GetMessage call")
}

func (c continuationConversationClient) GetMessageByElicitation(context.Context, string, string) (*apiconv.Message, error) {
	return nil, fmt.Errorf("unexpected GetMessageByElicitation call")
}

func (c continuationConversationClient) PatchModelCall(context.Context, *apiconv.MutableModelCall) error {
	return fmt.Errorf("unexpected PatchModelCall call")
}

func (c continuationConversationClient) PatchToolCall(context.Context, *apiconv.MutableToolCall) error {
	return fmt.Errorf("unexpected PatchToolCall call")
}

func (c continuationConversationClient) PatchTurn(context.Context, *apiconv.MutableTurn) error {
	return fmt.Errorf("unexpected PatchTurn call")
}

func (c continuationConversationClient) DeleteConversation(context.Context, string) error {
	return fmt.Errorf("unexpected DeleteConversation call")
}

func (c continuationConversationClient) DeleteMessage(context.Context, string, string) error {
	return fmt.Errorf("unexpected DeleteMessage call")
}

type continuationRecordingModel struct {
	requests []*llm.GenerateRequest
	response *llm.GenerateResponse
}

func (m *continuationRecordingModel) Generate(_ context.Context, request *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	cp := *request
	cp.Messages = append([]llm.Message(nil), request.Messages...)
	m.requests = append(m.requests, &cp)
	return m.response, nil
}

func (m *continuationRecordingModel) Implements(string) bool {
	return true
}

func TestGroupMessagesByAnchor_IncludesAssistantMessages(t *testing.T) {
	respID := "resp-1"
	timeRef := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	toolTrace := respID
	traces := apiconv.IndexedMessages{
		"call-1": {ToolMessage: []*agconv.ToolMessageView{{ToolCall: &agconv.ToolCallView{TraceId: &toolTrace}}}},
		respID:   {ModelCall: &agconv.ModelCallView{TraceId: &respID}, CreatedAt: timeRef},
	}
	testCases := []struct {
		name     string
		messages []llm.Message
	}{
		{
			name: "single anchor",
			messages: []llm.Message{
				{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "run"}}},
				{Role: llm.RoleTool, ToolCallId: "call-1"},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			groups, order, latest := groupMessagesByAnchor(tc.messages, traces)
			if assert.Len(t, order, 1) {
				assert.EqualValues(t, respID, order[0])
			}
			assert.EqualValues(t, respID, latest)
			if assert.Contains(t, groups, respID) {
				if assert.Len(t, groups[respID], 2) {
					assert.EqualValues(t, llm.RoleAssistant, groups[respID][0].Role)
					assert.EqualValues(t, llm.RoleTool, groups[respID][1].Role)
					assert.EqualValues(t, "call-1", groups[respID][1].ToolCallId)
				}
			}
		})
	}
}
