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
}
