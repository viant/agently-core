package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	asynccfg "github.com/viant/agently-core/protocol/async"
	"github.com/viant/agently-core/protocol/binding"
	memory "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
)

type terminalCarrierReceiptPublisher struct{ events []*streaming.Event }

func (p *terminalCarrierReceiptPublisher) Publish(_ context.Context, e *streaming.Event) error {
	if e != nil {
		p.events = append(p.events, e)
	}
	return nil
}

func TestPreparedTerminalCarrierPublishesObservedReceipt(t *testing.T) {
	for _, tc := range []struct {
		name            string
		optIn, steering bool
	}{
		{"ordinary wait", false, false}, {"opted in wait", true, false}, {"steered wait", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "parent", TurnID: "turn"})
			manager := asynccfg.NewManager()
			manager.Register(ctx, asynccfg.RegisterInput{ID: "task", ParentConvID: "parent", ParentTurnID: "turn", ToolCallID: "monitor-call", ToolMessageID: "monitor-message", ToolName: "tasks/status", ExecutionMode: string(asynccfg.ExecutionModeWait), TerminalCarrierBeforeModel: tc.optIn, Status: "succeeded", Message: "Task complete"})
			pub := &terminalCarrierReceiptPublisher{}
			service := &Service{asyncManager: manager, streamPub: pub}
			b := terminalCarrierBinding("turn", &binding.Message{ID: "monitor-message", Kind: binding.MessageKindToolResult, ToolOpID: "monitor-call", Content: "Task running"})
			waiting, err := service.prepareTerminalCarriersBeforeModel(ctx, b, memory.TurnMeta{ConversationID: "parent", TurnID: "turn"}, tc.steering)
			require.NoError(t, err)
			require.False(t, waiting)
			require.Len(t, pub.events, 1, "a terminal binding must not leave the observed tool pending")
			require.Equal(t, streaming.EventTypeToolCallCompleted, pub.events[0].Type)
			require.Equal(t, "monitor-call", pub.events[0].ToolCallID)
			require.Equal(t, "monitor-message", pub.events[0].ToolMessageID)
		})
	}
}
