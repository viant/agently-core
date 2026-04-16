package streaming

import (
	"context"
	"errors"
	"log"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
)

var ErrClosed = errors.New("streaming bus is closed")

type MemoryBus struct {
	mu     sync.RWMutex
	closed bool
	buffer int
	subs   map[string]*memorySub
	seq    map[string]int64
}

type memorySub struct {
	id     string
	filter Filter
	ch     chan *Event
	once   sync.Once
	parent *MemoryBus
}

func NewMemoryBus(buffer int) *MemoryBus {
	if buffer <= 0 {
		buffer = 64
	}
	return &MemoryBus{
		buffer: buffer,
		subs:   map[string]*memorySub{},
		seq:    map[string]int64{},
	}
}

func (b *MemoryBus) Publish(ctx context.Context, event *Event) error {
	if event == nil {
		return nil
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return ErrClosed
	}
	streamID := event.ConversationID
	if streamID == "" {
		streamID = event.StreamID
	}
	if streamID != "" && event.EventSeq == 0 {
		b.seq[streamID]++
		event.EventSeq = b.seq[streamID]
	}
	debugTiming := streamTimingDebugEnabled()
	subs := make([]*memorySub, 0, len(b.subs))
	for _, sub := range b.subs {
		subs = append(subs, sub)
	}
	b.mu.Unlock()

	if debugTiming {
		log.Printf("[debug][streaming-bus] ts=%s published_at=%s type=%q stream_id=%q convo=%q turn=%q message=%q assistant=%q elicitation=%q seq=%d status=%q created_at=%s",
			time.Now().UTC().Format(time.RFC3339Nano),
			time.Now().UTC().Format(time.RFC3339Nano),
			string(event.Type),
			event.StreamID,
			event.ConversationID,
			event.TurnID,
			event.MessageID,
			event.AssistantMessageID,
			event.ElicitationID,
			event.EventSeq,
			event.Status,
			event.CreatedAt.UTC().Format(time.RFC3339Nano),
		)
	}

	for _, sub := range subs {
		if sub.filter != nil && !sub.filter(event) {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case sub.ch <- event:
		default:
			log.Printf("[warn][streaming-bus] DROPPED event type=%q stream_id=%q convo=%q turn=%q tool=%q sub=%q buf_cap=%d",
				string(event.Type), event.StreamID, event.ConversationID, event.TurnID, event.ToolName, sub.id, cap(sub.ch))
		}
	}
	return nil
}

func streamTimingDebugEnabled() bool {
	value := os.Getenv("AGENTLY_DEBUG_STREAM_TIMING")
	switch value {
	case "1", "true", "TRUE", "on", "ON", "yes", "YES":
		return true
	default:
		return false
	}
}

func (b *MemoryBus) Subscribe(_ context.Context, filter Filter) (Subscription, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, ErrClosed
	}
	sub := &memorySub{
		id:     uuid.NewString(),
		filter: filter,
		ch:     make(chan *Event, b.buffer),
		parent: b,
	}
	b.subs[sub.id] = sub
	return sub, nil
}

func (b *MemoryBus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	for id, sub := range b.subs {
		close(sub.ch)
		delete(b.subs, id)
	}
	return nil
}

func (s *memorySub) ID() string { return s.id }

func (s *memorySub) C() <-chan *Event { return s.ch }

func (s *memorySub) Close() error {
	s.once.Do(func() {
		s.parent.mu.Lock()
		defer s.parent.mu.Unlock()
		if _, ok := s.parent.subs[s.id]; ok {
			delete(s.parent.subs, s.id)
			close(s.ch)
		}
	})
	return nil
}
