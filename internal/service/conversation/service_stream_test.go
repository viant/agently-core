package conversation

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	convcli "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/runtime/streaming"
)

func TestMessagePatchPayload(t *testing.T) {
	msg := convcli.NewMessage()
	msg.SetId("m1")
	msg.SetStatus("completed")
	msg.SetToolName("llm/agents-run")
	msg.SetInterim(0)
	msg.SetPreamble("delegating")
	msg.SetLinkedConversationID("child-123")
	msg.SetTurnID("turn-1")
	msg.SetContent("done")
	msg.SetRole("assistant")
	msg.SetType("text")
	msg.SetIteration(2)
	msg.SetCreatedAt(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))

	got := messagePatchPayload(msg)
	require.EqualValues(t, "completed", got["status"])
	require.EqualValues(t, "llm/agents/run", got["toolName"])
	require.EqualValues(t, 0, got["interim"])
	require.EqualValues(t, "delegating", got["preamble"])
	require.EqualValues(t, "child-123", got["linkedConversationId"])
	require.EqualValues(t, "turn-1", got["turnId"])
	require.EqualValues(t, "done", got["content"])
	require.EqualValues(t, "assistant", got["role"])
	require.EqualValues(t, "text", got["messageType"])
	require.EqualValues(t, 2, got["iteration"])
	require.EqualValues(t, "2026-01-02T03:04:05Z", got["createdAt"])
}

func TestMessagePatchPayload_Empty(t *testing.T) {
	msg := convcli.NewMessage()
	msg.SetId("m1")
	msg.SetConversationID("c1")
	msg.SetRole("assistant")
	msg.SetType("text")

	got := messagePatchPayload(msg)
	require.EqualValues(t, "assistant", got["role"])
	require.EqualValues(t, "text", got["messageType"])
}

func TestPublishTurnEvent_RunningTurnPublishesStartedControl(t *testing.T) {
	bus := streaming.NewMemoryBus(1)
	svc := &Service{streamPub: bus}
	sub, err := bus.Subscribe(context.Background(), nil)
	require.NoError(t, err)
	defer sub.Close()

	turn := convcli.NewTurn()
	turn.SetId("turn-1")
	turn.SetConversationID("conv-1")
	turn.SetStatus("running")
	turn.SetRunID("run-1")
	turn.SetCreatedAt(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))

	svc.publishTurnEvent(context.Background(), turn)

	select {
	case ev := <-sub.C():
		require.NotNil(t, ev)
		require.Equal(t, streaming.EventTypeControl, ev.Type)
		require.Equal(t, "turn_started", ev.Op)
		require.Equal(t, "turn-1", ev.ID)
		require.Equal(t, "conv-1", ev.StreamID)
		require.EqualValues(t, "turn-1", ev.Patch["turnId"])
		require.EqualValues(t, "running", ev.Patch["status"])
		require.EqualValues(t, "run-1", ev.Patch["runId"])
		require.EqualValues(t, "2026-01-02T03:04:05Z", ev.Patch["createdAt"])
	case <-time.After(2 * time.Second):
		t.Fatal("expected turn_started event")
	}
}
