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

// Close reasons surfaced via Subscription.Reason() after the subscription
// channel closes. SSE / API handlers should read the reason after their
// `for ev := range sub.C()` loop exits to distinguish a clean end-of-stream
// from a forced termination.
const (
	// ReasonClosed — the caller closed the subscription, or the bus itself
	// was closed. No events were lost from the caller's perspective.
	ReasonClosed = "closed"
	// ReasonOverflow — the subscriber's buffer filled and the bus
	// terminated the subscription to preserve publisher progress. The
	// client missed one or more events; it should reconnect and (once the
	// replay/resume story lands in Phase 2) resume from LastSeq().
	ReasonOverflow = "overflow"
)

type MemoryBus struct {
	mu            sync.RWMutex
	closed        bool
	defaultBuffer int
	subs          map[string]*memorySub
	seq           map[string]int64
}

type memorySub struct {
	id     string
	filter Filter
	ch     chan *Event
	parent *MemoryBus

	// mu guards ch, closed, reason, lastSeq. It is held only for
	// enqueue/close/inspect operations — never across a blocking channel
	// send — so a slow reader cannot block the publisher. Publish uses a
	// non-blocking select while holding mu; overflow is therefore detected
	// under the lock and the subscription is closed atomically.
	mu      sync.Mutex
	closed  bool
	reason  string
	lastSeq int64
}

// SubscribeOption configures a new subscription.
type SubscribeOption func(*subscribeConfig)

type subscribeConfig struct {
	buffer int
	filter Filter
}

// WithBuffer sets the subscriber-side event buffer. Values <= 0 fall back to
// the bus default. UI clients that tolerate lag can pick a deeper buffer to
// avoid overflow-driven disconnects; fast in-process consumers can stay with
// the default.
func WithBuffer(n int) SubscribeOption {
	return func(c *subscribeConfig) { c.buffer = n }
}

// WithFilter installs an event filter on the subscription.
func WithFilter(f Filter) SubscribeOption {
	return func(c *subscribeConfig) { c.filter = f }
}

func NewMemoryBus(buffer int) *MemoryBus {
	if buffer <= 0 {
		buffer = 64
	}
	return &MemoryBus{
		defaultBuffer: buffer,
		subs:          map[string]*memorySub{},
		seq:           map[string]int64{},
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
		sub.deliver(ctx, event)
	}
	return nil
}

// deliver performs a non-blocking send to the subscriber. If the buffer is
// full the subscription is closed with ReasonOverflow; the publisher moves on
// to the next subscriber rather than stalling on any single slow consumer.
func (s *memorySub) deliver(ctx context.Context, event *Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	// Track the highest sequence number delivered (or about-to-be-delivered)
	// so a subsequent overflow/close can report where the stream stopped.
	if event.EventSeq > s.lastSeq {
		s.lastSeq = event.EventSeq
	}
	select {
	case <-ctx.Done():
		return
	case s.ch <- event:
		return
	default:
		// Buffer full. Terminate this subscription with an explicit overflow
		// reason so the consumer can reconnect instead of silently losing
		// events.
		log.Printf("[warn][streaming-bus] OVERFLOW sub=%s stream=%q convo=%q turn=%q type=%q buf_cap=%d last_seq=%d",
			s.id, event.StreamID, event.ConversationID, event.TurnID, string(event.Type), cap(s.ch), s.lastSeq)
		s.reason = ReasonOverflow
		s.closed = true
		close(s.ch)
		s.parent.removeSubLocked(s.id)
	}
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

// Subscribe creates a subscription with the bus default buffer size. Prefer
// SubscribeOpts for per-subscriber tuning.
func (b *MemoryBus) Subscribe(_ context.Context, filter Filter) (Subscription, error) {
	return b.subscribe(filter, b.defaultBuffer)
}

// SubscribeOpts creates a subscription with optional per-subscriber overrides
// (buffer size, filter). Back-pressure tolerance is a consumer concern, so
// UI clients that fall behind during heavy deltas can request a larger buffer
// via WithBuffer(N).
func (b *MemoryBus) SubscribeOpts(_ context.Context, opts ...SubscribeOption) (Subscription, error) {
	cfg := &subscribeConfig{buffer: b.defaultBuffer}
	for _, o := range opts {
		if o != nil {
			o(cfg)
		}
	}
	return b.subscribe(cfg.filter, cfg.buffer)
}

func (b *MemoryBus) subscribe(filter Filter, buffer int) (Subscription, error) {
	if buffer <= 0 {
		buffer = b.defaultBuffer
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, ErrClosed
	}
	sub := &memorySub{
		id:     uuid.NewString(),
		filter: filter,
		ch:     make(chan *Event, buffer),
		parent: b,
	}
	b.subs[sub.id] = sub
	return sub, nil
}

func (b *MemoryBus) Close() error {
	b.mu.Lock()
	subs := make([]*memorySub, 0, len(b.subs))
	for id, sub := range b.subs {
		subs = append(subs, sub)
		delete(b.subs, id)
	}
	b.closed = true
	b.mu.Unlock()
	// Close each subscription outside the bus lock so the per-sub mutex
	// ordering stays (sub.mu taken after bus.mu is released).
	for _, sub := range subs {
		sub.closeWithReason(ReasonClosed)
	}
	return nil
}

// removeSubLocked deletes a subscription from the bus registry. Called from
// memorySub.deliver while holding sub.mu; it acquires the bus lock on its
// own. Safe against duplicate removal.
func (b *MemoryBus) removeSubLocked(id string) {
	b.mu.Lock()
	delete(b.subs, id)
	b.mu.Unlock()
}

func (s *memorySub) ID() string { return s.id }

func (s *memorySub) C() <-chan *Event { return s.ch }

// Reason returns the reason the subscription channel was closed. Empty while
// the subscription is still live. See Reason* constants.
func (s *memorySub) Reason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reason
}

// LastSeq returns the highest EventSeq delivered (or attempted) on this
// subscription. Useful for resume/replay: after range exits on overflow, the
// client can reconnect and request events with EventSeq > LastSeq().
func (s *memorySub) LastSeq() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastSeq
}

func (s *memorySub) Close() error {
	s.closeWithReason(ReasonClosed)
	return nil
}

func (s *memorySub) closeWithReason(reason string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.reason = reason
	close(s.ch)
	s.mu.Unlock()
	// Remove from the bus registry. Safe to call even if the bus itself
	// is closed — removeSubLocked takes the bus lock and a missing entry
	// is a no-op.
	s.parent.removeSubLocked(s.id)
}
