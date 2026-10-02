package queuereorder

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	turnwrite "github.com/viant/agently-core/internal/datly/turn/write"
	queueread "github.com/viant/agently-core/internal/datly/turnqueue/read"
	queuewrite "github.com/viant/agently-core/internal/datly/turnqueue/write"
	dexec "github.com/viant/datly/exec"
	custom "github.com/viant/datly/runtime/handler/custom"
	"github.com/viant/datly/spec"
	"github.com/viant/x"
	xdatly "github.com/viant/xdatly"
	"github.com/viant/xdatly/handler"
)

var ErrConflict = errors.New("queued turn order changed")

// ReorderComponent coordinates the two generated writers; it owns no SQL or
// table mutation policy. The host keeps this route private.
type ReorderComponent struct {
	Contract xdatly.Component[Input, Output] `component:"QueueReorder,path=/v1/internal/agently/turnqueue/reorder,method=POST,handler=NewQueueReorder,internal=true"`
}

type Input struct {
	ConversationID string `parameter:"ConversationID,kind=body,in=conversationId,required=true"`
	FirstID        string `parameter:"FirstID,kind=body,in=firstId,required=true"`
	SecondID       string `parameter:"SecondID,kind=body,in=secondId,required=true"`
	FirstSequence  int64  `parameter:"FirstSequence,kind=body,in=firstSequence,required=true"`
	SecondSequence int64  `parameter:"SecondSequence,kind=body,in=secondSequence,required=true"`
}

type Output struct {
	FirstID        string `json:"firstId"`
	SecondID       string `json:"secondId"`
	FirstSequence  int64  `json:"firstSequence"`
	SecondSequence int64  `json:"secondSequence"`
}

type Reorder struct{}

func NewQueueReorder() handler.Contract[Input, Output] { return &Reorder{} }

// Exports supplies the named factory and typed contract to the Datly 1.0 host
// when it registers this private orchestration component.
func Exports() (*x.Registry, error) {
	registry := x.NewRegistry()
	for _, typ := range []reflect.Type{reflect.TypeFor[Input](), reflect.TypeFor[Output]()} {
		registry.Register(x.NewType(typ))
	}
	factory, err := x.NewFunction(reflect.TypeFor[ReorderComponent]().PkgPath(), "NewQueueReorder", custom.Factory(NewQueueReorder))
	if err != nil {
		return nil, err
	}
	if err := registry.RegisterFunctions(factory); err != nil {
		return nil, err
	}
	return registry, nil
}

var _ handler.Contract[Input, Output] = (*Reorder)(nil)

func (*Reorder) Exec(ctx context.Context, session handler.Session, input *Input, output *Output) error {
	if session == nil || session.Binder() == nil || input == nil || output == nil {
		return fmt.Errorf("queue reorder invocation is incomplete")
	}
	conversationID := strings.TrimSpace(input.ConversationID)
	firstID, secondID := strings.TrimSpace(input.FirstID), strings.TrimSpace(input.SecondID)
	if conversationID == "" || firstID == "" || secondID == "" || firstID == secondID || input.FirstSequence == input.SecondSequence {
		return fmt.Errorf("queue reorder requires one conversation, two distinct turns and two distinct sequences")
	}
	deps := struct {
		Invoker dexec.ComponentInvoker     `bind:"kind=component_invoker,required"`
		Starter handler.TransactionStarter `bind:"kind=transactionStarter,required"`
	}{}
	if err := session.Binder().Bind(ctx, &deps); err != nil {
		return err
	}
	if deps.Invoker == nil || deps.Starter == nil {
		return fmt.Errorf("queue reorder capabilities are unavailable")
	}
	// Start the parent's managed unit before the read so both generated writers
	// join its transaction. The parent alone commits or rolls it back.
	if err := deps.Starter.Start(ctx); err != nil {
		return err
	}
	query := &queueread.QueueRowsInput{}
	query.SetConversationId(conversationID)
	query.SetQueueStatus("queued")
	value, err := deps.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: readerTarget, Input: query})
	if err != nil {
		return err
	}
	rows, ok := value.(*queueread.QueueRowsOutput)
	if !ok || rows == nil {
		return fmt.Errorf("queue reader returned %T", value)
	}
	var first, second *queueread.QueueRowView
	for _, row := range rows.Data {
		if row == nil {
			continue
		}
		switch row.TurnId {
		case firstID:
			if first != nil {
				return ErrConflict
			}
			first = row
		case secondID:
			if second != nil {
				return ErrConflict
			}
			second = row
		}
	}
	if first == nil || second == nil || first.QueueSeq != input.FirstSequence || second.QueueSeq != input.SecondSequence {
		return ErrConflict
	}
	turnFirst, turnSecond := &turnwrite.Turn{}, &turnwrite.Turn{}
	turnFirst.SetId(firstID)
	turnFirst.SetQueueSeq(&input.SecondSequence)
	turnSecond.SetId(secondID)
	turnSecond.SetQueueSeq(&input.FirstSequence)
	turnInput := &turnwrite.Input{}
	turnInput.SetTurns([]*turnwrite.Turn{turnFirst, turnSecond})
	if _, err := deps.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: turnWriterTarget, Input: turnInput}); err != nil {
		return err
	}
	queueFirst, queueSecond := &queuewrite.TurnQueue{}, &queuewrite.TurnQueue{}
	queueFirst.SetId(first.Id)
	queueFirst.SetQueueSeq(&input.SecondSequence)
	queueSecond.SetId(second.Id)
	queueSecond.SetQueueSeq(&input.FirstSequence)
	queueInput := &queuewrite.Input{}
	queueInput.SetQueues([]*queuewrite.TurnQueue{queueFirst, queueSecond})
	if _, err := deps.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: queueWriterTarget, Input: queueInput}); err != nil {
		return err
	}
	output.FirstID, output.SecondID = firstID, secondID
	output.FirstSequence, output.SecondSequence = input.SecondSequence, input.FirstSequence
	return nil
}

var readerTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[queueread.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/api/agently/turnqueue/list"},
}
var turnWriterTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[turnwrite.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/turn"},
}
var queueWriterTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[queuewrite.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/turnqueue"},
}
