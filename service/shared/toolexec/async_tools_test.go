package toolexec

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	asynccfg "github.com/viant/agently-core/protocol/async"
	memory "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	modelcallctx "github.com/viant/agently-core/service/core/modelcall"
)

type asyncRegistry struct {
	scriptedRegistry
	cfg         *asynccfg.Config
	cancelCalls int
	callTimes   []time.Time
}

func (a *asyncRegistry) AsyncConfig(name string) (*asynccfg.Config, bool) {
	if a.cfg == nil {
		return nil, false
	}
	if name == a.cfg.Run.Tool || name == a.cfg.Status.Tool || (a.cfg.Cancel != nil && name == a.cfg.Cancel.Tool) {
		return a.cfg, true
	}
	return nil, false
}

func (a *asyncRegistry) Execute(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	a.callTimes = append(a.callTimes, time.Now())
	if a.cfg != nil && a.cfg.Cancel != nil && name == a.cfg.Cancel.Tool {
		a.cancelCalls++
		return `{"status":"canceled"}`, nil
	}
	return a.scriptedRegistry.Execute(ctx, name, args)
}

type captureStreamPublisher struct {
	events []*streaming.Event
}

func (c *captureStreamPublisher) Publish(_ context.Context, ev *modelcallctx.StreamEvent) error {
	if ev != nil && ev.Event != nil {
		c.events = append(c.events, ev.Event)
	}
	return nil
}

func TestExecuteToolStep_AsyncPublishesLifecycleEvents(t *testing.T) {
	cfg := &asynccfg.Config{
		WaitForResponse: true,
		PollIntervalMs:  5,
		Run: asynccfg.RunConfig{
			Tool:            "llm/agents:start",
			OperationIDPath: "conversationId",
			Selector:        &asynccfg.Selector{StatusPath: "status"},
		},
		Status: asynccfg.StatusConfig{
			Tool:           "llm/agents:status",
			OperationIDArg: "conversationId",
			Selector: asynccfg.Selector{
				StatusPath: "status",
				DataPath:   "items",
			},
		},
	}
	reg := &asyncRegistry{
		scriptedRegistry: scriptedRegistry{script: []scriptedResult{
			{result: `{"status":"running","conversationId":"child-1"}`},
			{result: `{"status":"completed","items":[{"conversationId":"child-1","status":"completed"}]}`},
		}},
		cfg: cfg,
	}
	pub := &captureStreamPublisher{}
	manager := asynccfg.NewManager()
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = memory.WithToolMessageID(ctx, "tool-msg-1")
	ctx = modelcallctx.WithStreamPublisher(ctx, pub)
	ctx = WithAsyncManager(ctx, manager)

	_, _, err := ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-1",
		Name: "llm/agents:start",
		Args: map[string]interface{}{"agentId": "coder", "objective": "analyze"},
	}, &stubConv{})
	require.NoError(t, err)

	deadline := time.Now().Add(time.Second)
	for len(pub.events) < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	require.NotEmpty(t, pub.events)

	var sawStarted, sawWaiting, sawCompleted bool
	for _, event := range pub.events {
		if event == nil {
			continue
		}
		if event.OperationID != "child-1" {
			continue
		}
		switch event.Type {
		case streaming.EventTypeToolCallStarted:
			sawStarted = true
		case streaming.EventTypeToolCallWaiting:
			sawWaiting = true
		case streaming.EventTypeToolCallCompleted:
			sawCompleted = true
		}
	}
	require.True(t, sawStarted)
	require.True(t, sawWaiting)
	require.False(t, sawCompleted)
	require.Equal(t, 1, reg.calls)
	rec, ok := manager.Get(context.Background(), "child-1")
	require.True(t, ok)
	require.NotNil(t, rec)
	require.False(t, rec.Terminal())
}

func TestExecuteToolStep_AsyncDoesNotAutoCancelWithoutLLMStatusCall(t *testing.T) {
	cfg := &asynccfg.Config{
		WaitForResponse: true,
		TimeoutMs:       20,
		PollIntervalMs:  5,
		Run: asynccfg.RunConfig{
			Tool:            "system/exec:start",
			OperationIDPath: "sessionId",
			Selector:        &asynccfg.Selector{StatusPath: "status"},
		},
		Status: asynccfg.StatusConfig{
			Tool:           "system/exec:status",
			OperationIDArg: "sessionId",
			Selector:       asynccfg.Selector{StatusPath: "status"},
		},
		Cancel: &asynccfg.CancelConfig{
			Tool:           "system/exec:cancel",
			OperationIDArg: "sessionId",
		},
	}
	reg := &asyncRegistry{
		scriptedRegistry: scriptedRegistry{script: []scriptedResult{
			{result: `{"status":"running","sessionId":"sess-1"}`},
			{result: `{"status":"running"}`},
			{result: `{"status":"running"}`},
		}},
		cfg: cfg,
	}
	pub := &captureStreamPublisher{}
	manager := asynccfg.NewManager()
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = memory.WithToolMessageID(ctx, "tool-msg-1")
	ctx = modelcallctx.WithStreamPublisher(ctx, pub)
	ctx = WithAsyncManager(ctx, manager)

	_, _, err := ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-1",
		Name: "system/exec:start",
		Args: map[string]interface{}{"commands": []string{"sleep 30"}},
	}, &stubConv{})
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)
	rec, ok := manager.Get(context.Background(), "sess-1")
	require.True(t, ok)
	require.NotNil(t, rec)
	require.False(t, rec.Terminal())
	require.Equal(t, 0, reg.cancelCalls)
	require.Equal(t, 1, reg.calls)
}

func TestExecuteToolStep_AsyncDoesNotAutoPollWithoutLLMStatusCall(t *testing.T) {
	cfg := &asynccfg.Config{
		WaitForResponse: true,
		PollIntervalMs:  5,
		Run: asynccfg.RunConfig{
			Tool:            "system/exec:start",
			OperationIDPath: "sessionId",
			Selector:        &asynccfg.Selector{StatusPath: "status"},
		},
		Status: asynccfg.StatusConfig{
			Tool:           "system/exec:status",
			OperationIDArg: "sessionId",
			Selector: asynccfg.Selector{
				StatusPath: "status",
				DataPath:   "stdout",
			},
		},
	}
	reg := &asyncRegistry{
		scriptedRegistry: scriptedRegistry{script: []scriptedResult{
			{result: `{"status":"running","sessionId":"sess-1"}`},
			{err: errors.New("temporary network timeout")},
			{err: errors.New("temporary network timeout")},
			{result: `{"status":"completed","stdout":"DONE"}`},
		}},
		cfg: cfg,
	}
	manager := asynccfg.NewManager()
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = memory.WithToolMessageID(ctx, "tool-msg-1")
	ctx = WithAsyncManager(ctx, manager)

	_, _, err := ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-1",
		Name: "system/exec:start",
		Args: map[string]interface{}{"commands": []string{"sleep 30"}},
	}, &stubConv{})
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)
	rec, ok := manager.Get(context.Background(), "sess-1")
	require.True(t, ok)
	require.NotNil(t, rec)
	require.False(t, rec.Terminal())
	require.Equal(t, 0, rec.PollFailures)
	require.Equal(t, 1, reg.calls)
}

func TestExecuteToolStep_SameToolRecallWaitsForPollWindow(t *testing.T) {
	cfg := &asynccfg.Config{
		WaitForResponse: true,
		PollIntervalMs:  80,
		Run: asynccfg.RunConfig{
			Tool:     "forecasting:Total",
			Selector: &asynccfg.Selector{StatusPath: "jobStatus"},
		},
		Status: asynccfg.StatusConfig{
			Tool:           "forecasting:Total",
			ReuseRunArgs:   true,
			Selector:       asynccfg.Selector{StatusPath: "jobStatus"},
			OperationIDArg: "",
		},
	}
	reg := &asyncRegistry{
		scriptedRegistry: scriptedRegistry{script: []scriptedResult{
			{result: `{"jobStatus":"WAITING","result":[]}`},
			{result: `{"jobStatus":"WAITING","result":[]}`},
		}},
		cfg: cfg,
	}
	manager := asynccfg.NewManager()
	conv := &stubConv{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncConversation(ctx, conv)
	args := map[string]interface{}{
		"DealsPmpIncl": []interface{}{142130},
		"From":         "2026-04-09T00:00:00Z",
		"To":           "2026-04-16T00:00:00Z",
	}

	_, _, err := ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-1",
		Name: "forecasting:Total",
		Args: args,
	}, conv)
	require.NoError(t, err)

	start := time.Now()
	_, _, err = ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-2",
		Name: "forecasting:Total",
		Args: args,
	}, conv)
	require.NoError(t, err)
	require.GreaterOrEqual(t, time.Since(start), 70*time.Millisecond)
	require.Len(t, reg.callTimes, 2)
	require.GreaterOrEqual(t, reg.callTimes[1].Sub(reg.callTimes[0]), 70*time.Millisecond)
}

func TestMaybeHandleAsyncTool_SameToolTerminalMarksAfterStatus(t *testing.T) {
	cfg := &asynccfg.Config{
		WaitForResponse: true,
		Run: asynccfg.RunConfig{
			Tool:     "forecasting:Total",
			Selector: &asynccfg.Selector{StatusPath: "jobStatus", DataPath: "result"},
		},
		Status: asynccfg.StatusConfig{
			Tool:         "forecasting:Total",
			ReuseRunArgs: true,
			Selector:     asynccfg.Selector{StatusPath: "jobStatus", DataPath: "result"},
		},
	}
	reg := &asyncRegistry{cfg: cfg}
	manager := asynccfg.NewManager()
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncWaitState(ctx)

	manager.Register(ctx, asynccfg.RegisterInput{
		ID:                "forecasting:Total:{\"DealsPmpIncl\":[142130]}",
		ParentConvID:      "conv-1",
		ParentTurnID:      "turn-1",
		ToolCallID:        "call-1",
		ToolName:          "forecasting:Total",
		RequestArgsDigest: `{"DealsPmpIncl":[142130]}`,
		RequestArgs:       map[string]interface{}{"DealsPmpIncl": []int{142130}},
		WaitForResponse:   true,
		Status:            "WAITING",
	})
	_ = manager.ConsumeChanged("conv-1", "turn-1")

	rec := maybeHandleAsyncTool(ctx, reg, StepInfo{
		ID:   "call-2",
		Name: "forecasting:Total",
		Args: map[string]interface{}{"DealsPmpIncl": []int{142130}},
	}, `{"jobStatus":"COMPLETE","result":[{"inventory":1}]}`, nil)
	require.Nil(t, rec)
	require.Equal(t, []string{`forecasting:Total:{"DealsPmpIncl":[142130]}`}, ConsumeAsyncWaitAfterStatus(ctx))
	changed := manager.ConsumeChanged("conv-1", "turn-1")
	require.Len(t, changed, 1)
	require.Equal(t, asynccfg.StateCompleted, changed[0].State)
}

func TestExecuteToolStep_AsyncStartDoesNotCompleteImmediately(t *testing.T) {
	cfg := &asynccfg.Config{
		WaitForResponse: true,
		PollIntervalMs:  50,
		Run: asynccfg.RunConfig{
			Tool:            "llm/agents:start",
			OperationIDPath: "conversationId",
			Selector:        &asynccfg.Selector{StatusPath: "status"},
		},
		Status: asynccfg.StatusConfig{
			Tool:           "llm/agents:status",
			OperationIDArg: "conversationId",
			Selector:       asynccfg.Selector{StatusPath: "status"},
		},
	}
	conv := &stubConv{}
	reg := &asyncRegistry{
		scriptedRegistry: scriptedRegistry{script: []scriptedResult{
			{result: `{"status":"running","conversationId":"child-1"}`},
			{result: `{"status":"completed"}`},
		}},
		cfg: cfg,
	}
	manager := asynccfg.NewManager()
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = memory.WithToolMessageID(ctx, "tool-msg-1")
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncConversation(ctx, conv)

	_, _, err := ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-1",
		Name: "llm/agents:start",
		Args: map[string]interface{}{"agentId": "coder", "objective": "analyze"},
	}, conv)
	require.NoError(t, err)

	var sawRunning bool
	for _, patched := range conv.patchedToolCalls {
		if patched == nil || patched.Status == "" {
			continue
		}
		if patched.Status == "running" {
			sawRunning = true
		}
	}
	require.True(t, sawRunning, "expected async start tool call to remain running immediately after start")
}

func TestExecuteToolStep_AsyncCompletionPersistsResponsePayload(t *testing.T) {
	cfg := &asynccfg.Config{
		WaitForResponse: true,
		PollIntervalMs:  5,
		Run: asynccfg.RunConfig{
			Tool:            "llm/agents:start",
			OperationIDPath: "conversationId",
			Selector:        &asynccfg.Selector{StatusPath: "status"},
		},
		Status: asynccfg.StatusConfig{
			Tool:           "llm/agents:status",
			OperationIDArg: "conversationId",
			Selector: asynccfg.Selector{
				StatusPath: "status",
				DataPath:   "items",
			},
		},
	}
	conv := &stubConv{}
	reg := &asyncRegistry{
		scriptedRegistry: scriptedRegistry{script: []scriptedResult{
			{result: `{"status":"running","conversationId":"child-1"}`},
			{result: `{"status":"completed","items":[{"conversationId":"child-1","status":"completed"}]}`},
		}},
		cfg: cfg,
	}
	manager := asynccfg.NewManager()
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = memory.WithToolMessageID(ctx, "tool-msg-1")
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncConversation(ctx, conv)

	_, _, err := ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-1",
		Name: "llm/agents:start",
		Args: map[string]interface{}{"agentId": "coder", "objective": "analyze"},
	}, conv)
	require.NoError(t, err)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, call := range conv.patchedToolCalls {
			if call == nil || call.ResponsePayloadID == nil {
				continue
			}
			if strings.TrimSpace(*call.ResponsePayloadID) != "" {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for async completion response payload persistence")
}

func TestMaybeHandleAsyncTool_StatusPublishesFailedLifecycleEvent(t *testing.T) {
	cfg := &asynccfg.Config{
		WaitForResponse: true,
		Run: asynccfg.RunConfig{
			Tool:            "system/exec:start",
			OperationIDPath: "sessionId",
			Selector:        &asynccfg.Selector{StatusPath: "status"},
		},
		Status: asynccfg.StatusConfig{
			Tool:           "system/exec:status",
			OperationIDArg: "sessionId",
			Selector: asynccfg.Selector{
				StatusPath: "status",
				DataPath:   "stdout",
				ErrorPath:  "stderr",
			},
		},
	}
	reg := &asyncRegistry{cfg: cfg}
	pub := &captureStreamPublisher{}
	manager := asynccfg.NewManager()
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = memory.WithToolMessageID(ctx, "tool-msg-1")
	ctx = modelcallctx.WithStreamPublisher(ctx, pub)
	ctx = WithAsyncManager(ctx, manager)
	manager.Register(ctx, asynccfg.RegisterInput{
		ID:                "sess-1",
		ParentConvID:      "conv-1",
		ParentTurnID:      "turn-1",
		ToolCallID:        "call-1",
		ToolMessageID:     "tool-msg-1",
		ToolName:          "system/exec:start",
		RequestArgsDigest: requestArgsDigest(cfg, map[string]interface{}{"sessionId": "sess-1"}),
		WaitForResponse:   true,
		Status:            "running",
	})
	_ = manager.ConsumeChanged("conv-1", "turn-1")

	rec := maybeHandleAsyncTool(ctx, reg, StepInfo{
		ID:   "call-2",
		Name: "system/exec:status",
		Args: map[string]interface{}{"sessionId": "sess-1"},
	}, `{"status":"failed","stdout":"WAITING","stderr":"TERMINAL_FAILURE"}`, nil)
	require.Nil(t, rec)

	var sawFailed bool
	for _, event := range pub.events {
		if event == nil || event.OperationID != "sess-1" {
			continue
		}
		if event.Type == streaming.EventTypeToolCallFailed {
			sawFailed = true
			require.Equal(t, "failed", event.Status)
			require.Equal(t, "TERMINAL_FAILURE", event.Error)
		}
	}
	require.True(t, sawFailed)
}

func TestMaybeHandleAsyncTool_StatusPublishesCanceledLifecycleEvent(t *testing.T) {
	cfg := &asynccfg.Config{
		WaitForResponse: true,
		Run: asynccfg.RunConfig{
			Tool:            "system/exec:start",
			OperationIDPath: "sessionId",
			Selector:        &asynccfg.Selector{StatusPath: "status"},
		},
		Status: asynccfg.StatusConfig{
			Tool:           "system/exec:status",
			OperationIDArg: "sessionId",
			Selector: asynccfg.Selector{
				StatusPath: "status",
				DataPath:   "stdout",
			},
		},
	}
	reg := &asyncRegistry{cfg: cfg}
	pub := &captureStreamPublisher{}
	manager := asynccfg.NewManager()
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = memory.WithToolMessageID(ctx, "tool-msg-1")
	ctx = modelcallctx.WithStreamPublisher(ctx, pub)
	ctx = WithAsyncManager(ctx, manager)
	manager.Register(ctx, asynccfg.RegisterInput{
		ID:                "sess-1",
		ParentConvID:      "conv-1",
		ParentTurnID:      "turn-1",
		ToolCallID:        "call-1",
		ToolMessageID:     "tool-msg-1",
		ToolName:          "system/exec:start",
		RequestArgsDigest: requestArgsDigest(cfg, map[string]interface{}{"sessionId": "sess-1"}),
		WaitForResponse:   true,
		Status:            "running",
	})
	_ = manager.ConsumeChanged("conv-1", "turn-1")

	rec := maybeHandleAsyncTool(ctx, reg, StepInfo{
		ID:   "call-2",
		Name: "system/exec:status",
		Args: map[string]interface{}{"sessionId": "sess-1"},
	}, `{"status":"canceled","stdout":"canceled"}`, nil)
	require.Nil(t, rec)

	var sawCanceled bool
	for _, event := range pub.events {
		if event == nil || event.OperationID != "sess-1" {
			continue
		}
		if event.Type == streaming.EventTypeToolCallCanceled {
			sawCanceled = true
			require.Equal(t, "canceled", event.Status)
		}
	}
	require.True(t, sawCanceled)
}

func TestMaybeHandleAsyncTool_StatusMarksRuntimeWaitWithoutSignal(t *testing.T) {
	cfg := &asynccfg.Config{
		WaitForResponse: true,
		Run: asynccfg.RunConfig{
			Tool:            "system/exec:start",
			OperationIDPath: "sessionId",
			Selector:        &asynccfg.Selector{StatusPath: "status"},
		},
		Status: asynccfg.StatusConfig{
			Tool:           "system/exec:status",
			OperationIDArg: "sessionId",
			Selector: asynccfg.Selector{
				StatusPath: "status",
				DataPath:   "stdout",
				ErrorPath:  "stderr",
			},
		},
	}
	reg := &asyncRegistry{cfg: cfg}
	manager := asynccfg.NewManager()
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncWaitState(ctx)

	manager.Register(ctx, asynccfg.RegisterInput{
		ID:              "sess-1",
		ParentConvID:    "conv-1",
		ParentTurnID:    "turn-1",
		ToolName:        "system/exec:start",
		WaitForResponse: true,
		Status:          "running",
	})
	_ = manager.ConsumeChanged("conv-1", "turn-1")

	rec := maybeHandleAsyncTool(ctx, reg, StepInfo{
		ID:   "call-2",
		Name: "system/exec:status",
		Args: map[string]interface{}{"sessionId": "sess-1"},
	}, `{"status":"running","stdout":"WAITING_FOR_READY_SIGNAL"}`, nil)
	require.Nil(t, rec)
	require.Equal(t, []string{"sess-1"}, ConsumeAsyncWaitAfterStatus(ctx))
	changed := manager.ConsumeChanged("conv-1", "turn-1")
	require.Len(t, changed, 1)
	require.Equal(t, "running", changed[0].Status)
}

func TestMaybeHandleAsyncTool_StatusTerminalPatchesOriginalAsyncToolCall(t *testing.T) {
	cfg := &asynccfg.Config{
		WaitForResponse: true,
		Run: asynccfg.RunConfig{
			Tool:            "system/exec:start",
			OperationIDPath: "sessionId",
			Selector:        &asynccfg.Selector{StatusPath: "status"},
		},
		Status: asynccfg.StatusConfig{
			Tool:           "system/exec:status",
			OperationIDArg: "sessionId",
			Selector: asynccfg.Selector{
				StatusPath: "status",
				DataPath:   "stdout",
				ErrorPath:  "stderr",
			},
		},
	}
	reg := &asyncRegistry{cfg: cfg}
	manager := asynccfg.NewManager()
	conv := &stubConv{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncWaitState(ctx)
	ctx = WithAsyncConversation(ctx, conv)

	manager.Register(ctx, asynccfg.RegisterInput{
		ID:              "sess-1",
		ParentConvID:    "conv-1",
		ParentTurnID:    "turn-1",
		ToolCallID:      "call-1",
		ToolMessageID:   "tool-msg-1",
		ToolName:        "system/exec:start",
		WaitForResponse: true,
		Status:          "running",
	})
	_ = manager.ConsumeChanged("conv-1", "turn-1")

	rec := maybeHandleAsyncTool(ctx, reg, StepInfo{
		ID:   "call-2",
		Name: "system/exec:status",
		Args: map[string]interface{}{"sessionId": "sess-1"},
	}, `{"status":"failed","stdout":"WAITING","stderr":"TERMINAL_FAILURE"}`, nil)
	require.Nil(t, rec)

	var sawFailed bool
	for _, patched := range conv.patchedToolCalls {
		if patched == nil || patched.Status == "" {
			continue
		}
		if patched.Status == "failed" {
			sawFailed = true
			if patched.ResponsePayloadID != nil {
				require.NotEmpty(t, strings.TrimSpace(*patched.ResponsePayloadID))
			}
		}
	}
	require.True(t, sawFailed, "expected terminal status tool call to patch original async start tool call to failed")
}

func TestMaybeHandleAsyncTool_StatusCanceledPatchesOriginalAsyncToolCall(t *testing.T) {
	cfg := &asynccfg.Config{
		WaitForResponse: true,
		Run: asynccfg.RunConfig{
			Tool:            "system/exec:start",
			OperationIDPath: "sessionId",
			Selector:        &asynccfg.Selector{StatusPath: "status"},
		},
		Status: asynccfg.StatusConfig{
			Tool:           "system/exec:status",
			OperationIDArg: "sessionId",
			Selector: asynccfg.Selector{
				StatusPath: "status",
				DataPath:   "stdout",
			},
		},
	}
	reg := &asyncRegistry{cfg: cfg}
	manager := asynccfg.NewManager()
	conv := &stubConv{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncWaitState(ctx)
	ctx = WithAsyncConversation(ctx, conv)

	manager.Register(ctx, asynccfg.RegisterInput{
		ID:              "sess-1",
		ParentConvID:    "conv-1",
		ParentTurnID:    "turn-1",
		ToolCallID:      "call-1",
		ToolMessageID:   "tool-msg-1",
		ToolName:        "system/exec:start",
		WaitForResponse: true,
		Status:          "running",
	})
	_ = manager.ConsumeChanged("conv-1", "turn-1")

	rec := maybeHandleAsyncTool(ctx, reg, StepInfo{
		ID:   "call-2",
		Name: "system/exec:status",
		Args: map[string]interface{}{"sessionId": "sess-1"},
	}, `{"status":"canceled","stdout":"canceled"}`, nil)
	require.Nil(t, rec)

	var sawCanceled bool
	for _, patched := range conv.patchedToolCalls {
		if patched == nil || patched.Status == "" {
			continue
		}
		if patched.Status == "canceled" {
			sawCanceled = true
			if patched.ResponsePayloadID != nil {
				require.NotEmpty(t, strings.TrimSpace(*patched.ResponsePayloadID))
			}
		}
	}
	require.True(t, sawCanceled, "expected terminal canceled status tool call to patch original async start tool call to canceled")
}

func TestMaybeHandleAsyncTool_SameToolReuseRunArgsTreatsRecallAsStatus(t *testing.T) {
	cfg := &asynccfg.Config{
		WaitForResponse: true,
		Run: asynccfg.RunConfig{
			Tool:     "forecasting:TotalV1",
			Selector: &asynccfg.Selector{StatusPath: "jobStatus", DataPath: "result"},
		},
		Status: asynccfg.StatusConfig{
			Tool:         "forecasting:TotalV1",
			ReuseRunArgs: true,
			Selector: asynccfg.Selector{
				StatusPath: "jobStatus",
				DataPath:   "result",
			},
		},
	}
	reg := &asyncRegistry{cfg: cfg}
	manager := asynccfg.NewManager()
	conv := &stubConv{}
	args := map[string]interface{}{
		"viewId": "TOTAL",
		"from":   "2026-04-09T00:00:00Z",
		"to":     "2026-04-16T00:00:00Z",
	}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncWaitState(ctx)
	ctx = WithAsyncConversation(ctx, conv)

	manager.Register(ctx, asynccfg.RegisterInput{
		ID:                "call-1",
		ParentConvID:      "conv-1",
		ParentTurnID:      "turn-1",
		ToolCallID:        "call-1",
		ToolMessageID:     "tool-msg-1",
		ToolName:          "forecasting:TotalV1",
		RequestArgsDigest: requestArgsDigest(cfg, args),
		WaitForResponse:   true,
		Status:            "WAITING",
	})
	_ = manager.ConsumeChanged("conv-1", "turn-1")

	rec := maybeHandleAsyncTool(ctx, reg, StepInfo{
		ID:   "call-2",
		Name: "forecasting:TotalV1",
		Args: args,
	}, `{"jobStatus":"WAITING","result":[]}`, nil)
	require.Nil(t, rec)
	require.Equal(t, []string{"call-1"}, ConsumeAsyncWaitAfterStatus(ctx))

	active, ok := manager.Get(context.Background(), "call-1")
	require.True(t, ok)
	require.NotNil(t, active)
	require.Equal(t, "WAITING", active.Status)
}

func TestExecuteToolStep_AsyncStartWithoutOperationIDUsesSyntheticID(t *testing.T) {
	cfg := &asynccfg.Config{
		WaitForResponse: true,
		Run: asynccfg.RunConfig{
			Tool:     "forecasting:TotalV1",
			Selector: &asynccfg.Selector{StatusPath: "jobStatus", DataPath: "result"},
		},
		Status: asynccfg.StatusConfig{
			Tool:         "forecasting:TotalV1",
			ReuseRunArgs: true,
			Selector: asynccfg.Selector{
				StatusPath: "jobStatus",
				DataPath:   "result",
			},
		},
	}
	reg := &asyncRegistry{
		scriptedRegistry: scriptedRegistry{script: []scriptedResult{
			{result: `{"jobStatus":"WAITING","result":[]}`},
		}},
		cfg: cfg,
	}
	manager := asynccfg.NewManager()
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = memory.WithToolMessageID(ctx, "tool-msg-1")
	ctx = WithAsyncManager(ctx, manager)

	_, _, err := ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-1",
		Name: "forecasting:TotalV1",
		Args: map[string]interface{}{"viewId": "TOTAL"},
	}, &stubConv{})
	require.NoError(t, err)

	rec, ok := manager.Get(context.Background(), "call-1")
	require.True(t, ok)
	require.NotNil(t, rec)
	require.Equal(t, "call-1", rec.ID)
	require.Equal(t, "forecasting:TotalV1", rec.ToolName)
}

func TestExecuteToolStep_AsyncStartTerminalDoesNotRemainRunning(t *testing.T) {
	cfg := &asynccfg.Config{
		WaitForResponse: true,
		Run: asynccfg.RunConfig{
			Tool:     "forecasting:Total",
			Selector: &asynccfg.Selector{StatusPath: "jobStatus", DataPath: "result"},
		},
		Status: asynccfg.StatusConfig{
			Tool:         "forecasting:Total",
			ReuseRunArgs: true,
			Selector: asynccfg.Selector{
				StatusPath: "jobStatus",
				DataPath:   "result",
			},
		},
	}
	conv := &stubConv{}
	reg := &asyncRegistry{
		scriptedRegistry: scriptedRegistry{script: []scriptedResult{
			{result: `{"jobStatus":"COMPLETE","result":[{"inventory":1}]}`},
		}},
		cfg: cfg,
	}
	manager := asynccfg.NewManager()
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = memory.WithToolMessageID(ctx, "tool-msg-1")
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncConversation(ctx, conv)

	_, _, err := ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-1",
		Name: "forecasting:Total",
		Args: map[string]interface{}{"DealsPmpIncl": []int{141509}},
	}, conv)
	require.NoError(t, err)

	var lastStatus string
	for _, patched := range conv.patchedToolCalls {
		if patched == nil || patched.Status == "" {
			continue
		}
		lastStatus = patched.Status
	}
	require.Equal(t, "completed", lastStatus, "expected terminal async-start tool call to complete immediately")
}

var _ apiconv.Client = (*stubConv)(nil)
