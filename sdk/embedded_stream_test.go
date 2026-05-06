package sdk

import (
	"context"
	"testing"

	"github.com/viant/agently-core/runtime/streaming"
)

type bufferAwareStreamBus struct {
	subscribeCalled     bool
	subscribeOptsCalled bool
}

func (b *bufferAwareStreamBus) Publish(context.Context, *streaming.Event) error { return nil }

func (b *bufferAwareStreamBus) Subscribe(_ context.Context, filter streaming.Filter) (streaming.Subscription, error) {
	b.subscribeCalled = true
	return streaming.NewMemoryBus(1).Subscribe(context.Background(), filter)
}

func (b *bufferAwareStreamBus) SubscribeOpts(_ context.Context, opts ...streaming.SubscribeOption) (streaming.Subscription, error) {
	b.subscribeOptsCalled = true
	return streaming.NewMemoryBus(1).SubscribeOpts(context.Background(), opts...)
}

type plainStreamBus struct {
	subscribeCalled bool
}

func (b *plainStreamBus) Publish(context.Context, *streaming.Event) error { return nil }

func (b *plainStreamBus) Subscribe(_ context.Context, filter streaming.Filter) (streaming.Subscription, error) {
	b.subscribeCalled = true
	return streaming.NewMemoryBus(1).Subscribe(context.Background(), filter)
}

func TestBackendClient_StreamEvents_UsesBufferedSubscribeWhenAvailable(t *testing.T) {
	bus := &bufferAwareStreamBus{}
	client := &backendClient{streaming: bus}

	sub, err := client.StreamEvents(context.Background(), &StreamEventsInput{ConversationID: "conv-1"})
	if err != nil {
		t.Fatalf("StreamEvents() error = %v", err)
	}
	defer sub.Close()

	if !bus.subscribeOptsCalled {
		t.Fatalf("expected SubscribeOpts path to be used")
	}
	if bus.subscribeCalled {
		t.Fatalf("expected plain Subscribe path to be skipped when SubscribeOpts is available")
	}
}

func TestBackendClient_StreamEvents_FallsBackToPlainSubscribe(t *testing.T) {
	bus := &plainStreamBus{}
	client := &backendClient{streaming: bus}

	sub, err := client.StreamEvents(context.Background(), &StreamEventsInput{ConversationID: "conv-1"})
	if err != nil {
		t.Fatalf("StreamEvents() error = %v", err)
	}
	defer sub.Close()

	if !bus.subscribeCalled {
		t.Fatalf("expected plain Subscribe path to be used")
	}
}
