package sdk

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
			Status: TurnStatusRunning,
			Elicitation: &ElicitationState{
				ElicitationID: "elic-1",
				Status:        ElicitationStatusPending,
			},
		}},
		Feeds: []*ActiveFeedState{{
			FeedID:    "plan",
			Title:     "Plan",
			ItemCount: 2,
		}},
	})
	if replaced == nil || len(replaced.Turns) != 1 || replaced.Turns[0].TurnID != "turn-2" {
		t.Fatalf("expected replaced transcript state, got %#v", replaced)
	}

	snapshot := tracker.Snapshot()
	if snapshot == nil || snapshot.State == nil || len(snapshot.State.Turns) != 1 {
		t.Fatalf("expected snapshot state, got %#v", snapshot)
	}
	if snapshot.ConversationID != "conv-1" {
		t.Fatalf("expected conversation id conv-1, got %#v", snapshot.ConversationID)
	}
	if snapshot.ActiveTurnID != "turn-2" {
		t.Fatalf("expected active turn id turn-2, got %#v", snapshot.ActiveTurnID)
	}
	if len(snapshot.Feeds) != 1 || snapshot.Feeds[0].FeedID != "plan" {
		t.Fatalf("expected snapshot feeds, got %#v", snapshot.Feeds)
	}
	if snapshot.PendingElicitation == nil || snapshot.PendingElicitation.ElicitationID != "elic-1" {
		t.Fatalf("expected pending elicitation, got %#v", snapshot.PendingElicitation)
	}
	if tracker.ActiveTurnID() != "turn-2" {
		t.Fatalf("expected tracker active turn id turn-2, got %#v", tracker.ActiveTurnID())
	}
	if tracker.PendingElicitation() == nil || tracker.PendingElicitation().ElicitationID != "elic-1" {
		t.Fatalf("expected tracker pending elicitation, got %#v", tracker.PendingElicitation())
	}
	if len(tracker.Feeds()) != 1 || tracker.Feeds()[0].FeedID != "plan" {
		t.Fatalf("expected tracker feeds, got %#v", tracker.Feeds())
	}
	if tracker.ConversationID() != "conv-1" {
		t.Fatalf("expected tracker conversation id conv-1, got %#v", tracker.ConversationID())
	}

	tracker.Clear()
	if tracker.State() != nil {
		t.Fatalf("expected cleared state, got %#v", tracker.State())
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

	require.Eventually(t, func() bool {
		state := tracker.State()
		return state != nil && len(state.Turns) == 1 && state.Turns[0].TurnID == "turn-1"
	}, 2*time.Second, 10*time.Millisecond, "tracker did not apply subscription event")

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("tracker did not stop after context cancel")
	}
}
