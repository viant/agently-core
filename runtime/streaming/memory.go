package streaming

import (
	"context"
	"errors"
	"sync"

	"github.com/google/uuid"
)

var ErrClosed = errors.New("streaming bus is closed")

type MemoryBus struct {
	mu     sync.RWMutex
	closed bool
	buffer int
	subs   map[string]*memorySub
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
	}
}

func (b *MemoryBus) Publish(ctx context.Context, event *Event) error {
	if event == nil {
		return nil
	}
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return ErrClosed
	}
	subs := make([]*memorySub, 0, len(b.subs))
	for _, sub := range b.subs {
		subs = append(subs, sub)
	}
	b.mu.RUnlock()

	for _, sub := range subs {
		if sub.filter != nil && !sub.filter(event) {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case sub.ch <- event:
		default:
			// best effort; drop when consumer is slow
		}
	}
	return nil
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
