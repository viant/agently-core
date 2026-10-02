package conversationtree

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	convread "github.com/viant/agently-core/internal/datly/conversation/read"
	legacyread "github.com/viant/agently-core/internal/datly/legacyrun/read"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	runread "github.com/viant/agently-core/internal/datly/run/read"
	"github.com/viant/agently-core/internal/store/maintenancediag"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"github.com/viant/xdatly/state"
)

const deleteBatchSize = 500

var conversationReaderTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[convread.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/api/agently/conversation/{id}"},
}
var runReaderTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[runread.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/api/agently/run/{id}"},
}

func (d *Discoverer) LockConversationGraph(ctx context.Context, graph *Graph) (retErr error) {
	done := maintenancediag.Phase(ctx, "lock_conversations")
	defer func() { done(retErr, "") }()
	if d == nil || d.Invoker == nil || d.OwnerID == nil {
		return fmt.Errorf("conversation graph reader is not configured")
	}
	if err := d.authorize(ctx, graph); err != nil {
		return err
	}
	return eachDeleteBatch(sortedMapKeys(graph.Nodes), func(ids []string) error {
		input := &convread.ConversationInput{}
		input.SetIds(ids)
		value, err := d.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{ReaderOptions: queryselectors.ForUpdateOptions(ctx, true), Target: conversationReaderTarget, Input: input, Providers: lockProviders("conversationaccess", d.OwnerID(ctx))})
		if err != nil {
			return err
		}
		output, ok := value.(*convread.ConversationOutput)
		if !ok || output == nil {
			return fmt.Errorf("conversation lock reader returned %T", value)
		}
		if len(output.Data) != len(ids) {
			return ErrNotFound
		}
		return nil
	})
}

func (d *Discoverer) LockDeletePlanRuns(ctx context.Context, plan *DeletePlan) (retErr error) {
	done := maintenancediag.Phase(ctx, "lock_runs")
	defer func() { done(retErr, "") }()
	if d == nil || d.Invoker == nil || d.OwnerID == nil || plan == nil {
		return fmt.Errorf("conversation deletion plan is required")
	}
	ids := append([]string(nil), plan.RunIDs...)
	for _, row := range plan.DetachRuns {
		if row != nil {
			ids = append(ids, row.Id)
		}
	}
	if err := eachDeleteBatch(ids, func(ids []string) error {
		input := &runread.RunRowsInput{}
		input.SetIds(ids)
		value, err := d.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{ReaderOptions: queryselectors.ForUpdateOptions(ctx, true), Target: runReaderTarget, Input: input, Providers: lockProviders("runaccess", d.OwnerID(ctx))})
		if err != nil {
			return err
		}
		if output, ok := value.(*runread.RunRowsOutput); !ok || output == nil {
			return fmt.Errorf("run lock reader returned %T", value)
		}
		return nil
	}); err != nil {
		return err
	}
	if !plan.Tables["schedule_run"] {
		return nil
	}
	return eachDeleteBatch(plan.ScheduleRunIDs, func(ids []string) error {
		input := &legacyread.Input{}
		input.SetIDs(ids)
		value, err := d.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{ReaderOptions: queryselectors.ForUpdateOptions(ctx, true), Target: legacyReaderTarget, Input: input, Providers: lockProviders("schedulerunaccess", d.OwnerID(ctx))})
		if err != nil {
			return err
		}
		if output, ok := value.(*legacyread.Output); !ok || output == nil {
			return fmt.Errorf("legacy run lock reader returned %T", value)
		}
		return nil
	})
}

func lockProviders(kind, owner string) []locator.Provider {
	owner = strings.TrimSpace(owner)
	return []locator.Provider{
		provider.Named(kind, func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			switch name {
			case "internal", "graph":
				return true, true, nil
			case "list", "ascending", "enforceVisibility":
				return false, true, nil
			case "mode":
				return "rows", true, nil
			}
			return nil, false, nil
		}),
		provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil }),
		queryselectors.Provider(state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Fields: []string{"id"}}}}),
	}
}

func eachDeleteBatch(ids []string, run func([]string) error) error {
	ids = normalizeIDs(ids)
	for start := 0; start < len(ids); start += deleteBatchSize {
		end := start + deleteBatchSize
		if end > len(ids) {
			end = len(ids)
		}
		if err := run(ids[start:end]); err != nil {
			return err
		}
	}
	return nil
}
