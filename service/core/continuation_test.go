package core

import (
	"context"
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

// TestBuildContinuationRequest_SkipsWhenToolResultDropped reproduces the
// "No tool output found for function call" bug. The LLM response (anchor)
// produced two tool calls, but one tool result's payload was lost so only
// one tool-call/result pair appears in the request messages. The old guard
// (count-based) would incorrectly allow continuation because it saw 1:1;
// the cross-check against the traces map detects the mismatch and skips.
func TestBuildContinuationRequest_SkipsWhenToolResultDropped(t *testing.T) {
	svc := &Service{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1"})
	history := &binding.History{
		Traces:       map[string]*binding.Trace{},
		LastResponse: &binding.Trace{ID: "resp-123", At: time.Now()},
	}
	// The anchor response produced two tool calls.
	history.Traces[binding.KindToolCall.Key("call-1")] = &binding.Trace{ID: "resp-123", Kind: binding.KindToolCall}
	history.Traces[binding.KindToolCall.Key("call-2")] = &binding.Trace{ID: "resp-123", Kind: binding.KindToolCall}

	// But only call-1's result survived (call-2's payload was lost).
	// toolResultLLMMessages would have produced the assistant+tool pair for call-1 only.
	req := &llm.GenerateRequest{}
	req.Messages = append(req.Messages,
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "resources-list"}}},
		llm.Message{Role: llm.RoleTool, ToolCallId: "call-1", Content: `{"files":["a.go"]}`},
	)

	cont := svc.BuildContinuationRequest(ctx, req, history)
	// Must skip continuation: the anchor had 2 tool calls but we only have 1 result.
	assert.Nil(t, cont, "continuation should be skipped when anchor tool call count exceeds selected tool results")
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
