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
			Tool:            "llm/agents:run",
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
		Name: "llm/agents:run",
		Args: map[string]interface{}{"agentId": "coder", "objective": "analyze"},
	}, &stubConv{})
	require.NoError(t, err)

	deadline := time.Now().Add(time.Second)
	for len(pub.events) < 3 && time.Now().Before(deadline) {
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
	require.True(t, sawCompleted)
}

func TestExecuteToolStep_AsyncTimeoutInvokesCancel(t *testing.T) {
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

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		rec, ok := manager.Get(context.Background(), "sess-1")
		if ok && rec != nil && rec.State == asynccfg.StateFailed {
			require.Equal(t, "operation timed out", rec.Error)
			require.Equal(t, 1, reg.cancelCalls)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for async timeout transition")
}

func TestExecuteToolStep_AsyncPollRetriesTransientErrors(t *testing.T) {
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

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec, ok := manager.Get(context.Background(), "sess-1")
		if ok && rec != nil && rec.State == asynccfg.StateCompleted {
			require.Equal(t, 0, rec.PollFailures)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for async retry completion")
}

func TestExecuteToolStep_AsyncStartDoesNotCompleteImmediately(t *testing.T) {
	cfg := &asynccfg.Config{
		WaitForResponse: true,
		PollIntervalMs:  50,
		Run: asynccfg.RunConfig{
			Tool:            "llm/agents:run",
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
		Name: "llm/agents:run",
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
			Tool:            "llm/agents:run",
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
		Name: "llm/agents:run",
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

func TestExecuteToolStep_AsyncPublishesFailedLifecycleEvent(t *testing.T) {
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
				ErrorPath:  "stderr",
			},
		},
	}
	reg := &asyncRegistry{
		scriptedRegistry: scriptedRegistry{script: []scriptedResult{
			{result: `{"status":"running","sessionId":"sess-1"}`},
			{result: `{"status":"failed","stdout":"WAITING","stderr":"TERMINAL_FAILURE"}`},
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

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, event := range pub.events {
			if event == nil || event.OperationID != "sess-1" {
				continue
			}
			if event.Type == streaming.EventTypeToolCallFailed {
				require.Equal(t, "failed", event.Status)
				require.Equal(t, "TERMINAL_FAILURE", event.Error)
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for async failed lifecycle event")
}

func TestExecuteToolStep_AsyncPublishesCanceledLifecycleEvent(t *testing.T) {
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
			{result: `{"status":"canceled","stdout":"canceled"}`},
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

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, event := range pub.events {
			if event == nil || event.OperationID != "sess-1" {
				continue
			}
			if event.Type == streaming.EventTypeToolCallCanceled {
				require.Equal(t, "canceled", event.Status)
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for async canceled lifecycle event")
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

var _ apiconv.Client = (*stubConv)(nil)
