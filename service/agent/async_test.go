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
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	asynccfg "github.com/viant/agently-core/protocol/async"
	"github.com/viant/agently-core/protocol/binding"
	memory "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/core"
	"github.com/viant/agently-core/service/reactor"
)

func TestInjectAsyncReinforcement_AddsSystemMessage(t *testing.T) {
	ctx := context.Background()
	client := &recordingConvClient{}
	svc := &Service{
		conversation: client,
		asyncManager: asynccfg.NewManager(),
	}

	turn := &memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}
	svc.asyncManager.Register(ctx, asynccfg.RegisterInput{
		ID:              "op-1",
		ParentConvID:    "conv-1",
		ParentTurnID:    "turn-1",
		ToolName:        "llm/agents:start",
		WaitForResponse: true,
		Status:          "running",
		Message:         "still working",
	})

	svc.injectAsyncReinforcement(ctx, turn)

	require.NotNil(t, client.lastMessage)
	require.Equal(t, "system", client.lastMessage.Role)
	require.NotNil(t, client.lastMessage.Mode)
	require.Equal(t, "async_wait", *client.lastMessage.Mode)
	require.NotNil(t, client.lastMessage.Content)
	content := *client.lastMessage.Content
	require.Contains(t, content, "op-1")
	// The batched template shows the operation message for non-terminal ops.
	require.Contains(t, content, "still working")
	// Behavior section: no status tool configured, no same-tool reuse → runtime handles it.
	require.Contains(t, content, "autonomously")
}

func TestInjectAsyncReinforcement_ConsumesPendingChange(t *testing.T) {
	ctx := context.Background()
	client := &recordingConvClient{}
	svc := &Service{
		conversation: client,
		asyncManager: asynccfg.NewManager(),
	}

	turn := &memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}
	svc.asyncManager.Register(ctx, asynccfg.RegisterInput{
		ID:              "op-1",
		ParentConvID:    "conv-1",
		ParentTurnID:    "turn-1",
		ToolName:        "system/exec:start",
		WaitForResponse: true,
		Status:          "running",
	})

	svc.injectAsyncReinforcement(ctx, turn)
	changed := svc.asyncManager.ConsumeChanged("conv-1", "turn-1")
	require.Len(t, changed, 0)
}

func TestInjectAsyncReinforcement_DoesNotFallbackToUnchangedActiveWaitOps(t *testing.T) {
	ctx := context.Background()
	client := &recordingConvClient{}
	svc := &Service{
		conversation: client,
		asyncManager: asynccfg.NewManager(),
	}

	turn := &memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}
	svc.asyncManager.Register(ctx, asynccfg.RegisterInput{
		ID:              "op-1",
		ParentConvID:    "conv-1",
		ParentTurnID:    "turn-1",
		ToolName:        "system/exec:start",
		WaitForResponse: true,
		Status:          "running",
		Message:         "still working",
	})

	// Consume the initial change so the op remains active-but-unchanged.
	_ = svc.asyncManager.ConsumeChanged("conv-1", "turn-1")

	svc.injectAsyncReinforcement(ctx, turn)

	require.Nil(t, client.lastMessage, "unchanged active wait ops must stay in runtime polling and not re-enter model context")
}

func TestInjectAsyncReinforcement_RuntimePolledInstruction(t *testing.T) {
	ctx := context.Background()
	client := &recordingConvClient{}
	svc := &Service{
		conversation: client,
		asyncManager: asynccfg.NewManager(),
	}

	turn := &memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}
	svc.asyncManager.Register(ctx, asynccfg.RegisterInput{
		ID:              "sess-1",
		ParentConvID:    "conv-1",
		ParentTurnID:    "turn-1",
		ToolName:        "system/exec:start",
		StatusToolName:  "system/exec:status",
		StatusArgs:      map[string]interface{}{"sessionId": "sess-1"},
		WaitForResponse: true,
		Status:          "running",
	})

	svc.injectAsyncReinforcement(ctx, turn)

	require.NotNil(t, client.lastMessage)
	require.NotNil(t, client.lastMessage.Content)
	content := strings.TrimSpace(*client.lastMessage.Content)
	// Runtime-polled ops must NOT expose the status tool or its args to the model —
	// the autonomous poller owns that call. Instead the template flags runtimePolled.
	require.Contains(t, content, "runtime polled: true")
	require.Contains(t, content, "do not call the status tool yourself")
	require.NotContains(t, content, "status tool:")
	require.NotContains(t, content, `{"sessionId":"sess-1"}`)
	require.NotContains(t, content, "same-tool reuse")
}

func TestInjectAsyncReinforcement_IncludesExplicitStatusToolInstruction(t *testing.T) {
	ctx := context.Background()
	svc := &Service{
		asyncManager: asynccfg.NewManager(),
	}

	rec := &asynccfg.OperationRecord{
		ID:              "child-1",
		ParentConvID:    "conv-1",
		ParentTurnID:    "turn-1",
		ToolName:        "llm/agents:start",
		StatusToolName:  "llm/agents:status",
		StatusArgs:      map[string]interface{}{"conversationId": "child-1"},
		WaitForResponse: false,
		Status:          "running",
		State:           asynccfg.StateRunning,
	}

	content := strings.TrimSpace(svc.renderBatchedAsyncReinforcement(ctx, []*asynccfg.OperationRecord{rec}))
	require.Contains(t, content, "status tool: `llm/agents:status`")
	require.Contains(t, content, `status tool args: `+"`"+`{"conversationId":"child-1"}`+"`")
	require.NotContains(t, content, "runtime polled: true")
}

func TestInjectAsyncReinforcement_TerminalPromptTellsModelToAnswer(t *testing.T) {
	ctx := context.Background()
	client := &recordingConvClient{}
	svc := &Service{
		conversation: client,
		asyncManager: asynccfg.NewManager(),
	}

	turn := &memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}
	svc.asyncManager.Register(ctx, asynccfg.RegisterInput{
		ID:                "op-1",
		ParentConvID:      "conv-1",
		ParentTurnID:      "turn-1",
		ToolName:          "forecasting-Total",
		WaitForResponse:   true,
		Status:            "COMPLETE",
		KeyData:           []byte(`[{"inventory":1}]`),
		RequestArgsDigest: `{"DealsPmpIncl":[142130]}`,
		RequestArgs:       map[string]interface{}{"DealsPmpIncl": []int{142130}},
	})

	svc.injectAsyncReinforcement(ctx, turn)

	require.NotNil(t, client.lastMessage)
	require.NotNil(t, client.lastMessage.Content)
	content := strings.TrimSpace(*client.lastMessage.Content)
	// Batched template behavior section for all-resolved state.
	require.Contains(t, content, "terminal state")
	require.Contains(t, content, "Do not poll again")
	require.Contains(t, content, "answer the user")
}

// ---------------------------------------------------------------------------
// Data-driven tests for the new batched reinforcement path
// ---------------------------------------------------------------------------

func TestBuildBatchedAsyncContext(t *testing.T) {
	type testCase struct {
		name            string
		allOps          []asynccfg.RegisterInput
		changedRecords  []*asynccfg.OperationRecord
		wantPending     int
		wantCompleted   int
		wantFailed      int
		wantCanceled    int
		wantAllResolved bool
		wantOpsLen      int
		wantOpChecks    []func(t *testing.T, ops []map[string]interface{})
	}

	cases := []testCase{
		{
			name: "single non-terminal non-wait op with distinct status tool",
			allOps: []asynccfg.RegisterInput{{
				ID: "op-1", ParentConvID: "c1", ParentTurnID: "t1",
				ToolName: "llm/agents:start", StatusToolName: "llm/agents:status",
				StatusArgs:      map[string]interface{}{"conversationId": "child-1"},
				WaitForResponse: false, Status: "running",
			}},
			changedRecords: []*asynccfg.OperationRecord{{
				ID: "op-1", ParentConvID: "c1", ParentTurnID: "t1",
				ToolName: "llm/agents:start", StatusToolName: "llm/agents:status",
				StatusArgs:      map[string]interface{}{"conversationId": "child-1"},
				WaitForResponse: false, State: asynccfg.StateRunning, Status: "running",
			}},
			wantPending: 1, wantAllResolved: false, wantOpsLen: 1,
			wantOpChecks: []func(t *testing.T, ops []map[string]interface{}){
				func(t *testing.T, ops []map[string]interface{}) {
					op := ops[0]
					require.Equal(t, "op-1", op["id"])
					require.Equal(t, "llm/agents:start", op["toolName"])
					require.Equal(t, false, op["terminal"])
					require.Equal(t, false, op["sameToolReuse"])
					require.Equal(t, false, op["runtimePolled"])
					require.Equal(t, "llm/agents:status", op["statusToolName"])
					require.Contains(t, op["statusToolArgsJSON"], "child-1")
					_, hasRequest := op["requestArgsJSON"]
					require.False(t, hasRequest, "explicit-status op must not expose requestArgsJSON")
				},
			},
		},
		{
			name: "single non-terminal op with same-tool reuse",
			allOps: []asynccfg.RegisterInput{{
				ID: "op-2", ParentConvID: "c1", ParentTurnID: "t1",
				ToolName: "forecasting:Total", StatusToolName: "forecasting:Total",
				RequestArgs:     map[string]interface{}{"viewId": "TOTAL"},
				WaitForResponse: true, Status: "waiting",
			}},
			changedRecords: []*asynccfg.OperationRecord{{
				ID: "op-2", ParentConvID: "c1", ParentTurnID: "t1",
				ToolName: "forecasting:Total", StatusToolName: "forecasting:Total",
				RequestArgs:     map[string]interface{}{"viewId": "TOTAL"},
				WaitForResponse: true, State: asynccfg.StateWaiting, Status: "waiting",
			}},
			wantPending: 1, wantAllResolved: false, wantOpsLen: 1,
			wantOpChecks: []func(t *testing.T, ops []map[string]interface{}){
				func(t *testing.T, ops []map[string]interface{}) {
					op := ops[0]
					require.Equal(t, true, op["sameToolReuse"])
					require.Contains(t, op["requestArgsJSON"], "TOTAL")
					_, hasStatus := op["statusToolName"]
					require.False(t, hasStatus, "same-tool reuse should not expose statusToolName")
				},
			},
		},
		{
			name: "single terminal completed op",
			allOps: []asynccfg.RegisterInput{{
				ID: "op-3", ParentConvID: "c1", ParentTurnID: "t1",
				ToolName: "system/exec:start", WaitForResponse: true, Status: "completed",
			}},
			changedRecords: []*asynccfg.OperationRecord{{
				ID: "op-3", ParentConvID: "c1", ParentTurnID: "t1",
				ToolName: "system/exec:start", WaitForResponse: true,
				State: asynccfg.StateCompleted, Status: "completed",
			}},
			wantCompleted: 1, wantAllResolved: true, wantOpsLen: 1,
			wantOpChecks: []func(t *testing.T, ops []map[string]interface{}){
				func(t *testing.T, ops []map[string]interface{}) {
					op := ops[0]
					require.Equal(t, true, op["terminal"])
					_, hasMsg := op["message"]
					require.False(t, hasMsg, "terminal op without message should omit message key")
				},
			},
		},
		{
			name: "terminal failed op exposes error",
			allOps: []asynccfg.RegisterInput{{
				ID: "op-4", ParentConvID: "c1", ParentTurnID: "t1",
				ToolName: "system/exec:start", WaitForResponse: true, Status: "failed", Error: "process exited 1",
			}},
			changedRecords: []*asynccfg.OperationRecord{{
				ID: "op-4", ParentConvID: "c1", ParentTurnID: "t1",
				ToolName: "system/exec:start", WaitForResponse: true,
				State: asynccfg.StateFailed, Status: "failed", Error: "process exited 1",
			}},
			wantFailed: 1, wantAllResolved: true, wantOpsLen: 1,
			wantOpChecks: []func(t *testing.T, ops []map[string]interface{}){
				func(t *testing.T, ops []map[string]interface{}) {
					op := ops[0]
					require.Equal(t, true, op["terminal"])
					require.Equal(t, "process exited 1", op["error"])
				},
			},
		},
		{
			name: "non-terminal op with message surfaced",
			allOps: []asynccfg.RegisterInput{{
				ID: "op-5", ParentConvID: "c1", ParentTurnID: "t1",
				ToolName: "llm/agents:start", WaitForResponse: true, Status: "running", Message: "processing step 3",
			}},
			changedRecords: []*asynccfg.OperationRecord{{
				ID: "op-5", ParentConvID: "c1", ParentTurnID: "t1",
				ToolName: "llm/agents:start", WaitForResponse: true,
				State: asynccfg.StateRunning, Status: "running", Message: "processing step 3",
			}},
			wantPending: 1, wantAllResolved: false, wantOpsLen: 1,
			wantOpChecks: []func(t *testing.T, ops []map[string]interface{}){
				func(t *testing.T, ops []map[string]interface{}) {
					require.Equal(t, "processing step 3", ops[0]["message"])
				},
			},
		},
		{
			name: "op with Instruction surfaces in context",
			allOps: []asynccfg.RegisterInput{{
				ID: "op-6", ParentConvID: "c1", ParentTurnID: "t1",
				ToolName: "llm/agents:start", WaitForResponse: true, Status: "running",
				Instruction: "Do not call llm/agents:start again.",
			}},
			changedRecords: []*asynccfg.OperationRecord{{
				ID: "op-6", ParentConvID: "c1", ParentTurnID: "t1",
				ToolName: "llm/agents:start", WaitForResponse: true,
				State: asynccfg.StateRunning, Status: "running",
				Instruction: "Do not call llm/agents:start again.",
			}},
			wantPending: 1, wantAllResolved: false, wantOpsLen: 1,
			wantOpChecks: []func(t *testing.T, ops []map[string]interface{}){
				func(t *testing.T, ops []map[string]interface{}) {
					require.Equal(t, "Do not call llm/agents:start again.", ops[0]["instruction"])
				},
			},
		},
		{
			name: "terminal op with TerminalInstruction surfaces in context",
			allOps: []asynccfg.RegisterInput{{
				ID: "op-7", ParentConvID: "c1", ParentTurnID: "t1",
				ToolName: "llm/agents:start", WaitForResponse: true, Status: "completed",
				TerminalInstruction: "Answer from child conversation result.",
			}},
			changedRecords: []*asynccfg.OperationRecord{{
				ID: "op-7", ParentConvID: "c1", ParentTurnID: "t1",
				ToolName: "llm/agents:start", WaitForResponse: true,
				State: asynccfg.StateCompleted, Status: "completed",
				TerminalInstruction: "Answer from child conversation result.",
			}},
			wantCompleted: 1, wantAllResolved: true, wantOpsLen: 1,
			wantOpChecks: []func(t *testing.T, ops []map[string]interface{}){
				func(t *testing.T, ops []map[string]interface{}) {
					require.Equal(t, "Answer from child conversation result.", ops[0]["terminalInstruction"])
					_, hasInst := ops[0]["instruction"]
					require.False(t, hasInst, "terminal op should not surface instruction")
				},
			},
		},
		{
			name: "mixed ops: one active, one completed — turn counts correct",
			allOps: []asynccfg.RegisterInput{
				{ID: "op-a", ParentConvID: "c1", ParentTurnID: "t1", ToolName: "tool:start", WaitForResponse: true, Status: "running"},
				{ID: "op-b", ParentConvID: "c1", ParentTurnID: "t1", ToolName: "tool:start", WaitForResponse: true, Status: "completed"},
			},
			changedRecords: []*asynccfg.OperationRecord{
				{ID: "op-a", ParentConvID: "c1", ParentTurnID: "t1", ToolName: "tool:start", WaitForResponse: true, State: asynccfg.StateRunning, Status: "running"},
				{ID: "op-b", ParentConvID: "c1", ParentTurnID: "t1", ToolName: "tool:start", WaitForResponse: true, State: asynccfg.StateCompleted, Status: "completed"},
			},
			wantPending: 1, wantCompleted: 1, wantAllResolved: false, wantOpsLen: 2,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			mgr := asynccfg.NewManager()
			for i := range tc.allOps {
				inp := tc.allOps[i]
				rec := mgr.Register(ctx, inp)
				if rec != nil && asynccfg.DeriveState(inp.Status, inp.Error, "") == asynccfg.StateCompleted {
					mgr.Update(ctx, asynccfg.UpdateInput{ID: inp.ID, Status: inp.Status, State: asynccfg.StateCompleted})
				}
			}
			svc := &Service{asyncManager: mgr}
			result := svc.buildBatchedAsyncContext(ctx, tc.changedRecords)

			turnAsync, ok := result["turnAsync"].(map[string]interface{})
			require.True(t, ok, "turnAsync should be a map")
			require.Equal(t, tc.wantPending, turnAsync["pending"], "pending count")
			require.Equal(t, tc.wantCompleted, turnAsync["completed"], "completed count")
			require.Equal(t, tc.wantFailed, turnAsync["failed"], "failed count")
			require.Equal(t, tc.wantCanceled, turnAsync["canceled"], "canceled count")
			require.Equal(t, tc.wantAllResolved, turnAsync["allResolved"], "allResolved")

			changedOps, ok := result["changedOperations"].([]map[string]interface{})
			require.True(t, ok)
			require.Len(t, changedOps, tc.wantOpsLen)

			for _, check := range tc.wantOpChecks {
				check(t, changedOps)
			}
		})
	}
}

func TestRenderBatchedAsyncReinforcement(t *testing.T) {
	type testCase struct {
		name            string
		records         []*asynccfg.OperationRecord
		wantContains    []string
		wantNotContains []string
	}

	cases := []testCase{
		{
			name: "runtime-polled op does not expose status tool to model",
			records: []*asynccfg.OperationRecord{{
				ID: "sess-1", ParentConvID: "c1", ParentTurnID: "t1",
				ToolName: "system/exec:start", StatusToolName: "system/exec:status",
				StatusArgs:      map[string]interface{}{"sessionId": "sess-1"},
				WaitForResponse: true, State: asynccfg.StateRunning, Status: "running",
			}},
			wantContains: []string{
				"sess-1",
				"system/exec:start",
				"runtime polled: true",
				"do not call the status tool yourself",
				// Behavior: no same-tool reuse → autonomous branch
				"autonomously",
			},
			// Status tool name and args must NOT appear — they belong to the poller.
			wantNotContains: []string{
				"status tool:",
				"system/exec:status",
				`"sessionId":"sess-1"`,
				"same-tool reuse",
			},
		},
		{
			name: "same-tool reuse op exposes requestArgsJSON and triggers model polling",
			records: []*asynccfg.OperationRecord{{
				ID: "op-2", ParentConvID: "c1", ParentTurnID: "t1",
				ToolName: "forecasting:Total", StatusToolName: "forecasting:Total",
				RequestArgs:     map[string]interface{}{"viewId": "TOTAL"},
				WaitForResponse: true, State: asynccfg.StateWaiting, Status: "waiting",
			}},
			wantContains: []string{
				"op-2",
				"same-tool reuse",
				`"viewId":"TOTAL"`,
				// Behavior: hasSameToolReuse=true → polling branch
				"same-tool polling",
			},
			wantNotContains: []string{
				"status tool:",
				"runtime polled: true",
				// Behavior section must not say "autonomously" for a same-tool reuse op.
				// (Rule 3 in the header mentions it but the behavior section must not.)
				"being handled autonomously",
			},
		},
		{
			name: "terminal completed op shows resolved behavior",
			records: []*asynccfg.OperationRecord{{
				ID: "op-3", ParentConvID: "c1", ParentTurnID: "t1",
				ToolName: "system/exec:start", WaitForResponse: true,
				State: asynccfg.StateCompleted, Status: "completed",
			}},
			wantContains: []string{
				"op-3",
				"terminal: true",
				"terminal state",
				"Do not poll again",
				"answer the user",
			},
		},
		{
			name: "terminal failed op shows error",
			records: []*asynccfg.OperationRecord{{
				ID: "op-4", ParentConvID: "c1", ParentTurnID: "t1",
				ToolName: "system/exec:start", WaitForResponse: true,
				State: asynccfg.StateFailed, Status: "failed", Error: "exit code 1",
			}},
			wantContains: []string{
				"terminal: true",
				"error: exit code 1",
				"terminal state",
			},
		},
		{
			name: "non-terminal op message surfaced",
			records: []*asynccfg.OperationRecord{{
				ID: "op-5", ParentConvID: "c1", ParentTurnID: "t1",
				ToolName: "llm/agents:start", WaitForResponse: true,
				State: asynccfg.StateRunning, Status: "running", Message: "processing step 3",
			}},
			wantContains: []string{"message: processing step 3"},
		},
		{
			name: "terminal op message surfaced",
			records: []*asynccfg.OperationRecord{{
				ID: "op-5b", ParentConvID: "c1", ParentTurnID: "t1",
				ToolName: "llm/agents:start", WaitForResponse: true,
				State: asynccfg.StateCompleted, Status: "succeeded", Message: "child agent finished with final summary",
			}},
			wantContains: []string{"message: child agent finished with final summary"},
		},
		{
			name: "op with Instruction rendered in output",
			records: []*asynccfg.OperationRecord{{
				ID: "op-6", ParentConvID: "c1", ParentTurnID: "t1",
				ToolName: "llm/agents:start", WaitForResponse: true,
				State: asynccfg.StateRunning, Status: "running",
				Instruction: "Do not call llm/agents:start again.",
			}},
			wantContains: []string{"Do not call llm/agents:start again."},
		},
		{
			name: "terminal op with TerminalInstruction rendered in output",
			records: []*asynccfg.OperationRecord{{
				ID: "op-7", ParentConvID: "c1", ParentTurnID: "t1",
				ToolName: "llm/agents:start", WaitForResponse: true,
				State: asynccfg.StateCompleted, Status: "completed",
				TerminalInstruction: "Answer from child conversation result.",
			}},
			wantContains: []string{
				"terminal instruction: Answer from child conversation result.",
			},
			// "  instruction:" (non-terminal path) must not appear; "terminal instruction:" is fine.
			wantNotContains: []string{"  instruction: Answer"},
		},
		{
			name: "multiple ops: one runtime-polled active, one completed — both in output",
			records: []*asynccfg.OperationRecord{
				{
					ID: "child-1", ParentConvID: "c1", ParentTurnID: "t1",
					ToolName: "llm/agents:start", StatusToolName: "llm/agents:status",
					StatusArgs:      map[string]interface{}{"conversationId": "child-1"},
					WaitForResponse: true, State: asynccfg.StateRunning, Status: "running",
				},
				{
					ID: "child-2", ParentConvID: "c1", ParentTurnID: "t1",
					ToolName: "llm/agents:start", WaitForResponse: true,
					State: asynccfg.StateCompleted, Status: "completed",
				},
			},
			wantContains: []string{
				"child-1",
				"child-2",
				// Active op is runtime-polled; status tool must NOT appear.
				"runtime polled: true",
				// Behavior: no same-tool reuse → autonomous branch.
				"autonomously",
			},
			wantNotContains: []string{"llm/agents:status", "status tool:"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			mgr := asynccfg.NewManager()
			for _, rec := range tc.records {
				mgr.Register(ctx, asynccfg.RegisterInput{
					ID:              rec.ID,
					ParentConvID:    rec.ParentConvID,
					ParentTurnID:    rec.ParentTurnID,
					ToolName:        rec.ToolName,
					WaitForResponse: rec.WaitForResponse,
					Status:          rec.Status,
				})
			}
			svc := &Service{
				conversation: &recordingConvClient{},
				asyncManager: mgr,
			}
			content := svc.renderBatchedAsyncReinforcement(ctx, tc.records)
			require.NotEmpty(t, content, "rendered content should not be empty")

			for _, want := range tc.wantContains {
				require.Contains(t, content, want, "expected %q in output", want)
			}
			for _, notWant := range tc.wantNotContains {
				require.NotContains(t, content, notWant, "did not expect %q in output", notWant)
			}
		})
	}
}

func TestInjectAsyncReinforcementForRecords_SkipsNonWaitOps(t *testing.T) {
	ctx := context.Background()
	client := &recordingConvClient{}
	mgr := asynccfg.NewManager()
	turn := &memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}
	svc := &Service{asyncManager: mgr, conversation: client}

	records := []*asynccfg.OperationRecord{{
		ID: "child-1", ParentConvID: "conv-1", ParentTurnID: "turn-1",
		ToolName: "llm/agents:start", WaitForResponse: false,
		State: asynccfg.StateRunning, Status: "running", Message: "child launched",
	}}

	svc.injectAsyncReinforcementForRecords(ctx, turn, records)

	require.Nil(t, client.lastMessage, "non-wait async ops should not emit async_wait reinforcement")
}

func TestInjectAsyncReinforcementForRecords_BatchesDefaultTemplateOps(t *testing.T) {
	// Records without custom prompts should produce exactly ONE system message
	// (the batch), not one per operation.
	ctx := context.Background()
	client := &recordingConvClient{}
	mgr := asynccfg.NewManager()
	svc := &Service{conversation: client, asyncManager: mgr}

	turn := &memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}

	records := []*asynccfg.OperationRecord{
		{
			ID: "op-a", ParentConvID: "conv-1", ParentTurnID: "turn-1",
			ToolName: "llm/agents:start", WaitForResponse: true,
			State: asynccfg.StateRunning, Status: "running",
		},
		{
			ID: "op-b", ParentConvID: "conv-1", ParentTurnID: "turn-1",
			ToolName: "llm/agents:start", WaitForResponse: true,
			State: asynccfg.StateCompleted, Status: "completed",
		},
	}
	for _, rec := range records {
		mgr.Register(ctx, asynccfg.RegisterInput{
			ID:              rec.ID,
			ParentConvID:    rec.ParentConvID,
			ParentTurnID:    rec.ParentTurnID,
			ToolName:        rec.ToolName,
			WaitForResponse: rec.WaitForResponse,
			Status:          rec.Status,
		})
	}

	svc.injectAsyncReinforcementForRecords(ctx, turn, records)

	require.Equal(t, 1, client.messageCount, "two default-template ops should produce exactly one batched system message")
	content := *client.lastMessage.Content
	require.Contains(t, content, "op-a")
	require.Contains(t, content, "op-b")
}

func TestBuildBatchedAsyncContext_RuntimePolledVsSameToolReuse(t *testing.T) {
	type testCase struct {
		name              string
		rec               *asynccfg.OperationRecord
		wantRuntimePolled bool
		wantSameToolReuse bool
		wantHasSameTool   bool
		wantHasStatusTool bool // statusToolName present in op entry
		wantHasReqArgs    bool // requestArgsJSON present in op entry
	}
	cases := []testCase{
		{
			name: "distinct status tool + non-wait op → explicit status tool in context",
			rec: &asynccfg.OperationRecord{
				ID: "op-1", ParentConvID: "c1", ParentTurnID: "t1",
				ToolName: "llm/agents:start", StatusToolName: "llm/agents:status",
				StatusArgs:      map[string]interface{}{"conversationId": "child-1"},
				WaitForResponse: false, State: asynccfg.StateRunning,
			},
			wantRuntimePolled: false,
			wantSameToolReuse: false,
			wantHasSameTool:   false,
			wantHasStatusTool: true,
			wantHasReqArgs:    false,
		},
		{
			name: "same-tool reuse → not runtimePolled, requestArgsJSON exposed",
			rec: &asynccfg.OperationRecord{
				ID: "op-2", ParentConvID: "c1", ParentTurnID: "t1",
				ToolName: "forecast:Total", StatusToolName: "forecast:Total",
				RequestArgs:     map[string]interface{}{"viewId": "TOTAL"},
				WaitForResponse: true, State: asynccfg.StateWaiting,
			},
			wantRuntimePolled: false,
			wantSameToolReuse: true,
			wantHasSameTool:   true,
			wantHasStatusTool: false,
			wantHasReqArgs:    true,
		},
		{
			name: "no status tool → neither flag, no args exposed",
			rec: &asynccfg.OperationRecord{
				ID: "op-3", ParentConvID: "c1", ParentTurnID: "t1",
				ToolName:        "llm/agents:start",
				WaitForResponse: true, State: asynccfg.StateRunning,
			},
			wantRuntimePolled: false,
			wantSameToolReuse: false,
			wantHasSameTool:   false,
			wantHasStatusTool: false,
			wantHasReqArgs:    false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			mgr := asynccfg.NewManager()
			mgr.Register(ctx, asynccfg.RegisterInput{
				ID: tc.rec.ID, ParentConvID: tc.rec.ParentConvID,
				ParentTurnID: tc.rec.ParentTurnID, ToolName: tc.rec.ToolName,
				WaitForResponse: tc.rec.WaitForResponse, Status: "running",
			})
			svc := &Service{asyncManager: mgr}
			result := svc.buildBatchedAsyncContext(ctx, []*asynccfg.OperationRecord{tc.rec})

			ops := result["changedOperations"].([]map[string]interface{})
			require.Len(t, ops, 1)
			op := ops[0]

			require.Equal(t, tc.wantRuntimePolled, op["runtimePolled"], "runtimePolled")
			require.Equal(t, tc.wantSameToolReuse, op["sameToolReuse"], "sameToolReuse")

			_, hasStatusTool := op["statusToolName"]
			require.Equal(t, tc.wantHasStatusTool, hasStatusTool, "statusToolName presence")
			_, hasReqArgs := op["requestArgsJSON"]
			require.Equal(t, tc.wantHasReqArgs, hasReqArgs, "requestArgsJSON presence")

			turnAsync := result["turnAsync"].(map[string]interface{})
			require.Equal(t, tc.wantHasSameTool, turnAsync["hasSameToolReuse"], "hasSameToolReuse in turnAsync")
		})
	}
}

func TestRenderModelManagedAsyncControl_IncludesExplicitStatusTool(t *testing.T) {
	ctx := context.Background()
	mgr := asynccfg.NewManager()
	svc := &Service{asyncManager: mgr}
	turn := &memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}

	mgr.Register(ctx, asynccfg.RegisterInput{
		ID:              "child-1",
		ParentConvID:    "conv-1",
		ParentTurnID:    "turn-1",
		ToolName:        "llm/agents:start",
		StatusToolName:  "llm/agents:status",
		StatusArgs:      map[string]interface{}{"conversationId": "child-1"},
		WaitForResponse: false,
		Status:          "running",
	})

	content := svc.renderModelManagedAsyncControl(ctx, turn)
	require.Contains(t, content, "status tool: `llm/agents:status`")
	require.Contains(t, content, "status tool args:")
	require.Contains(t, content, `"conversationId":"child-1"`)
	require.Contains(t, content, "separate status function")
	require.NotContains(t, content, "All async operations reached terminal state")
}

func TestResolveAsyncReinforcementPrompt_UsesDefaultsWhenConfigured(t *testing.T) {
	// P2: renderBatchedAsyncReinforcement must prefer the workspace/defaults prompt
	// over the embedded fallback when one is configured.
	ctx := context.Background()
	client := &recordingConvClient{}
	mgr := asynccfg.NewManager()

	svc := &Service{
		conversation: client,
		asyncManager: mgr,
		defaults: &config.Defaults{
			AsyncReinforcementPrompt: &binding.Prompt{
				Text:   `CUSTOM-PROMPT op={{.Context.changedOperations}}`,
				Engine: "go",
			},
		},
	}

	mgr.Register(ctx, asynccfg.RegisterInput{
		ID: "op-1", ParentConvID: "conv-1", ParentTurnID: "turn-1",
		ToolName: "tool:start", WaitForResponse: true, Status: "running",
	})

	turn := &memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}
	svc.injectAsyncReinforcementForRecords(ctx, turn, []*asynccfg.OperationRecord{{
		ID: "op-1", ParentConvID: "conv-1", ParentTurnID: "turn-1",
		ToolName: "tool:start", WaitForResponse: true, State: asynccfg.StateRunning,
	}})

	require.NotNil(t, client.lastMessage)
	require.NotNil(t, client.lastMessage.Content)
	require.Contains(t, *client.lastMessage.Content, "CUSTOM-PROMPT",
		"defaults AsyncReinforcementPrompt must override embedded fallback")
}

func TestMarkAssistantMessageInterim_PatchesLatestAssistantMessage(t *testing.T) {
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
		ConversationID: "conv-1",
		TurnID:         "turn-1",
	})
	ctx = context.WithValue(ctx, memory.ModelMessageIDKey, "assistant-1")

	client := &recordingConvClient{}
	svc := &Service{conversation: client}
	turn := &memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}

	svc.markAssistantMessageInterim(ctx, turn, &core.GenerateOutput{MessageID: "assistant-1"})

	require.NotNil(t, client.lastMessage)
	require.Equal(t, "assistant-1", client.lastMessage.Id)
	require.Equal(t, "conv-1", client.lastMessage.ConversationID)
	require.NotNil(t, client.lastMessage.Interim)
	require.Equal(t, 1, *client.lastMessage.Interim)
}

type terminalAsyncFinder struct {
	content string
}

func (f *terminalAsyncFinder) Find(context.Context, string) (llm.Model, error) {
	return terminalAsyncModel{content: f.content}, nil
}

type terminalAsyncModel struct {
	content string
}

func (m terminalAsyncModel) Generate(context.Context, *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	return &llm.GenerateResponse{
		Choices: []llm.Choice{{
			Index:   0,
			Message: llm.NewAssistantMessage(m.content),
		}},
	}, nil
}

func (m terminalAsyncModel) Implements(string) bool { return false }

func TestServiceRunPlanAndStatus_AllowsModelFinalAnswerAfterTerminalAsyncState(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		state        asynccfg.State
		status       string
		finalContent string
	}{
		{
			name:         "failed async op does not abort",
			state:        asynccfg.StateFailed,
			status:       "failed",
			finalContent: "ASYNC_FAIL_DONE status=failed",
		},
		{
			name:         "canceled async op does not abort",
			state:        asynccfg.StateCanceled,
			status:       "canceled",
			finalContent: "ASYNC_CANCEL_DONE status=canceled",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			convClient := &dedupeConvClient{
				conversation: &apiconv.Conversation{
					Id: "conv-1",
					Transcript: []*agconv.TranscriptView{
						{
							Id:             "turn-1",
							ConversationId: "conv-1",
							Message: []*agconv.MessageView{
								{
									Id:             "user-1",
									ConversationId: "conv-1",
									Role:           "user",
									Type:           "task",
									Content:        cancelPtr("hello"),
									TurnId:         cancelPtr("turn-1"),
								},
							},
						},
					},
				},
			}

			llmSvc := core.New(&terminalAsyncFinder{content: tc.finalContent}, nil, convClient)
			svc := &Service{
				llm:          llmSvc,
				conversation: convClient,
				orchestrator: reactor.New(llmSvc, nil, convClient, nil, nil),
				defaults:     &config.Defaults{},
				asyncManager: asynccfg.NewManager(),
			}

			svc.asyncManager.Register(context.Background(), asynccfg.RegisterInput{
				ID:              "op-1",
				ParentConvID:    "conv-1",
				ParentTurnID:    "turn-1",
				ToolName:        "system/exec:start",
				WaitForResponse: true,
				Status:          tc.status,
				Error: func() string {
					if tc.state == asynccfg.StateFailed {
						return "boom"
					}
					return ""
				}(),
			})
			_, _ = svc.asyncManager.Update(context.Background(), asynccfg.UpdateInput{
				ID:     "op-1",
				Status: tc.status,
				Error: func() string {
					if tc.state == asynccfg.StateFailed {
						return "boom"
					}
					return ""
				}(),
				State: tc.state,
			})

			input := &QueryInput{
				ConversationID: "conv-1",
				UserId:         "user-1",
				Query:          "hello",
				Agent: &agentmdl.Agent{
					Identity: agentmdl.Identity{ID: "simple"},
					ModelSelection: llm.ModelSelection{
						Model: "mock-model",
					},
					Prompt: &binding.Prompt{Text: "You are helpful."},
				},
			}
			output := &QueryOutput{}
			ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
				ConversationID: "conv-1",
				TurnID:         "turn-1",
			})
			ctx = memory.WithRunMeta(ctx, memory.RunMeta{RunID: "turn-1"})

			status, err := svc.runPlanAndStatus(ctx, input, output)
			require.NoError(t, err)
			require.Equal(t, "succeeded", status)
			require.NotNil(t, output)
			require.Equal(t, tc.finalContent, strings.TrimSpace(output.Content))
		})
	}
}

func TestServiceRunPlanAndStatus_DoesNotFinalizeWhileAnyAsyncWaitOpRemains(t *testing.T) {
	t.Parallel()

	convClient := &dedupeConvClient{
		conversation: &apiconv.Conversation{
			Id: "conv-1",
			Transcript: []*agconv.TranscriptView{
				{
					Id:             "turn-1",
					ConversationId: "conv-1",
					Message: []*agconv.MessageView{
						{
							Id:             "user-1",
							ConversationId: "conv-1",
							Role:           "user",
							Type:           "task",
							Content:        cancelPtr("hello"),
							TurnId:         cancelPtr("turn-1"),
						},
					},
				},
			},
		},
	}

	llmSvc := core.New(&terminalAsyncFinder{content: "SHOULD_NOT_FINALIZE"}, nil, convClient)
	svc := &Service{
		llm:          llmSvc,
		conversation: convClient,
		orchestrator: reactor.New(llmSvc, nil, convClient, nil, nil),
		defaults:     &config.Defaults{},
		asyncManager: asynccfg.NewManager(),
	}

	svc.asyncManager.Register(context.Background(), asynccfg.RegisterInput{
		ID:              "op-complete",
		ParentConvID:    "conv-1",
		ParentTurnID:    "turn-1",
		ToolName:        "forecasting-Total",
		WaitForResponse: true,
		Status:          "COMPLETE",
	})
	_, _ = svc.asyncManager.Update(context.Background(), asynccfg.UpdateInput{
		ID:     "op-complete",
		Status: "COMPLETE",
		State:  asynccfg.StateCompleted,
	})

	svc.asyncManager.Register(context.Background(), asynccfg.RegisterInput{
		ID:              "op-waiting",
		ParentConvID:    "conv-1",
		ParentTurnID:    "turn-1",
		ToolName:        "forecasting-Total",
		WaitForResponse: true,
		Status:          "WAITING",
		PollIntervalMs:  500,
	})

	input := &QueryInput{
		ConversationID: "conv-1",
		UserId:         "user-1",
		Query:          "hello",
		Agent: &agentmdl.Agent{
			Identity: agentmdl.Identity{ID: "simple"},
			ModelSelection: llm.ModelSelection{
				Model: "mock-model",
			},
			Prompt: &binding.Prompt{Text: "You are helpful."},
		},
	}
	output := &QueryOutput{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
		ConversationID: "conv-1",
		TurnID:         "turn-1",
	})
	ctx = memory.WithRunMeta(ctx, memory.RunMeta{RunID: "turn-1"})
	ctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	status, err := svc.runPlanAndStatus(ctx, input, output)
	require.Error(t, err)
	require.Equal(t, "canceled", status)
	require.NotEqual(t, "SHOULD_NOT_FINALIZE", strings.TrimSpace(output.Content))
}
