package streaming

import "context"

type Filter func(*Event) bool

type Publisher interface {
	Publish(ctx context.Context, event *Event) error
}

type Subscriber interface {
	Subscribe(ctx context.Context, filter Filter) (Subscription, error)
}

type Bus interface {
	Publisher
	Subscriber
}

type Subscription interface {
	ID() string
	C() <-chan *Event
	Close() error
	// Reason returns why the subscription channel closed. Empty while the
	// subscription is still live. One of: ReasonClosed (normal close) or
	// ReasonOverflow (buffer filled and bus dropped the subscription).
	// Consumers should check Reason() after their `range C()` loop exits
	// to distinguish a clean end-of-stream from a forced disconnect.
	Reason() string
	// LastSeq returns the highest EventSeq delivered on this subscription.
	// After an overflow close a client can reconnect and resume from this
	// sequence (Phase 2 of the streaming backpressure work).
	LastSeq() int64
}
