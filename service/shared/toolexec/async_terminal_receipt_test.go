package toolexec

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	asynccfg "github.com/viant/agently-core/protocol/async"
	memory "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	modelcallctx "github.com/viant/agently-core/service/core/modelcall"
)

// Independent synthetic tasks: a wait result must close the same status carrier
// that the client observed, even when completion precedes the next poll tick.
func TestAwaitedTerminalSynchronizesStreamReceipt(t *testing.T) {
	for _, tc := range []struct {
		status string
		event  streaming.EventType
	}{
		{"succeeded", streaming.EventTypeToolCallCompleted},
		{"failed", streaming.EventTypeToolCallFailed},
		{"canceled", streaming.EventTypeToolCallCanceled},
		{"running", ""},
	} {
		t.Run(tc.status, func(t *testing.T) {
			pub := &captureStreamPublisher{}
			ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "parent", TurnID: "parent-turn"})
			ctx = memory.WithToolMessageID(ctx, "start-message")
			ctx = modelcallctx.WithStreamPublisher(ctx, pub)
			ctx = WithAsyncConversation(ctx, &stubConv{})
			manager := asynccfg.NewManager()
			manager.Register(ctx, asynccfg.RegisterInput{ID: "task", ParentConvID: "parent", ParentTurnID: "parent-turn", ToolCallID: "monitor-call", ToolMessageID: "monitor-message", ToolName: "tasks/status", ExecutionMode: string(asynccfg.ExecutionModeWait), Status: "running"})
			manager.Update(ctx, asynccfg.UpdateInput{ID: "task", Status: tc.status, Message: "Observed task state"})
			syncAwaitedAsyncTerminalPersistence(ctx, manager, &asynccfg.Config{}, StepInfo{ID: "start-call", Name: "tasks/start"}, "task")
			events := pub.eventsSnapshot()
			if tc.event == "" {
				require.Empty(t, events)
				return
			}
			require.Len(t, events, 1)
			require.Equal(t, tc.event, events[0].Type)
			require.Equal(t, "monitor-call", events[0].ToolCallID)
			require.Equal(t, "monitor-message", events[0].ToolMessageID)
			require.Equal(t, "tasks/status", events[0].ToolName)
			require.Equal(t, "parent-turn", events[0].TurnID)
		})
	}
}
