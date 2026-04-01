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

// NewConversationStreamTracker creates a tracker optionally seeded with conversation ID.
func NewConversationStreamTracker(conversationID string) *ConversationStreamTracker {
	tracker := &ConversationStreamTracker{}
	if conversationID != "" {
		tracker.state = &ConversationState{ConversationID: conversationID}
	}
	return tracker
}

// State returns the current canonical state pointer.
func (t *ConversationStreamTracker) State() *ConversationState {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.state
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
