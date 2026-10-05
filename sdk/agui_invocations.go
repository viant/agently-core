package sdk

import (
	"context"
	"errors"
	"fmt"
	"sync"

	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
)

// Every invocation is subscribed before Query starts. Requests are acknowledged
// by the journal owner so translator and projection mutations remain ordered.
type aguiInvocationEvent struct {
	invocation  requestctx.Invocation
	event       *streaming.Event
	returned    *requestctx.InvocationResult
	registered  bool
	observerErr error
	ack         chan error
}
type aguiInvocationReturn struct {
	result requestctx.InvocationResult
	done   chan error
}
type aguiInvocationReader struct {
	returned chan aguiInvocationReturn
	done     chan struct{}
	failure  error // read only after done closes (channel synchronizes publication)
}
type aguiInvocationObserver struct {
	ctx                  context.Context
	client               Client
	rootThread, rootTurn string
	events               chan aguiInvocationEvent
	mu                   sync.Mutex
	readers              map[string]*aguiInvocationReader
	invocations          map[string]requestctx.Invocation
	runtime              aguiRuntime
	store                aguistore.Store
	run                  *aguistore.Run
	detachedContexts     map[string]context.Context
}

func newAGUIInvocationObserver(ctx context.Context, client Client, thread, turn string) *aguiInvocationObserver {
	return &aguiInvocationObserver{ctx: ctx, client: client, rootThread: thread, rootTurn: turn, events: make(chan aguiInvocationEvent, 64), readers: map[string]*aguiInvocationReader{}, invocations: map[string]requestctx.Invocation{}}
}
func (o *aguiInvocationObserver) deliver(item aguiInvocationEvent) error {
	item.ack = make(chan error, 1)
	select {
	case o.events <- item:
	case <-o.ctx.Done():
		return o.ctx.Err()
	}
	select {
	case err := <-item.ack:
		return err
	case <-o.ctx.Done():
		return o.ctx.Err()
	}
}
func (o *aguiInvocationObserver) BeforeInvocation(ctx context.Context, inv requestctx.Invocation) error {
	if inv.Detached {
		childCtx, err := startAGUIDetached(ctx, o, inv)
		if err != nil {
			return err
		}
		o.mu.Lock()
		if o.detachedContexts == nil {
			o.detachedContexts = map[string]context.Context{}
		}
		o.detachedContexts[inv.ID] = childCtx
		o.mu.Unlock()
		return nil
	}
	if o.ctx.Err() != nil {
		return nil
	}
	o.mu.Lock()
	parentOK := inv.ParentConversationID == o.rootThread && inv.ParentTurnID == o.rootTurn
	for _, parent := range o.invocations {
		if inv.ParentConversationID == parent.ConversationID && inv.ParentTurnID == parent.TurnID && inv.ParentInvocationID == parent.ID {
			parentOK = true
		}
	}
	_, exists := o.readers[inv.ID]
	o.mu.Unlock()
	if !parentOK || exists {
		return fmt.Errorf("invalid AG-UI invocation causal identity")
	}
	sub, err := subscribeNativeEvents(o.ctx, o.client, &StreamEventsInput{ConversationID: inv.ConversationID, Filter: func(e *streaming.Event) bool {
		return e != nil && e.ConversationID == inv.ConversationID && e.TurnID == inv.TurnID
	}})
	if err != nil {
		return err
	}
	if err = o.deliver(aguiInvocationEvent{invocation: inv, registered: true}); err != nil {
		sub.Close()
		if errors.Is(err, context.Canceled) || errors.Is(err, errAGUIObserverLost) {
			return nil
		}
		return err
	}
	reader := &aguiInvocationReader{returned: make(chan aguiInvocationReturn, 1), done: make(chan struct{})}
	o.mu.Lock()
	o.readers[inv.ID] = reader
	o.invocations[inv.ID] = inv
	o.mu.Unlock()
	go func() {
		defer sub.Close()
		defer close(reader.done)
		detach := func() {
			reader.failure = aguiObserverLost(sub.Reason())
			_ = o.deliver(aguiInvocationEvent{invocation: inv, observerErr: reader.failure})
		}
		for {
			select {
			case <-o.ctx.Done():
				return
			case event, open := <-sub.C():
				if !open {
					detach()
					return
				}
				if o.deliver(aguiInvocationEvent{invocation: inv, event: event}) != nil {
					return
				}
			case returned := <-reader.returned:
				// Query has returned; drain published events before recording its outcome.
				for {
					select {
					case event, open := <-sub.C():
						if !open {
							detach()
							returned.done <- nil // native outcome is independent of observer loss
							return
						}
						if o.deliver(aguiInvocationEvent{invocation: inv, event: event}) != nil {
							return
						}
					default:
						goto drained
					}
				}
			drained:
				returned.done <- o.deliver(aguiInvocationEvent{invocation: inv, returned: &returned.result})
				return
			}
		}
	}()
	return nil
}
func (o *aguiInvocationObserver) InvocationReturned(ctx context.Context, result requestctx.InvocationResult) error {
	if o.ctx.Err() != nil {
		return nil
	}
	if result.Invocation.Detached {
		return nil
	}
	o.mu.Lock()
	reader := o.readers[result.Invocation.ID]
	o.mu.Unlock()
	if reader == nil {
		return fmt.Errorf("AG-UI invocation not registered")
	}
	request := aguiInvocationReturn{result: result, done: make(chan error, 1)}
	select {
	case reader.returned <- request:
		select {
		case err := <-request.done:
			return nativeInvocationObservationAck(err)
		case <-reader.done:
			select {
			case err := <-request.done:
				return nativeInvocationObservationAck(err)
			default:
				if errors.Is(reader.failure, errAGUIObserverLost) {
					return nil
				}
				return fmt.Errorf("AG-UI child observer closed before outcome acknowledgement")
			}
		case <-o.ctx.Done():
			return nil
		}
	case <-reader.done:
		if errors.Is(reader.failure, errAGUIObserverLost) {
			return nil
		}
		return fmt.Errorf("AG-UI child observer closed before outcome")
	case <-o.ctx.Done():
		return nil
	}
}

func (o *aguiInvocationObserver) InvocationContext(ctx context.Context, inv requestctx.Invocation) context.Context {
	if !inv.Detached {
		return ctx
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if detached := o.detachedContexts[inv.ID]; detached != nil {
		return detached
	}
	return ctx
}

// The native invocation's outcome must survive loss of its protocol observer.
func nativeInvocationObservationAck(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errAGUIObserverLost) {
		return nil
	}
	return err
}
