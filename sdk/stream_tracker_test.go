package sdk

import (
	"context"
	"testing"
	"time"

	"github.com/viant/agently-core/runtime/streaming"
)

func TestConversationStreamTracker_ApplyEventAndTranscript(t *testing.T) {
	tracker := NewConversationStreamTracker("conv-1")

	state := tracker.ApplyEvent(&streaming.Event{
		Type:           streaming.EventTypeTurnStarted,
		ConversationID: "conv-1",
		TurnID:         "turn-1",
		UserMessageID:  "user-1",
		CreatedAt:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if state == nil || len(state.Turns) != 1 {
		t.Fatalf("expected one turn, got %#v", state)
	}
	if state.Turns[0].User == nil || state.Turns[0].User.MessageID != "user-1" {
		t.Fatalf("expected user message id, got %#v", state.Turns[0].User)
	}

	replaced := tracker.ApplyTranscript(&ConversationState{
		ConversationID: "conv-1",
		Turns: []*TurnState{{
			TurnID: "turn-2",
			Status: TurnStatusCompleted,
		}},
	})
	if replaced == nil || len(replaced.Turns) != 1 || replaced.Turns[0].TurnID != "turn-2" {
		t.Fatalf("expected replaced transcript state, got %#v", replaced)
	}
}

func TestConversationStreamTracker_TrackSubscription(t *testing.T) {
	bus := streaming.NewMemoryBus(4)
	sub, err := bus.Subscribe(context.Background(), nil)
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	defer sub.Close()

	tracker := NewConversationStreamTracker("")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- tracker.TrackSubscription(ctx, sub)
	}()

	err = bus.Publish(context.Background(), &streaming.Event{
		Type:           streaming.EventTypeTurnStarted,
		ConversationID: "conv-1",
		TurnID:         "turn-1",
	})
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		state := tracker.State()
		if state != nil && len(state.Turns) == 1 && state.Turns[0].TurnID == "turn-1" {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("tracker did not apply subscription event: %#v", tracker.State())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("tracker did not stop after context cancel")
	}
}
