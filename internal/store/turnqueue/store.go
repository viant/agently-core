package turnqueue

import (
	"context"
	"fmt"
	"reflect"

	read "github.com/viant/agently-core/internal/datly/turnqueue/read"
	write "github.com/viant/agently-core/internal/datly/turnqueue/write"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
)

// Store maps the application's queue caller to the one generated reader and
// writer for turn_queue. Transaction ownership remains with the host runtime.
type Store struct {
	Invoker dexec.ComponentInvoker
}

var readerTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[read.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/api/agently/turnqueue/list"},
}
var writerTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/turnqueue"},
}

func (s *Store) List(ctx context.Context, input *read.QueueRowsInput) ([]*read.QueueRowView, error) {
	if s == nil || s.Invoker == nil {
		return nil, fmt.Errorf("turn queue store is not configured")
	}
	if input == nil {
		input = &read.QueueRowsInput{}
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: readerTarget, Input: input})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*read.QueueRowsOutput)
	if !ok || out == nil {
		return nil, fmt.Errorf("turn queue reader returned %T", value)
	}
	return out.Data, nil
}

func (s *Store) Patch(ctx context.Context, row *write.TurnQueue) error {
	if row == nil {
		return fmt.Errorf("turn queue input is required")
	}
	return s.PatchMany(ctx, []*write.TurnQueue{row})
}

// PatchMany submits all queue changes to a single generated writer invocation.
// A late failure rolls back earlier rows under the host's transaction policy.
func (s *Store) PatchMany(ctx context.Context, rows []*write.TurnQueue) error {
	if s == nil || s.Invoker == nil {
		return fmt.Errorf("turn queue store is not configured")
	}
	input := &write.Input{}
	input.SetQueues(rows)
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: writerTarget, Input: input})
	if err != nil {
		return err
	}
	if _, ok := value.(*write.Output); !ok {
		return fmt.Errorf("turn queue writer returned %T", value)
	}
	return nil
}
