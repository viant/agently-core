package queuereorder

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	queueread "github.com/viant/agently-core/internal/datly/turnqueue/read"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
)

var ErrTurnNotQueued = errors.New("queued turn not found")
var ErrMoveOutsideQueue = errors.New("turn cannot be moved in requested direction")

var reorderTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[ReorderComponent]().PkgPath(), Name: "QueueReorder"},
	Route:     spec.RouteRef{Method: "POST", Path: "/v1/internal/agently/turnqueue/reorder"},
}

// Move selects adjacent queued turns through the transcribed reader, then asks
// the private component to compare and swap both turn and queue rows atomically.
func Move(ctx context.Context, invoker dexec.ComponentInvoker, conversationID, turnID, direction string) error {
	if invoker == nil {
		return fmt.Errorf("queue component invoker is required")
	}
	conversationID, turnID = strings.TrimSpace(conversationID), strings.TrimSpace(turnID)
	if conversationID == "" || turnID == "" {
		return ErrTurnNotQueued
	}
	direction = strings.ToLower(strings.TrimSpace(direction))
	if direction != "up" && direction != "down" {
		return errors.New("direction must be up or down")
	}
	query := &queueread.QueueRowsInput{}
	query.SetConversationId(conversationID)
	query.SetQueueStatus("queued")
	query.SetNativeQueuedOnly(true)
	value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: readerTarget, Input: query})
	if err != nil {
		return err
	}
	out, ok := value.(*queueread.QueueRowsOutput)
	if !ok || out == nil {
		return fmt.Errorf("queue reader returned %T", value)
	}
	index := -1
	for i, row := range out.Data {
		if row != nil && row.TurnId == turnID {
			index = i
			break
		}
	}
	if index < 0 {
		return ErrTurnNotQueued
	}
	other := index - 1
	if direction == "down" {
		other = index + 1
	}
	if other < 0 || other >= len(out.Data) {
		return ErrMoveOutsideQueue
	}
	first, second := out.Data[index], out.Data[other]
	if first == nil || second == nil {
		return ErrConflict
	}
	result, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: reorderTarget, Input: &Input{
		ConversationID: conversationID,
		FirstID:        first.TurnId, SecondID: second.TurnId,
		FirstSequence: first.QueueSeq, SecondSequence: second.QueueSeq,
	}})
	if err != nil {
		return err
	}
	if _, ok := result.(*Output); !ok {
		return fmt.Errorf("queue reorder returned %T", result)
	}
	return nil
}
