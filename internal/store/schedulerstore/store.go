package schedulerstore

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	read "github.com/viant/agently-core/internal/datly/schedule/read"
	write "github.com/viant/agently-core/internal/datly/schedule/write"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
)

type Store struct {
	Invoker dexec.ComponentInvoker
	OwnerID func(context.Context) string
}

var readerTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[read.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/api/agently/scheduler/schedule/{id}"},
}
var writerTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/scheduler/"},
}

// List reads one or all schedules with the canonical owner/internal predicate.
func (s *Store) List(ctx context.Context, id string, internal bool) ([]*read.ScheduleView, error) {
	if s == nil || s.Invoker == nil || s.OwnerID == nil {
		return nil, fmt.Errorf("schedule component store is not configured")
	}
	owner := strings.TrimSpace(s.OwnerID(ctx))
	input := &read.ScheduleInput{}
	if id = strings.TrimSpace(id); id != "" {
		input.SetId(id)
	}
	providers := []locator.Provider{
		provider.Named("scheduleaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "internal" {
				return internal, true, nil
			}
			return nil, false, nil
		}),
		provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil }),
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: readerTarget, Input: input, Providers: providers})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*read.ScheduleOutput)
	if !ok || out == nil {
		return nil, fmt.Errorf("schedule reader returned %T", value)
	}
	return out.Data, nil
}

func (s *Store) PatchTrusted(ctx context.Context, row *write.Schedule) error {
	if s == nil || s.Invoker == nil {
		return fmt.Errorf("schedule component store is not configured")
	}
	if row == nil {
		return fmt.Errorf("schedule mutation is required")
	}
	input := &write.Input{}
	input.SetSchedules([]*write.Schedule{row})
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: writerTarget, Input: input})
	if err != nil {
		return err
	}
	if _, ok := value.(*write.Output); !ok {
		return fmt.Errorf("schedule writer returned %T", value)
	}
	return nil
}
