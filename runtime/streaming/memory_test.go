package streaming

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestMemoryBus_PublishSubscribe(t *testing.T) {
	bus := NewMemoryBus(4)
	sub, err := bus.Subscribe(context.Background(), nil)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Close()

	ev := &Event{Type: EventTypeTextDelta, Content: "hello"}
	if err := bus.Publish(context.Background(), ev); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case got := <-sub.C():
		if got == nil || got.Content != "hello" {
			t.Fatalf("unexpected event: %#v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestMemoryBus_AssignsMonotonicEventSeqPerConversation(t *testing.T) {
	bus := NewMemoryBus(4)
	sub, err := bus.Subscribe(context.Background(), nil)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Close()

	first := &Event{Type: EventTypeTextDelta, ConversationID: "conv-1", StreamID: "conv-1", Content: "a"}
	second := &Event{Type: EventTypeTextDelta, ConversationID: "conv-1", StreamID: "conv-1", Content: "b"}
	other := &Event{Type: EventTypeTextDelta, ConversationID: "conv-2", StreamID: "conv-2", Content: "c"}
	explicit := &Event{Type: EventTypeTextDelta, ConversationID: "conv-1", StreamID: "conv-1", Content: "d", EventSeq: 99}

	for _, ev := range []*Event{first, second, other, explicit} {
		if err := bus.Publish(context.Background(), ev); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	got := []*Event{<-sub.C(), <-sub.C(), <-sub.C(), <-sub.C()}
	if got[0].EventSeq != 1 || got[1].EventSeq != 2 {
		t.Fatalf("unexpected conv-1 sequence: %d %d", got[0].EventSeq, got[1].EventSeq)
	}
	if got[2].EventSeq != 1 {
		t.Fatalf("unexpected conv-2 sequence: %d", got[2].EventSeq)
	}
	if got[3].EventSeq != 99 {
		t.Fatalf("explicit sequence overwritten: %d", got[3].EventSeq)
	}
}

// TestMemoryBus_OverflowClosesSubscription verifies Phase 1 of the streaming
// backpressure redesign: a slow subscriber whose buffer fills is terminated
// with ReasonOverflow instead of having events silently dropped.
func TestMemoryBus_OverflowClosesSubscription(t *testing.T) {
	bus := NewMemoryBus(2)
	sub, err := bus.Subscribe(context.Background(), nil)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// Publish buffer + 2 extra events without reading; the last two must
	// trigger an overflow close.
	for i := 0; i < 4; i++ {
		if err := bus.Publish(context.Background(), &Event{
			Type:           EventTypeTextDelta,
			ConversationID: "c1",
			StreamID:       "c1",
			Content:        strconv.Itoa(i),
		}); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	// Drain the buffered events — the channel should then be closed.
	received := 0
drain:
	for {
		select {
		case _, ok := <-sub.C():
			if !ok {
				break drain
			}
			received++
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out draining sub; received=%d", received)
		}
	}
	if received == 0 {
		t.Fatalf("expected at least one event before overflow")
	}
	if sub.Reason() != ReasonOverflow {
		t.Fatalf("reason = %q, want %q", sub.Reason(), ReasonOverflow)
	}
	if sub.LastSeq() <= 0 {
		t.Fatalf("expected LastSeq > 0, got %d", sub.LastSeq())
	}
}

// TestMemoryBus_NormalCloseReason ensures an explicit Close reports the
// normal close reason so consumers can distinguish clean shutdown from
// overflow.
func TestMemoryBus_NormalCloseReason(t *testing.T) {
	bus := NewMemoryBus(4)
	sub, err := bus.Subscribe(context.Background(), nil)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := sub.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if sub.Reason() != ReasonClosed {
		t.Fatalf("reason = %q, want %q", sub.Reason(), ReasonClosed)
	}
}

// TestMemoryBus_PerSubscriberBuffer exercises SubscribeOpts+WithBuffer so a
// UI-style lagging consumer can reserve a deeper buffer than the bus default.
func TestMemoryBus_PerSubscriberBuffer(t *testing.T) {
	bus := NewMemoryBus(2)
	sub, err := bus.SubscribeOpts(context.Background(), WithBuffer(8))
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Close()

	for i := 0; i < 8; i++ {
		if err := bus.Publish(context.Background(), &Event{
			Type:           EventTypeTextDelta,
			ConversationID: "c1",
			StreamID:       "c1",
			Content:        strconv.Itoa(i),
		}); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}
	if sub.Reason() != "" {
		t.Fatalf("unexpected reason before overflow: %q", sub.Reason())
	}
}

// TestMemoryBus_ConcurrentPublishClose covers the prior send-on-closed-channel
// race: Publish and Close racing should never panic.
func TestMemoryBus_ConcurrentPublishClose(t *testing.T) {
	bus := NewMemoryBus(4)
	sub, err := bus.Subscribe(context.Background(), nil)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = bus.Publish(context.Background(), &Event{
				Type: EventTypeTextDelta, ConversationID: "c", StreamID: "c",
			})
		}
	}()
	// Drain a few events then close to race with the publisher.
	go func() {
		count := 0
		for range sub.C() {
			count++
			if count > 100 {
				break
			}
		}
	}()
	time.Sleep(10 * time.Millisecond)
	_ = sub.Close()
	close(stop)
	wg.Wait()
}
