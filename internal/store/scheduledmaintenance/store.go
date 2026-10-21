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
	ctx, capture := tree.WithMaintenancePlanCapture(ctx)
	value, effects, err := maintenancetx.Invoke(ctx, s.Invoker, func(scope context.Context) (context.Context, error) {
		capture.Reset()
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
		if effects != nil {
			summary, summaryErr := effects.CommitSummary()
			if summaryErr != nil {
				return nil, errors.Join(err, summaryErr)
			}
			out, _ := value.(*Output)
			if out == nil {
				out = &Output{RunID: input.RunID, Mode: input.Mode}
			}
			progress := tree.MaintenanceProgress{}
			progress.Add(summary)
			out.Partial, out.CommittedBatches, out.AffectedRows = progress.AffectedRows > 0, progress.CommittedBatches, progress.AffectedRows
			out.ConversationCount, out.Deleted, out.Reason = progress.ConversationCount, false, "partial_error"
			return out, err
		}
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
		var progress tree.MaintenanceProgress
		if capture.HasPlan() {
			if _, err = effects.CommitSummary(); err == nil {
				progress, err = capture.Apply(ctx, s.Invoker, input.Lease)
			}
		} else {
			var summary maintenancetx.Summary
			summary, err = effects.CommitSummary()
			if err == nil {
				progress.Add(summary)
			}
		}
		out.Partial, out.CommittedBatches, out.AffectedRows = progress.Partial, progress.CommittedBatches, progress.AffectedRows
		out.ConversationCount = progress.ConversationCount
		out.Deleted = err == nil && progress.DeletedRows > 0
		if err != nil {
			out.Reason = "partial_error"
			var guarded *maintenancetx.GuardChanged
			if errors.As(err, &guarded) {
				out.Reason = "protected_by_guard"
			}
			return out, err
		}
		out.Reason = "deleted"
		if !out.Deleted && out.Eligible {
			out.Reason = "already_deleted"
		}
	}
	return out, nil
}
