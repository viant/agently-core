package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"time"

	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/runtime/streaming"
	modelcall "github.com/viant/agently-core/service/core/modelcall"
	goalsys "github.com/viant/agently-core/service/goal"
	dexec "github.com/viant/datly/exec"
)

// executeAGUIGoalTransaction commits a deterministic domain mutation and its
// protocol result together. It requires native participants in the same runtime.
// It emits all successful boundaries in the journal; the handler only replays it.
func executeAGUIGoalTransaction(ctx context.Context, client Client, store aguistore.Store, record *aguistore.Run, operation string, payload json.RawMessage) (*aguistore.Run, error) {
	backend, ok := client.(*backendClient)
	if !ok || backend == nil {
		return nil, fmt.Errorf("atomic goal commands require the native backend")
	}
	repository, ok := store.(*aguistore.ComponentStore)
	if !ok || repository == nil || record == nil {
		return nil, fmt.Errorf("atomic goal commands require the native protocol store")
	}
	participant, ok := backend.goalRepo.(interface{ DatlyInvoker() dexec.ComponentInvoker })
	if !ok || !sameAGUIInvoker(participant.DatlyInvoker(), repository.Invoker) || !sameAGUIInvoker(backend.goalInvoker, repository.Invoker) {
		return nil, fmt.Errorf("goal and protocol records must use one native Datly runtime")
	}
	if backend.schedulerSvc != nil && !sameAGUIInvoker(backend.schedulerSvc.DatlyInvoker(), repository.Invoker) {
		return nil, fmt.Errorf("goal scheduler must use the protocol's native Datly runtime")
	}
	if err := validateAcceptedGoalCommand(record, operation, payload); err != nil {
		return nil, err
	}
	executor := &goalTransactionExecutor{backend: backend, operation: operation, payload: append(json.RawMessage(nil), payload...), buffer: &goalNotificationBuffer{}}
	next, outcome, err := aguistore.ExecuteCommand(ctx, repository.Invoker, &aguistore.CommandInput{Principal: record.Principal, ThreadID: record.ThreadID, RunID: record.RunID, ExpectedRevision: record.Revision, LeaseOwner: record.LeaseOwner}, executor)
	if err != nil {
		return nil, err
	}
	if !outcome.CommitConfirmed() {
		return nil, fmt.Errorf("goal command transaction did not confirm an owned commit")
	}
	if err := executor.buffer.flush(context.WithoutCancel(ctx), backend.streaming, executor.publisher); err != nil {
		// The result is already durable. Publication failure must not be mistaken for
		// rollback or justify executing this command again.
		return next, fmt.Errorf("goal command committed; notification delivery failed: %w", err)
	}
	return next, nil
}

// runAGUIGoal is the durable worker entry point. Runtime execution is deliberately
// absent: this path does not invoke Query, a model, or a scheduler runner.
func runAGUIGoal(ctx context.Context, client Client, _ aguiRuntime, store aguistore.Store, record *aguistore.Run, operation string, payload json.RawMessage) error {
	_, err := executeAGUIGoalTransaction(ctx, client, store, record, operation, payload)
	return err
}

type goalTransactionExecutor struct {
	backend   *backendClient
	operation string
	payload   json.RawMessage
	publisher modelcall.StreamPublisher
	buffer    *goalNotificationBuffer
}

func (e *goalTransactionExecutor) Execute(ctx context.Context, invoker dexec.ComponentInvoker, record *aguistore.Run) ([]json.RawMessage, error) {
	if err := validateAcceptedGoalCommand(record, e.operation, e.payload); err != nil {
		return nil, err
	}
	copied := *e.backend
	copied.goalRepo = goalsys.NewStore(invoker)
	copied.goalInvoker = invoker
	if copied.schedulerSvc != nil {
		scheduler, err := copied.schedulerSvc.ForGoalCommand(invoker)
		if err != nil {
			return nil, err
		}
		copied.schedulerSvc = scheduler
	}
	copied.streaming = e.buffer.withSubscriber(e.backend.streaming)
	e.publisher, _ = modelcall.StreamPublisherFromContext(ctx)
	ctx = modelcall.WithStreamPublisher(ctx, e.buffer)
	result, handled, err := dispatchAGUIGoal(ctx, &copied, record.ConversationID, e.operation, e.payload)
	if err != nil {
		return nil, err
	}
	if !handled {
		return nil, fmt.Errorf("unsupported goal command %s", e.operation)
	}

	if e.operation != "goal.get" && e.buffer.count() == 0 {
		event := &streaming.Event{Type: streaming.EventTypeGoalUpdated, ConversationID: record.ConversationID, StreamID: record.ConversationID, CreatedAt: time.Now().UTC()}
		resultValue := result.(*AGUIGoalCommandResult)
		if resultValue.Goal != nil {
			event.GoalID = resultValue.Goal.ID
			event.Status = resultValue.Goal.Status
			event.Patch = map[string]any{"goal": resultValue.Goal}
		}
		if e.operation == "goal.clear" {
			event.Type = streaming.EventTypeGoalCleared
			event.Patch = map[string]any{"goal": nil}
		}
		if err := e.buffer.Publish(ctx, &modelcall.StreamEvent{ConversationID: record.ConversationID, Event: event}); err != nil {
			return nil, err
		}
	}
	if err = e.buffer.err(); err != nil {
		return nil, err
	}
	var input struct {
		ForwardedProps struct {
			Agently struct {
				RequestID string `json:"requestId"`
			} `json:"agently"`
		} `json:"forwardedProps"`
	}
	if err = json.Unmarshal(record.Input, &input); err != nil {
		return nil, err
	}
	events := []json.RawMessage{}
	if record.LastSequence == 0 {
		events = append(events, rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": record.ThreadID, "runId": record.RunID, "protocolVersion": "1.0"}))
	}
	events = append(events, rawAGUI(map[string]any{"type": "CUSTOM", "name": "agently.goal.result", "value": map[string]any{"version": "1", "operation": e.operation, "requestId": input.ForwardedProps.Agently.RequestID, "result": result}}), rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": record.ThreadID, "runId": record.RunID, "outcome": map[string]any{"type": "success"}}))
	return events, nil
}
func sameAGUIInvoker(a, b dexec.ComponentInvoker) bool {
	if a == nil || b == nil {
		return false
	}
	av, bv := reflect.ValueOf(a), reflect.ValueOf(b)
	return av.Type() == bv.Type() && av.Kind() == reflect.Pointer && av.Pointer() == bv.Pointer()
}
func validateAcceptedGoalCommand(record *aguistore.Run, operation string, payload json.RawMessage) error {
	var accepted struct {
		ForwardedProps struct {
			Agently struct {
				Version, Operation string
				Payload            json.RawMessage
			} `json:"agently"`
		} `json:"forwardedProps"`
	}
	if record == nil || json.Unmarshal(record.Input, &accepted) != nil || accepted.ForwardedProps.Agently.Version != "1" || accepted.ForwardedProps.Agently.Operation != operation {
		return fmt.Errorf("goal command differs from accepted input")
	}
	canonical := func(raw []byte) []byte {
		if len(bytes.TrimSpace(raw)) == 0 {
			raw = []byte(`{}`)
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if decoder.Decode(&value) != nil {
			return nil
		}
		data, _ := json.Marshal(value)
		return data
	}
	if !bytes.Equal(canonical(accepted.ForwardedProps.Agently.Payload), canonical(payload)) {
		return fmt.Errorf("goal payload differs from accepted input")
	}
	return nil
}

type goalNotificationBuffer struct {
	mu         sync.Mutex
	messages   []*modelcall.StreamEvent
	failure    error
	subscriber streaming.Subscriber
}

// Model streaming notifications are bounded and detached before commit.
func (b *goalNotificationBuffer) Publish(_ context.Context, event *modelcall.StreamEvent) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.messages) >= 64 {
		b.failure = fmt.Errorf("goal notification buffer exceeded 64 events")
		return b.failure
	}
	data, err := json.Marshal(event)
	if err != nil {
		b.failure = err
		return err
	}
	var cloned modelcall.StreamEvent
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err = decoder.Decode(&cloned); err != nil {
		b.failure = err
		return err
	}
	b.messages = append(b.messages, &cloned)
	return nil
}
func (b *goalNotificationBuffer) err() error { b.mu.Lock(); defer b.mu.Unlock(); return b.failure }
func (b *goalNotificationBuffer) withSubscriber(subscriber streaming.Subscriber) streaming.Bus {
	b.subscriber = subscriber
	return &goalBufferBus{buffer: b}
}
func (b *goalNotificationBuffer) flush(ctx context.Context, bus streaming.Publisher, publisher modelcall.StreamPublisher) error {
	b.mu.Lock()
	events := append([]*modelcall.StreamEvent(nil), b.messages...)
	b.mu.Unlock()
	for _, event := range events {
		if publisher != nil {
			if err := publisher.Publish(ctx, event); err != nil {
				return err
			}
		} else if bus != nil && event.Event != nil {
			if err := bus.Publish(ctx, event.Event); err != nil {
				return err
			}
		}
	}
	return nil
}

type goalBufferBus struct{ buffer *goalNotificationBuffer }

func (b *goalBufferBus) Publish(ctx context.Context, event *streaming.Event) error {
	return b.buffer.Publish(ctx, &modelcall.StreamEvent{ConversationID: event.ConversationID, Event: event})
}
func (b *goalBufferBus) Subscribe(ctx context.Context, filter streaming.Filter) (streaming.Subscription, error) {
	if b.buffer.subscriber == nil {
		return nil, fmt.Errorf("goal source subscriber is unavailable")
	}
	return b.buffer.subscriber.Subscribe(ctx, filter)
}

func (b *goalNotificationBuffer) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.messages)
}
