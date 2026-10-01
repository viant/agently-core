package scheduledelete

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	tree "github.com/viant/agently-core/internal/store/conversationtree"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
)

var deleteTarget = dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[Component]().PkgPath(), Name: "ScheduleCascadeDelete"}, Route: spec.RouteRef{Method: "POST", Path: "/v1/internal/agently/scheduler/delete"}}

type Store struct {
	Invoker dexec.ComponentInvoker
	OwnerID func(context.Context) string
	Schema  tree.TableInspector
}

func (s *Store) DeleteSchedule(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	return s.invoke(ctx, &Input{ID: strings.TrimSpace(id)})
}
func (s *Store) DeleteRun(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: empty id", ErrScheduledRunNotFound)
	}
	return s.invoke(ctx, &Input{ID: strings.TrimSpace(id), RunOnly: true})
}
func (s *Store) invoke(ctx context.Context, input *Input) error {
	if s == nil || s.Invoker == nil || s.OwnerID == nil {
		return fmt.Errorf("schedule deletion store is not configured")
	}
	owner := strings.TrimSpace(s.OwnerID(ctx))
	schema := s.Schema
	if schema == nil {
		schema, _ = s.Invoker.(tree.TableInspector)
	}
	if schema == nil {
		return fmt.Errorf("schedule deletion requires linked schema metadata")
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: deleteTarget, Input: input, Providers: []locator.Provider{provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil }), provider.Named("conversationtreeSchema", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		if name == "inspector" {
			return schema, true, nil
		}
		return nil, false, nil
	})}})
	if err != nil {
		return err
	}
	if output, ok := value.(*Output); !ok || output == nil {
		return fmt.Errorf("schedule deletion returned %T", value)
	}
	return nil
}
func invokeTree(ctx context.Context, invoker dexec.ComponentInvoker, roots, runs, legacy []string, now time.Time) error {
	return tree.InvokeDelete(ctx, invoker, &tree.DeleteInput{RootIDs: normalize(roots), RunIDs: normalize(runs), ScheduleRunIDs: normalize(legacy), Now: &now})
}
