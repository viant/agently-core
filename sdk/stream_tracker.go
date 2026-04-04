package sdk

import (
	"context"
	"sync"

	"github.com/viant/agently-core/runtime/streaming"
)

// ConversationStreamTracker is a small semantic facade over the canonical reducer.
//
// It gives SDK consumers one place to:
// - apply live stream events
// - replace/reconcile with transcript snapshots
// - read the current canonical state
//
// The goal is to keep stream consumers out of direct reducer bookkeeping.
type ConversationStreamTracker struct {
	mu    sync.RWMutex
	state *ConversationState
}

type ConversationStreamSnapshot struct {
	ConversationID     string             `json:"conversationId,omitempty"`
	State              *ConversationState `json:"state,omitempty"`
	ActiveTurnID       string             `json:"activeTurnId,omitempty"`
	Feeds              []*ActiveFeedState `json:"feeds,omitempty"`
	PendingElicitation *ElicitationState  `json:"pendingElicitation,omitempty"`
}

// NewConversationStreamTracker creates a tracker optionally seeded with conversation ID.
func NewConversationStreamTracker(conversationID string) *ConversationStreamTracker {
	tracker := &ConversationStreamTracker{}
	if conversationID != "" {
		tracker.state = &ConversationState{ConversationID: conversationID}
	}
	return tracker
}

// State returns the current canonical state pointer.
// Returns nil when no events have been applied yet or after Reset/Clear is called.
// Callers must nil-check the result before dereferencing.
func (t *ConversationStreamTracker) State() *ConversationState {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.state
}

// Snapshot returns a lightweight immutable view of the current tracked state.
func (t *ConversationStreamTracker) Snapshot() *ConversationStreamSnapshot {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return &ConversationStreamSnapshot{
		ConversationID:     conversationID(t.state),
		State:              t.state,
		ActiveTurnID:       activeTurnID(t.state),
		Feeds:              activeFeeds(t.state),
		PendingElicitation: pendingElicitation(t.state),
	}
}

// Reset clears the tracked state.
func (t *ConversationStreamTracker) Reset() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state = nil
}

// Clear is an alias for Reset to align with the TS tracker surface.
func (t *ConversationStreamTracker) Clear() {
	t.Reset()
}

// ConversationID returns the currently tracked conversation ID when known.
func (t *ConversationStreamTracker) ConversationID() string {
	if t == nil {
		return ""
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return conversationID(t.state)
}

// ActiveTurn returns the latest non-terminal turn when one exists.
func (t *ConversationStreamTracker) ActiveTurn() *TurnState {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return activeTurn(t.state)
}

// ActiveTurnID returns the ID of the latest non-terminal turn.
func (t *ConversationStreamTracker) ActiveTurnID() string {
	if turn := t.ActiveTurn(); turn != nil {
		return turn.TurnID
	}
	return ""
}

// Feeds returns the currently tracked active feeds.
func (t *ConversationStreamTracker) Feeds() []*ActiveFeedState {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return activeFeeds(t.state)
}

// PendingElicitation returns the latest pending elicitation when present.
func (t *ConversationStreamTracker) PendingElicitation() *ElicitationState {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return pendingElicitation(t.state)
}

// ApplyEvent applies a single streaming event to the tracked state.
func (t *ConversationStreamTracker) ApplyEvent(event *streaming.Event) *ConversationState {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state = Reduce(t.state, event)
	return t.state
}

// ApplyTranscript replaces the tracked state with an authoritative transcript snapshot.
func (t *ConversationStreamTracker) ApplyTranscript(state *ConversationState) *ConversationState {
	if t == nil {
		return state
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state = state
	return t.state
}

// TrackSubscription consumes a streaming subscription until the context is done
// or the subscription channel closes, applying every event to the tracker.
func (t *ConversationStreamTracker) TrackSubscription(ctx context.Context, sub streaming.Subscription) error {
	if t == nil || sub == nil {
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-sub.C():
			if !ok {
				return nil
			}
			t.ApplyEvent(ev)
		}
	}
}

func activeTurn(state *ConversationState) *TurnState {
	if state == nil {
		return nil
	}
	for i := len(state.Turns) - 1; i >= 0; i-- {
		turn := state.Turns[i]
		if turn == nil {
			continue
		}
		switch turn.Status {
		case TurnStatusRunning, TurnStatusWaitingForUser:
			return turn
		}
	}
	return nil
}

func activeTurnID(state *ConversationState) string {
	if turn := activeTurn(state); turn != nil {
		return turn.TurnID
	}
	return ""
}

func activeFeeds(state *ConversationState) []*ActiveFeedState {
	if state == nil || len(state.Feeds) == 0 {
		return nil
	}
	return state.Feeds
}

func pendingElicitation(state *ConversationState) *ElicitationState {
	if state == nil {
		return nil
	}
	for i := len(state.Turns) - 1; i >= 0; i-- {
		turn := state.Turns[i]
		if turn == nil || turn.Elicitation == nil {
			continue
		}
		if turn.Elicitation.Status == ElicitationStatusPending {
			return turn.Elicitation
		}
	}
	return nil
}

func conversationID(state *ConversationState) string {
	if state == nil {
		return ""
	}
	return state.ConversationID
}
