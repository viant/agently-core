package scheduledmaintenance

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	tree "github.com/viant/agently-core/internal/store/conversationtree"
	"github.com/viant/agently-core/internal/store/maintenancediag"
	"github.com/viant/agently-core/internal/store/maintenancelease"
	"github.com/viant/agently-core/internal/store/maintenancetx"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
)

var maintainTarget = dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[Component]().PkgPath(), Name: "ScheduledRunMaintenance"}, Route: spec.RouteRef{Method: "POST", Path: "/v1/internal/agently/scheduled-run/maintenance"}}

func (s *Store) Maintain(ctx context.Context, input *Input) (result *Output, retErr error) {
	ctx, trace := maintenancediag.Begin(ctx, "scheduledmaintenance")
	defer func() { trace.Finish(retErr) }()
	if s == nil || s.Invoker == nil || input == nil {
		return nil, fmt.Errorf("scheduled maintenance runtime and input are required")
	}
	schema := s.Schema
	if schema == nil {
		driver, _ := s.Invoker.(tree.DriverInspector)
		var err error
		schema, err = tree.MaintenanceSchema(ctx, driver)
		if err != nil {
			return nil, err
		}
	}
	if schema == nil {
		return nil, fmt.Errorf("scheduled maintenance requires linked schema metadata")
	}
	providers := []locator.Provider{provider.Named("conversationtreeSchema", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		if name == "inspector" {
			return schema, true, nil
		}
		return nil, false, nil
	})}
	value, effects, err := maintenancetx.Invoke(ctx, s.Invoker, func(scope context.Context) (context.Context, error) {
		if input.Mode == DeleteMode {
			var err error
			scope, err = (&maintenancelease.Store{Invoker: s.Invoker}).Preflight(scope, input.Lease)
			if err != nil {
				return nil, err
			}
		}
		driver, _ := s.Invoker.(maintenancetx.DriverInspector)
		return maintenancetx.Prepare(scope, driver)
	}, dexec.ComponentRequest{Target: maintainTarget, Input: input, Providers: providers})
	if err != nil {
		var guarded *maintenancetx.GuardChanged
		if errors.As(err, &guarded) {
			return &Output{RunID: input.RunID, Mode: input.Mode, Reason: "protected_by_guard"}, nil
		}
		var decision *skipped
		if errors.As(err, &decision) {
			return decision.result, nil
		}
		return nil, err
	}
	out, ok := value.(*Output)
	if !ok || out == nil {
		return nil, fmt.Errorf("scheduled maintenance returned %T", value)
	}
	if input.Mode == DeleteMode {
		out.ConversationCount = int(effects.Deleted("conversation"))
		out.Deleted = effects.DeletedRows() > 0
		if !out.Deleted && out.Eligible {
			out.Reason = "already_deleted"
		}
	}
	return out, nil
}
