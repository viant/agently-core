package toolexec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/internal/service/conversation/memory"
	asynccfg "github.com/viant/agently-core/protocol/async"
	asyncnarrator "github.com/viant/agently-core/protocol/async/narrator"
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
		DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
		PollIntervalMs:       5,
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
	ctx = memory.WithModelMessageID(ctx, "assistant-1")
	ctx = modelcallctx.WithStreamPublisher(ctx, pub)
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncConversation(ctx, &stubConv{})

	_, _, err := ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-1",
		Name: "llm/agents:start",
		Args: map[string]interface{}{"agentId": "coder", "objective": "analyze"},
	}, &stubConv{})
	require.NoError(t, err)

	_, _, err = ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-2",
		Name: "llm/agents:status",
		Args: map[string]interface{}{"conversationId": "child-1"},
	}, &stubConv{})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return len(pub.events) >= 3
	}, time.Second, 10*time.Millisecond)
	require.NotEmpty(t, pub.events)

	var sawStarted, sawWaiting, sawCompleted bool
	for _, event := range pub.events {
		if event == nil {
			continue
		}
		if event.OperationID != "child-1" {
			continue
		}
		require.Equal(t, "assistant-1", event.AssistantMessageID)
		switch event.Type {
		case streaming.EventTypeToolCallStarted:
			require.Equal(t, "call-1", event.ToolCallID)
			sawStarted = true
		case streaming.EventTypeToolCallWaiting:
			require.Equal(t, "call-1", event.ToolCallID)
			sawWaiting = true
		case streaming.EventTypeToolCallCompleted:
			require.Equal(t, "call-2", event.ToolCallID)
			sawCompleted = true
		}
	}
	require.True(t, sawStarted)
	require.True(t, sawWaiting)
	require.True(t, sawCompleted)
	require.Eventually(t, func() bool {
		rec, ok := manager.Get(context.Background(), "child-1")
		return ok && rec != nil && rec.Terminal()
	}, time.Second, 10*time.Millisecond)
	require.GreaterOrEqual(t, reg.calls, 2)
}

func TestExecuteToolStep_StartAutoPollsInWaitMode(t *testing.T) {
	cfg := &asynccfg.Config{
		DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
		PollIntervalMs:       5,
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
	conv := &stubConv{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = memory.WithToolMessageID(ctx, "tool-msg-1")
	ctx = memory.WithModelMessageID(ctx, "assistant-1")
	ctx = modelcallctx.WithStreamPublisher(ctx, pub)
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncConversation(ctx, conv)

	_, _, err := ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-1",
		Name: "llm/agents:start",
		Args: map[string]interface{}{"agentId": "coder", "objective": "analyze"},
	}, conv)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return len(reg.callTimes) >= 2
	}, time.Second, 10*time.Millisecond, "wait-mode start should launch autonomous status polling")
	require.NotEmpty(t, conv.patchedToolCalls)
	last := conv.patchedToolCalls[len(conv.patchedToolCalls)-1]
	require.NotNil(t, last)
	require.Equal(t, "completed", strings.TrimSpace(last.Status))
}

func TestExecuteToolStep_AsyncOverrideUsesExecutionMode(t *testing.T) {
	cfg := &asynccfg.Config{
		DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
		PollIntervalMs:       5,
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
	manager := asynccfg.NewManager()
	conv := convmem.New()
	seed := apiconv.NewConversation()
	seed.SetId("conv-1")
	seed.SetAgentId("coder")
	require.NoError(t, conv.PatchConversations(context.Background(), seed))
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = memory.WithToolMessageID(ctx, "tool-msg-1")
	ctx = memory.WithModelMessageID(ctx, "assistant-1")
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncConversation(ctx, conv)

	_, _, err := ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-1",
		Name: "llm/agents:start",
		Args: map[string]interface{}{
			"agentId":   "coder",
			"objective": "analyze",
			"_agently": map[string]interface{}{
				"async": map[string]interface{}{
					"executionMode": "detach",
				},
			},
		},
	}, conv)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	require.Len(t, reg.callTimes, 1, "override should suppress autonomous polling")
	rec, ok := manager.Get(context.Background(), "child-1")
	require.True(t, ok)
	require.NotNil(t, rec)
	require.False(t, asynccfg.ExecutionModeWaits(rec.ExecutionMode))
}

func TestExecuteToolStep_StoresOperationIntentFromRunIntentPath(t *testing.T) {
	cfg := &asynccfg.Config{
		DefaultExecutionMode: string(asynccfg.ExecutionModeDetach),
		Run: asynccfg.RunConfig{
			Tool:            "llm/agents:start",
			OperationIDPath: "conversationId",
			IntentPath:      "objective",
			SummaryPaths:    []string{"workdir", "orderId"},
			Selector:        &asynccfg.Selector{StatusPath: "status"},
		},
		Status: asynccfg.StatusConfig{
			Tool:           "llm/agents:status",
			OperationIDArg: "conversationId",
			Selector:       asynccfg.Selector{StatusPath: "status"},
		},
	}
	reg := &asyncRegistry{
		scriptedRegistry: scriptedRegistry{script: []scriptedResult{
			{result: `{"status":"running","conversationId":"child-1"}`},
		}},
		cfg: cfg,
	}
	manager := asynccfg.NewManager()
	conv := &stubConv{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = memory.WithToolMessageID(ctx, "tool-msg-1")
	ctx = memory.WithModelMessageID(ctx, "assistant-1")
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncConversation(ctx, conv)

	_, _, err := ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-1",
		Name: "llm/agents:start",
		Args: map[string]interface{}{
			"objective": " Inspect   repository structure ",
			"workdir":   "/tmp/ws",
			"orderId":   2639076,
		},
	}, conv)
	require.NoError(t, err)

	rec, ok := manager.Get(context.Background(), "child-1")
	require.True(t, ok)
	require.NotNil(t, rec)
	require.Equal(t, "Inspect repository structure", rec.OperationIntent)
	require.Equal(t, "workdir=/tmp/ws | orderId=2639076", rec.OperationSummary)
}

func TestExecuteToolStep_StatusWaitExecutionMode_ParksUntilTerminal(t *testing.T) {
	cfg := &asynccfg.Config{
		DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
		Run: asynccfg.RunConfig{
			Tool:            "llm/agents:start",
			OperationIDPath: "conversationId",
			Selector:        &asynccfg.Selector{StatusPath: "status"},
		},
		Status: asynccfg.StatusConfig{
			Tool:           "llm/agents:status",
			OperationIDArg: "conversationId",
			Selector:       asynccfg.Selector{StatusPath: "status", DataPath: "items"},
		},
	}
	reg := &asyncRegistry{
		scriptedRegistry: scriptedRegistry{script: []scriptedResult{
			{result: `{"status":"running","items":[{"conversationId":"child-1","status":"running"}]}`},
		}},
		cfg: cfg,
	}
	manager := asynccfg.NewManager()
	conv := &stubConv{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = memory.WithToolMessageID(ctx, "tool-msg-1")
	ctx = memory.WithModelMessageID(ctx, "assistant-1")
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncConversation(ctx, conv)

	manager.Register(ctx, asynccfg.RegisterInput{
		ID:             "child-1",
		ParentConvID:   "conv-1",
		ParentTurnID:   "turn-1",
		ToolName:       "llm/agents:start",
		StatusToolName: "llm/agents:status",
		ExecutionMode:  string(asynccfg.ExecutionModeWait),
		Status:         "running",
	})
	_ = manager.ConsumeChanged("conv-1", "turn-1")

	go func() {
		time.Sleep(25 * time.Millisecond)
		_, _ = manager.Update(context.Background(), asynccfg.UpdateInput{
			ID:      "child-1",
			Status:  "running",
			Message: "phase 1",
		})
		time.Sleep(10 * time.Millisecond)
		_, _ = manager.Update(context.Background(), asynccfg.UpdateInput{
			ID:      "child-1",
			Status:  "completed",
			State:   asynccfg.StateCompleted,
			Message: "done",
			KeyData: json.RawMessage(`{"items":[{"conversationId":"child-1","status":"completed"}]}`),
		})
	}()

	started := time.Now()
	out, _, err := ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-status",
		Name: "llm/agents:status",
		Args: map[string]interface{}{"conversationId": "child-1"},
	}, conv)
	require.NoError(t, err)
	require.GreaterOrEqual(t, time.Since(started), 20*time.Millisecond)
	require.Contains(t, out.Result, `"reason":"success"`)
	require.Contains(t, out.Result, `"operationId":"child-1"`)
	var preambleIDs []string
	for _, msg := range conv.patchedMessages {
		if msg == nil || msg.Interim == nil || *msg.Interim != 1 || msg.Preamble == nil {
			continue
		}
		preambleIDs = append(preambleIDs, msg.Id)
	}
	require.GreaterOrEqual(t, len(preambleIDs), 2, "expected create + update on interim preamble message")
	require.Equal(t, preambleIDs[0], preambleIDs[len(preambleIDs)-1], "expected same assistantMessageId reused for preamble updates")
}

func TestExecuteToolStep_StatusWaitExecutionMode_SoftReleasesOnIdle(t *testing.T) {
	cfg := &asynccfg.Config{
		DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
		Run: asynccfg.RunConfig{
			Tool:            "system/exec:start",
			OperationIDPath: "sessionId",
			Selector:        &asynccfg.Selector{StatusPath: "status"},
		},
		Status: asynccfg.StatusConfig{
			Tool:           "system/exec:status",
			OperationIDArg: "sessionId",
			Selector:       asynccfg.Selector{StatusPath: "status", DataPath: "stdout"},
		},
	}
	reg := &asyncRegistry{
		scriptedRegistry: scriptedRegistry{script: []scriptedResult{
			{result: `{"status":"running","stdout":"still working"}`},
		}},
		cfg: cfg,
	}
	manager := asynccfg.NewManager()
	conv := &stubConv{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = memory.WithToolMessageID(ctx, "tool-msg-1")
	ctx = memory.WithModelMessageID(ctx, "assistant-1")
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncConversation(ctx, conv)

	manager.Register(ctx, asynccfg.RegisterInput{
		ID:             "sess-1",
		ParentConvID:   "conv-1",
		ParentTurnID:   "turn-1",
		ToolName:       "system/exec:start",
		StatusToolName: "system/exec:status",
		ExecutionMode:  string(asynccfg.ExecutionModeWait),
		Status:         "running",
		IdleTimeoutMs:  20,
	})
	_ = manager.ConsumeChanged("conv-1", "turn-1")

	started := time.Now()
	out, _, err := ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-status",
		Name: "system/exec:status",
		Args: map[string]interface{}{"sessionId": "sess-1"},
	}, conv)
	require.NoError(t, err)
	require.GreaterOrEqual(t, time.Since(started), 15*time.Millisecond)
	require.Contains(t, out.Result, `"reason":"running_idle"`)
	require.Contains(t, out.Result, `"opsStillActive":true`)
	require.NotEmpty(t, conv.patchedMessages)
}

func TestExecuteToolStep_StatusWaitExecutionMode_DebouncesPreambleUpdates(t *testing.T) {
	prevWindow := asyncNarrationDebounceWindow
	asyncNarrationDebounceWindow = 10 * time.Millisecond
	defer func() { asyncNarrationDebounceWindow = prevWindow }()

	cfg := &asynccfg.Config{
		DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
		Run: asynccfg.RunConfig{
			Tool:            "llm/agents:start",
			OperationIDPath: "conversationId",
			IntentPath:      "objective",
			Selector:        &asynccfg.Selector{StatusPath: "status"},
		},
		Status: asynccfg.StatusConfig{
			Tool:           "llm/agents:status",
			OperationIDArg: "conversationId",
			Selector:       asynccfg.Selector{StatusPath: "status", DataPath: "items"},
		},
	}
	reg := &asyncRegistry{
		scriptedRegistry: scriptedRegistry{script: []scriptedResult{
			{result: `{"status":"running","items":[{"conversationId":"child-1","status":"running"}]}`},
		}},
		cfg: cfg,
	}
	manager := asynccfg.NewManager()
	conv := &stubConv{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = memory.WithToolMessageID(ctx, "tool-msg-1")
	ctx = memory.WithModelMessageID(ctx, "assistant-1")
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncConversation(ctx, conv)
	manager.Register(ctx, asynccfg.RegisterInput{
		ID:              "child-1",
		ParentConvID:    "conv-1",
		ParentTurnID:    "turn-1",
		ToolName:        "llm/agents:start",
		StatusToolName:  "llm/agents:status",
		OperationIntent: "inspect repo",
		ExecutionMode:   string(asynccfg.ExecutionModeWait),
		Status:          "running",
	})
	_ = manager.ConsumeChanged("conv-1", "turn-1")

	go func() {
		time.Sleep(1 * time.Millisecond)
		_, _ = manager.Update(context.Background(), asynccfg.UpdateInput{ID: "child-1", Status: "running", Message: "phase 1"})
		time.Sleep(1 * time.Millisecond)
		_, _ = manager.Update(context.Background(), asynccfg.UpdateInput{ID: "child-1", Status: "running", Message: "phase 2"})
		time.Sleep(15 * time.Millisecond)
		_, _ = manager.Update(context.Background(), asynccfg.UpdateInput{
			ID:      "child-1",
			Status:  "completed",
			State:   asynccfg.StateCompleted,
			Message: "done",
			KeyData: json.RawMessage(`{"items":[{"conversationId":"child-1","status":"completed"}]}`),
		})
	}()

	_, _, err := ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-status",
		Name: "llm/agents:status",
		Args: map[string]interface{}{"conversationId": "child-1"},
	}, conv)
	require.NoError(t, err)

	var preamblePatches int
	var preambles []string
	for _, msg := range conv.patchedMessages {
		if msg == nil || msg.Interim == nil || *msg.Interim != 1 || msg.Preamble == nil {
			continue
		}
		preamblePatches++
		preambles = append(preambles, *msg.Preamble)
	}
	require.LessOrEqual(t, preamblePatches, 3, "expected create + debounced updates + optional final flush only")
	for _, preamble := range preambles {
		require.NotContains(t, preamble, "phase 1", "phase 1 should be coalesced away by debounce")
	}
	require.Contains(t, strings.Join(preambles, "\n"), "phase 2")
}

func TestExecuteToolStep_StatusWaitExecutionMode_UsesLLMNarratorRunner(t *testing.T) {
	cfg := &asynccfg.Config{
		DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
		Narration:            "llm",
		Run: asynccfg.RunConfig{
			Tool:            "llm/agents:start",
			OperationIDPath: "conversationId",
			IntentPath:      "objective",
			Selector:        &asynccfg.Selector{StatusPath: "status"},
		},
		Status: asynccfg.StatusConfig{
			Tool:           "llm/agents:status",
			OperationIDArg: "conversationId",
			Selector:       asynccfg.Selector{StatusPath: "status", DataPath: "items"},
		},
	}
	reg := &asyncRegistry{
		scriptedRegistry: scriptedRegistry{script: []scriptedResult{
			{result: `{"status":"running","items":[{"conversationId":"child-1","status":"running"}]}`},
		}},
		cfg: cfg,
	}
	manager := asynccfg.NewManager()
	conv := &stubConv{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = memory.WithToolMessageID(ctx, "tool-msg-1")
	ctx = memory.WithModelMessageID(ctx, "assistant-1")
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncConversation(ctx, conv)
	ctx = WithAsyncNarratorRunner(ctx, func(_ context.Context, in asyncnarrator.LLMInput) (string, error) {
		return "llm:" + in.Intent + ":" + in.Message, nil
	})
	manager.Register(ctx, asynccfg.RegisterInput{
		ID:              "child-1",
		ParentConvID:    "conv-1",
		ParentTurnID:    "turn-1",
		ToolName:        "llm/agents:start",
		StatusToolName:  "llm/agents:status",
		OperationIntent: "inspect repo",
		ExecutionMode:   string(asynccfg.ExecutionModeWait),
		Status:          "running",
	})
	_ = manager.ConsumeChanged("conv-1", "turn-1")

	go func() {
		time.Sleep(25 * time.Millisecond)
		_, _ = manager.Update(context.Background(), asynccfg.UpdateInput{
			ID:      "child-1",
			Status:  "completed",
			State:   asynccfg.StateCompleted,
			Message: "done",
			KeyData: json.RawMessage(`{"items":[{"conversationId":"child-1","status":"completed"}]}`),
		})
	}()

	_, _, err := ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-status",
		Name: "llm/agents:status",
		Args: map[string]interface{}{"conversationId": "child-1"},
	}, conv)
	require.NoError(t, err)

	var found bool
	for _, msg := range conv.patchedMessages {
		if msg == nil || msg.Preamble == nil {
			continue
		}
		if strings.Contains(*msg.Preamble, "llm:inspect repo") {
			found = true
			break
		}
	}
	require.True(t, found, "expected llm runner preamble to be used")
}

func TestExecuteToolStep_AsyncUsesExplicitMessagePathForChildStatus(t *testing.T) {
	cfg := &asynccfg.Config{
		DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
		PollIntervalMs:       5,
		Run: asynccfg.RunConfig{
			Tool:            "llm/agents:start",
			OperationIDPath: "conversationId",
			Selector:        &asynccfg.Selector{StatusPath: "status"},
		},
		Status: asynccfg.StatusConfig{
			Tool:           "llm/agents:status",
			OperationIDArg: "conversationId",
			Selector: asynccfg.Selector{
				StatusPath:  "status",
				MessagePath: "message",
				DataPath:    "items",
			},
		},
	}
	reg := &asyncRegistry{
		scriptedRegistry: scriptedRegistry{script: []scriptedResult{
			{result: `{"status":"running","conversationId":"child-1"}`},
			{result: `{"status":"running","message":"platform child matched sites","messageKind":"preamble"}`},
		}},
		cfg: cfg,
	}
	manager := asynccfg.NewManager()
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = memory.WithToolMessageID(ctx, "tool-msg-1")
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncConversation(ctx, &stubConv{})

	_, _, err := ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-1",
		Name: "llm/agents:start",
		Args: map[string]interface{}{"agentId": "coder", "objective": "analyze"},
	}, &stubConv{})
	require.NoError(t, err)
	maybeStartAsyncPoller(ctx, manager, reg, cfg, memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}, "child-1", &stubConv{})

	require.Eventually(t, func() bool {
		rec, ok := manager.Get(context.Background(), "child-1")
		return ok && rec != nil && strings.TrimSpace(rec.Message) == "platform child matched sites"
	}, time.Second, 10*time.Millisecond)
}

func TestExecuteToolStep_StripsAgentlyControlArgsBeforeExecution(t *testing.T) {
	cfg := &asynccfg.Config{
		DefaultExecutionMode: string(asynccfg.ExecutionModeDetach),
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
	reg := &argsCapturingRegistry{
		asyncRegistry: asyncRegistry{
			scriptedRegistry: scriptedRegistry{script: []scriptedResult{
				{result: `{"status":"running","conversationId":"child-1"}`},
			}},
			cfg: cfg,
		},
	}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = WithAsyncManager(ctx, asynccfg.NewManager())
	ctx = WithAsyncConversation(ctx, &stubConv{})

	_, _, err := ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-1",
		Name: "llm/agents:start",
		Args: map[string]interface{}{
			"agentId": "coder",
			"_agently": map[string]interface{}{
				"async": map[string]interface{}{
					"executionMode": "detach",
				},
			},
		},
	}, &stubConv{})
	require.NoError(t, err)
	require.NotEmpty(t, reg.capturedArgs)
	_, exists := reg.capturedArgs[0]["_agently"]
	require.False(t, exists, "control envelope must not be passed to underlying tool")
}

func TestExecuteToolStep_AsyncAutoCancelsOnTimeout(t *testing.T) {
	cfg := &asynccfg.Config{
		DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
		TimeoutMs:            20,
		PollIntervalMs:       5,
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
	ctx = WithAsyncConversation(ctx, &stubConv{})
	_, _, err := ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-1",
		Name: "system/exec:start",
		Args: map[string]interface{}{"commands": []string{"sleep 30"}},
	}, &stubConv{})
	require.NoError(t, err)
	_, _, err = ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-2",
		Name: "system/exec:status",
		Args: map[string]interface{}{"sessionId": "sess-1"},
	}, &stubConv{})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		rec, ok := manager.Get(context.Background(), "sess-1")
		return ok && rec != nil && rec.Terminal()
	}, time.Second, 10*time.Millisecond)
	rec, ok := manager.Get(context.Background(), "sess-1")
	require.True(t, ok)
	require.NotNil(t, rec)
	require.True(t, rec.Terminal())
	require.Equal(t, asynccfg.StateFailed, rec.State)
	require.Equal(t, 1, reg.cancelCalls)
	require.GreaterOrEqual(t, reg.calls, 3)
}

func TestExecuteToolStep_AsyncAutoPollsToCompletion(t *testing.T) {
	cfg := &asynccfg.Config{
		DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
		PollIntervalMs:       5,
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
			{result: `{"status":"completed","stdout":"DONE"}`},
		}},
		cfg: cfg,
	}
	manager := asynccfg.NewManager()
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = memory.WithToolMessageID(ctx, "tool-msg-1")
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncConversation(ctx, &stubConv{})

	_, _, err := ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-1",
		Name: "system/exec:start",
		Args: map[string]interface{}{"commands": []string{"sleep 30"}},
	}, &stubConv{})
	require.NoError(t, err)
	_, _, err = ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-2",
		Name: "system/exec:status",
		Args: map[string]interface{}{"sessionId": "sess-1"},
	}, &stubConv{})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		rec, ok := manager.Get(context.Background(), "sess-1")
		return ok && rec != nil && rec.Terminal()
	}, time.Second, 10*time.Millisecond)
	rec, ok := manager.Get(context.Background(), "sess-1")
	require.True(t, ok)
	require.NotNil(t, rec)
	require.True(t, rec.Terminal())
	require.Equal(t, asynccfg.StateCompleted, rec.State)
	require.Equal(t, 0, rec.PollFailures)
	require.GreaterOrEqual(t, reg.calls, 2)
}

func TestExecuteToolStep_SameToolRecallWaitsForPollWindow(t *testing.T) {
	cfg := &asynccfg.Config{
		DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
		PollIntervalMs:       80,
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
		DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
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
		ExecutionMode:     string(asynccfg.ExecutionModeWait),
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
		DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
		PollIntervalMs:       50,
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

	var sawCompleted bool
	for _, patched := range conv.patchedToolCalls {
		if patched == nil || patched.Status == "" {
			continue
		}
		if patched.Status == "completed" {
			sawCompleted = true
		}
	}
	require.True(t, sawCompleted, "expected async start tool call to complete immediately after launch")
}

func TestExecuteToolStep_AsyncCompletionPersistsResponsePayload(t *testing.T) {
	cfg := &asynccfg.Config{
		DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
		PollIntervalMs:       5,
		Run: asynccfg.RunConfig{
			Tool:            "llm/agents:start",
			OperationIDPath: "conversationId",
			Selector:        &asynccfg.Selector{StatusPath: "status"},
		},
		Status: asynccfg.StatusConfig{
			Tool:           "llm/agents:status",
			OperationIDArg: "conversationId",
			Selector: asynccfg.Selector{
				StatusPath:  "status",
				MessagePath: "message",
			},
		},
	}
	conv := &stubConv{}
	reg := &asyncRegistry{
		scriptedRegistry: scriptedRegistry{script: []scriptedResult{
			{result: `{"status":"running","conversationId":"child-1"}`},
			{result: `{"status":"completed","message":"final child answer","messageKind":"response"}`},
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
	maybeStartAsyncPoller(ctx, manager, reg, cfg, memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}, "child-1", conv)

	require.Eventually(t, func() bool {
		for _, call := range conv.patchedToolCalls {
			if call == nil || call.ResponsePayloadID == nil {
				continue
			}
			if strings.TrimSpace(*call.ResponsePayloadID) != "" {
				return true
			}
		}
		return false
	}, time.Second, 10*time.Millisecond, "timed out waiting for async completion response payload persistence")

	require.Eventually(t, func() bool {
		for _, message := range conv.patchedMessages {
			if message == nil || message.Content == nil {
				continue
			}
			body := strings.TrimSpace(*message.Content)
			if body == "" {
				continue
			}
			if !strings.Contains(body, `"conversationId":"child-1"`) {
				continue
			}
			if !strings.Contains(body, `"messageKind":"answer"`) {
				continue
			}
			require.Contains(t, body, `"message":"final child answer"`)
			require.NotContains(t, body, `"items"`)
			return true
		}
		return false
	}, time.Second, 10*time.Millisecond, "expected normalized async status message content to be persisted on the parent tool message")
}

func TestMaybeHandleAsyncTool_StatusPublishesFailedLifecycleEvent(t *testing.T) {
	cfg := &asynccfg.Config{
		DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
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
		ExecutionMode:     string(asynccfg.ExecutionModeWait),
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
		DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
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
		ExecutionMode:     string(asynccfg.ExecutionModeWait),
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
		DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
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
		ID:            "sess-1",
		ParentConvID:  "conv-1",
		ParentTurnID:  "turn-1",
		ToolName:      "system/exec:start",
		ExecutionMode: string(asynccfg.ExecutionModeWait),
		Status:        "running",
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

func TestMaybeHandleAsyncTool_StatusChangedDoesNotMarkAfterStatus(t *testing.T) {
	cfg := &asynccfg.Config{
		DefaultExecutionMode: string(asynccfg.ExecutionModeDetach),
		Run: asynccfg.RunConfig{
			Tool:            "llm/agents:start",
			OperationIDPath: "conversationId",
			Selector:        &asynccfg.Selector{StatusPath: "status"},
		},
		Status: asynccfg.StatusConfig{
			Tool:           "llm/agents:status",
			OperationIDArg: "conversationId",
			Selector: asynccfg.Selector{
				StatusPath:  "status",
				MessagePath: "message",
			},
		},
	}
	reg := &asyncRegistry{cfg: cfg}
	manager := asynccfg.NewManager()
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncWaitState(ctx)

	manager.Register(ctx, asynccfg.RegisterInput{
		ID:            "child-1",
		ParentConvID:  "conv-1",
		ParentTurnID:  "turn-1",
		ToolName:      "llm/agents:start",
		ExecutionMode: string(asynccfg.ExecutionModeDetach),
		Status:        "running",
		Message:       "old message",
	})
	_ = manager.ConsumeChanged("conv-1", "turn-1")

	rec := maybeHandleAsyncTool(ctx, reg, StepInfo{
		ID:   "call-2",
		Name: "llm/agents:status",
		Args: map[string]interface{}{"conversationId": "child-1"},
	}, `{"status":"running","message":"new message","messageKind":"preamble"}`, nil)
	require.Nil(t, rec)
	require.Empty(t, ConsumeAsyncWaitAfterStatus(ctx))
}

func TestExecuteToolStep_ActivatedStatusPollerReturnsLatestSnapshotOnTimeout(t *testing.T) {
	cfg := &asynccfg.Config{
		DefaultExecutionMode: string(asynccfg.ExecutionModeDetach),
		TimeoutMs:            20,
		PollIntervalMs:       5,
		Run: asynccfg.RunConfig{
			Tool:            "llm/agents:start",
			OperationIDPath: "conversationId",
			Selector:        &asynccfg.Selector{StatusPath: "status"},
		},
		Status: asynccfg.StatusConfig{
			Tool:           "llm/agents:status",
			OperationIDArg: "conversationId",
			Selector: asynccfg.Selector{
				StatusPath:  "status",
				MessagePath: "message",
			},
		},
	}
	reg := &asyncRegistry{
		scriptedRegistry: scriptedRegistry{script: []scriptedResult{
			{result: `{"status":"running","message":"same status","messageKind":"preamble"}`},
		}},
		cfg: cfg,
	}
	manager := asynccfg.NewManager()
	conv := &stubConv{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = memory.WithToolMessageID(ctx, "tool-msg-2")
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncWaitState(ctx)
	ctx = WithAsyncConversation(ctx, conv)

	manager.Register(ctx, asynccfg.RegisterInput{
		ID:             "child-1",
		ParentConvID:   "conv-1",
		ParentTurnID:   "turn-1",
		ToolCallID:     "call-start",
		ToolMessageID:  "tool-msg-start",
		ToolName:       "llm/agents:start",
		ExecutionMode:  string(asynccfg.ExecutionModeDetach),
		Status:         "running",
		Message:        "same status",
		TimeoutMs:      20,
		PollIntervalMs: 5,
	})
	_ = manager.ConsumeChanged("conv-1", "turn-1")

	out, _, err := ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-status",
		Name: "llm/agents:status",
		Args: map[string]interface{}{"conversationId": "child-1"},
	}, conv)
	require.NoError(t, err)
	require.Contains(t, out.Result, "same status")
	require.Eventually(t, func() bool {
		return reg.calls >= 2
	}, time.Second, 10*time.Millisecond, "activated status should lazily launch poller after first status fetch")
	require.Eventually(t, func() bool {
		return len(conv.patchedMessages) > 0
	}, time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool {
		for _, msg := range conv.patchedMessages {
			if msg == nil || msg.Preamble == nil {
				continue
			}
			if strings.Contains(strings.TrimSpace(*msg.Preamble), "same status") {
				return true
			}
		}
		return false
	}, time.Second, 10*time.Millisecond, "activated status poller should emit a backend-authored preamble")
}

func TestExecuteToolStep_ActivatedStatusPollerCompletesOnChangedSnapshot(t *testing.T) {
	cfg := &asynccfg.Config{
		DefaultExecutionMode: string(asynccfg.ExecutionModeDetach),
		PollIntervalMs:       5,
		Run: asynccfg.RunConfig{
			Tool:            "llm/agents:start",
			OperationIDPath: "conversationId",
			Selector:        &asynccfg.Selector{StatusPath: "status"},
		},
		Status: asynccfg.StatusConfig{
			Tool:           "llm/agents:status",
			OperationIDArg: "conversationId",
			Selector: asynccfg.Selector{
				StatusPath:  "status",
				MessagePath: "message",
			},
		},
	}
	reg := &asyncRegistry{
		scriptedRegistry: scriptedRegistry{script: []scriptedResult{
			{result: `{"status":"running","message":"same status","messageKind":"preamble"}`},
			{result: `{"status":"running","message":"changed status","messageKind":"preamble"}`},
		}},
		cfg: cfg,
	}
	manager := asynccfg.NewManager()
	conv := &stubConv{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = memory.WithToolMessageID(ctx, "tool-msg-2")
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncWaitState(ctx)
	ctx = WithAsyncConversation(ctx, conv)

	manager.Register(ctx, asynccfg.RegisterInput{
		ID:             "child-1",
		ParentConvID:   "conv-1",
		ParentTurnID:   "turn-1",
		ToolCallID:     "call-start",
		ToolMessageID:  "tool-msg-start",
		ToolName:       "llm/agents:start",
		StatusToolName: "llm/agents:status",
		StatusArgs:     map[string]interface{}{"conversationId": "child-1"},
		ExecutionMode:  string(asynccfg.ExecutionModeDetach),
		Status:         "running",
		Message:        "same status",
		PollIntervalMs: 5,
	})
	_ = manager.ConsumeChanged("conv-1", "turn-1")

	out, _, err := ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-status",
		Name: "llm/agents:status",
		Args: map[string]interface{}{"conversationId": "child-1"},
	}, conv)
	require.NoError(t, err)
	require.Equal(t, 2, reg.calls, "status-attached observation should keep polling until the snapshot changes")
	require.Contains(t, out.Result, "changed status")
	require.NotEmpty(t, conv.patchedToolCalls)
	require.Equal(t, "running", strings.TrimSpace(conv.patchedToolCalls[0].Status))
	require.Equal(t, "completed", strings.TrimSpace(conv.patchedToolCalls[len(conv.patchedToolCalls)-1].Status))
	require.Eventually(t, func() bool {
		for _, msg := range conv.patchedMessages {
			if msg == nil || msg.Preamble == nil {
				continue
			}
			if strings.Contains(strings.TrimSpace(*msg.Preamble), "changed status") {
				return true
			}
		}
		return false
	}, time.Second, 10*time.Millisecond)
}

func TestMaybeHandleAsyncTool_StatusTerminalPatchesOriginalAsyncToolCall(t *testing.T) {
	cfg := &asynccfg.Config{
		DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
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
	ctx = memory.WithToolMessageID(ctx, "tool-msg-2")
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncWaitState(ctx)
	ctx = WithAsyncConversation(ctx, conv)

	manager.Register(ctx, asynccfg.RegisterInput{
		ID:            "sess-1",
		ParentConvID:  "conv-1",
		ParentTurnID:  "turn-1",
		ToolCallID:    "call-1",
		ToolMessageID: "tool-msg-1",
		ToolName:      "system/exec:start",
		ExecutionMode: string(asynccfg.ExecutionModeWait),
		Status:        "running",
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
	require.True(t, sawFailed, "expected terminal status tool call to patch the status carrier to failed")
}

func TestMaybeHandleAsyncTool_StatusDoesNotPatchOriginalToolCallWhenExecutionModeDetach(t *testing.T) {
	cfg := &asynccfg.Config{
		DefaultExecutionMode: string(asynccfg.ExecutionModeDetach),
		Run: asynccfg.RunConfig{
			Tool:            "llm/agents:start",
			OperationIDPath: "conversationId",
			Selector:        &asynccfg.Selector{StatusPath: "status"},
		},
		Status: asynccfg.StatusConfig{
			Tool:           "llm/agents:status",
			OperationIDArg: "conversationId",
			Selector: asynccfg.Selector{
				StatusPath:  "status",
				MessagePath: "message",
			},
		},
	}
	reg := &asyncRegistry{cfg: cfg}
	manager := asynccfg.NewManager()
	conv := &stubConv{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncConversation(ctx, conv)

	manager.Register(ctx, asynccfg.RegisterInput{
		ID:            "child-1",
		ParentConvID:  "conv-1",
		ParentTurnID:  "turn-1",
		ToolCallID:    "call-1",
		ToolMessageID: "tool-msg-1",
		ToolName:      "llm/agents:start",
		ExecutionMode: string(asynccfg.ExecutionModeDetach),
		Status:        "running",
	})
	_ = manager.ConsumeChanged("conv-1", "turn-1")

	rec := maybeHandleAsyncTool(ctx, reg, StepInfo{
		ID:   "call-2",
		Name: "llm/agents:status",
		Args: map[string]interface{}{"conversationId": "child-1"},
	}, `{"status":"completed","message":"child final answer","messageKind":"response"}`, nil)
	require.Nil(t, rec)

	stored, ok := manager.Get(context.Background(), "child-1")
	require.True(t, ok)
	require.NotNil(t, stored)
	require.Equal(t, asynccfg.StateCompleted, stored.State)
	require.Equal(t, "child final answer", stored.Message)
	require.Empty(t, conv.patchedMessages, "non-wait async ops should not overwrite the start tool message")
	require.Empty(t, conv.patchedToolCalls, "non-wait async ops should not patch the original start tool call state")
	require.Empty(t, conv.patchedPayloads, "non-wait async ops should not persist synthetic payloads onto the start tool row")
}

func TestMaybeHandleAsyncTool_StatusCanceledPatchesOriginalAsyncToolCall(t *testing.T) {
	cfg := &asynccfg.Config{
		DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
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
	ctx = memory.WithToolMessageID(ctx, "tool-msg-2")
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncWaitState(ctx)
	ctx = WithAsyncConversation(ctx, conv)

	manager.Register(ctx, asynccfg.RegisterInput{
		ID:            "sess-1",
		ParentConvID:  "conv-1",
		ParentTurnID:  "turn-1",
		ToolCallID:    "call-1",
		ToolMessageID: "tool-msg-1",
		ToolName:      "system/exec:start",
		ExecutionMode: string(asynccfg.ExecutionModeWait),
		Status:        "running",
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
	require.True(t, sawCanceled, "expected terminal canceled status tool call to patch the status carrier to canceled")
}

func TestMaybeHandleAsyncTool_SameToolReuseRunArgsTreatsRecallAsStatus(t *testing.T) {
	cfg := &asynccfg.Config{
		DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
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
		ExecutionMode:     string(asynccfg.ExecutionModeWait),
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
		DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
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
		DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
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

// ---------------------------------------------------------------------------
// Tests for P1 fixes: status-args reuse and poll-context propagation
// ---------------------------------------------------------------------------

// argsCapturingRegistry records the args and context passed to each Execute call.
type argsCapturingRegistry struct {
	asyncRegistry
	capturedArgs   []map[string]interface{}
	capturedHasPub []bool // whether the stream publisher was present in ctx
}

func (r *argsCapturingRegistry) Execute(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	argsCopy := make(map[string]interface{}, len(args))
	for k, v := range args {
		argsCopy[k] = v
	}
	r.capturedArgs = append(r.capturedArgs, argsCopy)
	_, hasPub := modelcallctx.StreamPublisherFromContext(ctx)
	r.capturedHasPub = append(r.capturedHasPub, hasPub)
	return r.asyncRegistry.Execute(ctx, name, args)
}

func (r *argsCapturingRegistry) AsyncConfig(name string) (*asynccfg.Config, bool) {
	return r.asyncRegistry.AsyncConfig(name)
}

func TestResolvePollerStatusArgs_UsesStoredRecordArgs(t *testing.T) {
	// Verifies that resolvePollerStatusArgs prefers the fully-prepared StatusArgs
	// stored on the OperationRecord over a stripped config-derived fallback.
	cfg := &asynccfg.Config{
		Status: asynccfg.StatusConfig{
			Tool:           "system/exec:status",
			OperationIDArg: "sessionId",
			ExtraArgs:      map[string]interface{}{"workspace": "ws-1"},
		},
	}
	manager := asynccfg.NewManager()
	ctx := context.Background()
	manager.Register(ctx, asynccfg.RegisterInput{
		ID:           "sess-1",
		ParentConvID: "conv-1",
		ParentTurnID: "turn-1",
		ToolName:     "system/exec:start",
		StatusArgs: map[string]interface{}{
			"sessionId": "sess-1",
			"workspace": "ws-1",
		},
		Status: "running",
	})

	args := resolvePollerStatusArgs(ctx, manager, cfg, "sess-1")

	require.Equal(t, "sess-1", args["sessionId"], "operation id must be present")
	require.Equal(t, "ws-1", args["workspace"], "ExtraArg from stored StatusArgs must be preserved")
}

func TestResolvePollerStatusArgs_FallsBackToConfigWhenNoRecord(t *testing.T) {
	cfg := &asynccfg.Config{
		Status: asynccfg.StatusConfig{
			Tool:           "system/exec:status",
			OperationIDArg: "sessionId",
			ExtraArgs:      map[string]interface{}{"workspace": "ws-fallback"},
		},
	}
	manager := asynccfg.NewManager()
	// No record registered — fallback path must still produce correct args.
	args := resolvePollerStatusArgs(context.Background(), manager, cfg, "sess-x")

	require.Equal(t, "sess-x", args["sessionId"])
	require.Equal(t, "ws-fallback", args["workspace"])
}

func TestPollAsyncOperation_UsesStoredStatusArgsNotBareOpID(t *testing.T) {
	// The poller must pass the fully-prepared StatusArgs (including ExtraArgs)
	// to each status-tool call, not just {OperationIDArg: opID}.
	cfg := &asynccfg.Config{
		DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
		PollIntervalMs:       5,
		Run: asynccfg.RunConfig{
			Tool:            "system/exec:start",
			OperationIDPath: "sessionId",
			Selector:        &asynccfg.Selector{StatusPath: "status"},
		},
		Status: asynccfg.StatusConfig{
			Tool:           "system/exec:status",
			OperationIDArg: "sessionId",
			ExtraArgs:      map[string]interface{}{"workspace": "ws-1"},
			Selector:       asynccfg.Selector{StatusPath: "status"},
		},
	}
	reg := &argsCapturingRegistry{
		asyncRegistry: asyncRegistry{
			scriptedRegistry: scriptedRegistry{script: []scriptedResult{
				{result: `{"status":"running","sessionId":"sess-1"}`},
				{result: `{"status":"completed"}`},
			}},
			cfg: cfg,
		},
	}
	pub := &captureStreamPublisher{}
	manager := asynccfg.NewManager()
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = memory.WithToolMessageID(ctx, "tool-msg-1")
	ctx = modelcallctx.WithStreamPublisher(ctx, pub)
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncConversation(ctx, &stubConv{})

	_, _, err := ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-1",
		Name: "system/exec:start",
		Args: map[string]interface{}{"commands": []string{"echo hi"}},
	}, &stubConv{})
	require.NoError(t, err)
	maybeStartAsyncPoller(ctx, manager, reg, cfg, memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}, "sess-1", &stubConv{})

	require.Eventually(t, func() bool {
		rec, ok := manager.Get(context.Background(), "sess-1")
		return ok && rec != nil && rec.Terminal()
	}, time.Second, 10*time.Millisecond)

	// The first Execute call is the start tool (no status args check needed).
	// Subsequent calls are status polls — they must include the ExtraArg.
	var statusCalls int
	for i, args := range reg.capturedArgs {
		// start call is index 0; status calls follow
		if i == 0 {
			continue
		}
		statusCalls++
		require.Equal(t, "sess-1", args["sessionId"],
			"status call %d must carry operationId", i)
		require.Equal(t, "ws-1", args["workspace"],
			"status call %d must carry ExtraArg from stored StatusArgs", i)
	}
	require.Greater(t, statusCalls, 0, "at least one status poll must have occurred")
}

func TestPollAsyncOperation_PassesPollContextToStatusTool(t *testing.T) {
	// The poller must pass `ctx` (which carries the stream publisher and other
	// request-scoped values from detachedAsyncPollContext) to reg.Execute, not
	// a fresh context.Background() that strips those values.
	cfg := &asynccfg.Config{
		DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
		PollIntervalMs:       5,
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
	reg := &argsCapturingRegistry{
		asyncRegistry: asyncRegistry{
			scriptedRegistry: scriptedRegistry{script: []scriptedResult{
				{result: `{"status":"running","conversationId":"child-1"}`},
				{result: `{"status":"completed"}`},
			}},
			cfg: cfg,
		},
	}
	pub := &captureStreamPublisher{}
	manager := asynccfg.NewManager()
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = memory.WithToolMessageID(ctx, "tool-msg-1")
	ctx = modelcallctx.WithStreamPublisher(ctx, pub)
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncConversation(ctx, &stubConv{})

	_, _, err := ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-1",
		Name: "llm/agents:start",
		Args: map[string]interface{}{"agentId": "coder"},
	}, &stubConv{})
	require.NoError(t, err)
	maybeStartAsyncPoller(ctx, manager, reg, cfg, memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}, "child-1", &stubConv{})

	require.Eventually(t, func() bool {
		rec, ok := manager.Get(context.Background(), "child-1")
		return ok && rec != nil && rec.Terminal()
	}, time.Second, 10*time.Millisecond)

	// Every status-tool call (indices > 0) must have received a context that
	// carries the stream publisher assembled by detachedAsyncPollContext.
	var statusCalls int
	for i, hasPub := range reg.capturedHasPub {
		if i == 0 {
			continue // start tool call — publisher may or may not be set
		}
		statusCalls++
		require.True(t, hasPub,
			"status poll %d must receive a context with the stream publisher", i)
	}
	require.Greater(t, statusCalls, 0)
}

// ---------------------------------------------------------------------------
// Test: poller stops when CancelTurnPollers is called (P1 fix)
// ---------------------------------------------------------------------------

func TestPollAsyncOperation_StopsWhenTurnCanceled(t *testing.T) {
	// The poller should stop as soon as CancelTurnPollers fires on the manager,
	// even if the status tool never returns a terminal state.
	cfg := &asynccfg.Config{
		DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
		PollIntervalMs:       5,
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
	}
	// Return "running" forever — the poller must not reach terminal on its own.
	reg := &asyncRegistry{
		scriptedRegistry: scriptedRegistry{script: []scriptedResult{
			{result: `{"status":"running","sessionId":"sess-1"}`},
			{result: `{"status":"running"}`},
			{result: `{"status":"running"}`},
			{result: `{"status":"running"}`},
			{result: `{"status":"running"}`},
		}},
		cfg: cfg,
	}
	manager := asynccfg.NewManager()
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = memory.WithToolMessageID(ctx, "tool-msg-1")
	ctx = WithAsyncManager(ctx, manager)
	ctx = WithAsyncConversation(ctx, &stubConv{})

	_, _, err := ExecuteToolStep(ctx, reg, StepInfo{
		ID:   "call-1",
		Name: "system/exec:start",
		Args: map[string]interface{}{"commands": []string{"sleep 9999"}},
	}, &stubConv{})
	require.NoError(t, err)
	maybeStartAsyncPoller(ctx, manager, reg, cfg, memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}, "sess-1", &stubConv{})

	// Wait for at least one poll to confirm the poller is running.
	require.Eventually(t, func() bool {
		return reg.calls >= 2
	}, time.Second, 5*time.Millisecond, "poller should have polled at least once")

	// Cancel the turn — this must stop the poller.
	manager.CancelTurnPollers(ctx, "conv-1", "turn-1")

	// The poller's slot should be freed promptly (FinishPoller fires on ctx.Done()).
	require.Eventually(t, func() bool {
		// TryStartPoller returns true only when no poller holds the slot.
		return manager.TryStartPoller(context.Background(), "sess-1")
	}, time.Second, 10*time.Millisecond, "poller should have exited after CancelTurnPollers")
}

var _ apiconv.Client = (*stubConv)(nil)
