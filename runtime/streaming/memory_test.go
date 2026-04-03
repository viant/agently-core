package streaming

import (
	"context"
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
