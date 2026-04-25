package conversation

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	convcli "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	convwrite "github.com/viant/agently-core/pkg/agently/conversation/write"
	turnwrite "github.com/viant/agently-core/pkg/agently/turn/write"
	asynccfg "github.com/viant/agently-core/protocol/async"
	"github.com/viant/agently-core/protocol/tool"
	memory "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/service/shared/toolexec"
)

type asyncStatusRegistry struct {
	cfg    *asynccfg.Config
	script []string
	calls  int
}

func (a *asyncStatusRegistry) Definitions() []llm.ToolDefinition                { return nil }
func (a *asyncStatusRegistry) MatchDefinition(string) []*llm.ToolDefinition     { return nil }
func (a *asyncStatusRegistry) GetDefinition(string) (*llm.ToolDefinition, bool) { return nil, false }
func (a *asyncStatusRegistry) MustHaveTools([]string) ([]llm.Tool, error)       { return nil, nil }
func (a *asyncStatusRegistry) SetDebugLogger(io.Writer)                         {}
func (a *asyncStatusRegistry) Initialize(context.Context)                       {}
func (a *asyncStatusRegistry) AsyncConfig(name string) (*asynccfg.Config, bool) {
	if a.cfg == nil {
		return nil, false
	}
	if name == a.cfg.Run.Tool || name == a.cfg.Status.Tool {
		return a.cfg, true
	}
	return nil, false
}
func (a *asyncStatusRegistry) Execute(_ context.Context, _ string, _ map[string]interface{}) (string, error) {
	if len(a.script) == 0 {
		return "", nil
	}
	idx := a.calls
	if idx >= len(a.script) {
		idx = len(a.script) - 1
	}
	a.calls++
	return a.script[idx], nil
}

var _ tool.Registry = (*asyncStatusRegistry)(nil)
var _ tool.AsyncResolver = (*asyncStatusRegistry)(nil)

func TestExecuteToolStep_ActivatedStatusPollerPublishesNarrationEvents(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", tmp)
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")

	dao, err := NewDatly(ctx)
	require.NoError(t, err)
	svc, err := New(ctx, dao)
	require.NoError(t, err)

	bus := streaming.NewMemoryBus(32)
	svc.SetStreamPublisher(bus)
	sub, err := bus.Subscribe(ctx, nil)
	require.NoError(t, err)
	defer sub.Close()

	conv := &convcli.MutableConversation{}
	conv.Has = &convwrite.ConversationHas{}
	conv.SetId("conv-async-status-preamble")
	conv.SetStatus("running")
	conv.SetVisibility("private")
	require.NoError(t, svc.PatchConversations(ctx, conv))

	turn := convcli.NewTurn()
	turn.Has = &turnwrite.TurnHas{}
	turn.SetId("turn-async-status-preamble")
	turn.SetConversationID("conv-async-status-preamble")
	turn.SetStatus("running")
	turn.SetCreatedAt(time.Now())
	require.NoError(t, svc.PatchTurn(ctx, turn))

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
	reg := &asyncStatusRegistry{
		cfg: cfg,
		script: []string{
			`{"status":"running","message":"same status","messageKind":"preamble"}`,
			`{"status":"running","message":"changed status","messageKind":"preamble"}`,
		},
	}

	manager := asynccfg.NewManager()
	runCtx := memory.WithTurnMeta(ctx, memory.TurnMeta{
		ConversationID: "conv-async-status-preamble",
		TurnID:         "turn-async-status-preamble",
	})
	runCtx = memory.WithUserAsk(runCtx, "Analyze order 2639076 performance")
	runCtx = toolexec.WithAsyncManager(runCtx, manager)
	runCtx = toolexec.WithAsyncConversation(runCtx, svc)

	manager.Register(runCtx, asynccfg.RegisterInput{
		ID:             "child-1",
		ParentConvID:   "conv-async-status-preamble",
		ParentTurnID:   "turn-async-status-preamble",
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
	_ = manager.ConsumeChanged("conv-async-status-preamble", "turn-async-status-preamble")

	_, _, err = toolexec.ExecuteToolStep(runCtx, reg, toolexec.StepInfo{
		ID:   "call-status",
		Name: "llm/agents:status",
		Args: map[string]interface{}{"conversationId": "child-1"},
	}, svc)
	require.NoError(t, err)

	var preambleEvents []*streaming.Event
	deadline := time.After(3 * time.Second)
	for len(preambleEvents) < 2 {
		select {
		case ev := <-sub.C():
			if ev != nil && ev.Type == streaming.EventTypeNarration {
				preambleEvents = append(preambleEvents, ev)
			}
		case <-deadline:
			t.Fatalf("expected 2 narration events, got %d", len(preambleEvents))
		}
	}

	// Narration payload now lives on the dedicated `Narration` field
	// (not `Content`) — TS reducer reads `event.narration`.
	require.Equal(t, preambleEvents[0].MessageID, preambleEvents[1].MessageID)
	require.Contains(t, preambleEvents[0].Narration, "same status")
	require.Contains(t, preambleEvents[1].Narration, "changed status")
	require.Empty(t, preambleEvents[0].Content, "narration text must not leak into Content")
	require.Empty(t, preambleEvents[1].Content)
}
