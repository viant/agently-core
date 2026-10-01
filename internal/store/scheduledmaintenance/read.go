package scheduledmaintenance

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	convread "github.com/viant/agently-core/internal/datly/conversation/read"
	legacyread "github.com/viant/agently-core/internal/datly/legacyrun/read"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	runread "github.com/viant/agently-core/internal/datly/run/read"
	schedread "github.com/viant/agently-core/internal/datly/schedule/read"
	tree "github.com/viant/agently-core/internal/store/conversationtree"

	"github.com/viant/agently-core/internal/datly/dbtime"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"github.com/viant/xdatly/state"
)

type CandidateRequest struct {
	InactiveBefore, AfterActivity time.Time
	AfterRunID                    string
	Limit                         int
}
type Candidate struct {
	sortID                 string
	RunID, ExpectedOwnerID string
	ActivityAt             time.Time
}
type Store struct {
	Invoker dexec.ComponentInvoker
	Schema  tree.TableInspector
}

var runTarget = dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[runread.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/run/{id}"}}
var legacyTarget = dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[legacyread.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/scheduler/legacy-run"}}
var scheduleTarget = dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[schedread.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/scheduler/schedule/{id}"}}
var conversationTarget = dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[convread.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/conversation/{id}"}}

func (s *Store) Candidates(ctx context.Context, request CandidateRequest) ([]Candidate, error) {
	if s == nil || s.Invoker == nil || request.InactiveBefore.IsZero() || request.Limit <= 0 || request.AfterActivity.IsZero() != (strings.TrimSpace(request.AfterRunID) == "") {
		return nil, fmt.Errorf("invalid scheduled maintenance candidate request")
	}
	request.InactiveBefore = request.InactiveBefore.UTC()
	request.AfterActivity = request.AfterActivity.UTC()
	request.AfterRunID = strings.TrimSpace(request.AfterRunID)
	input := &runread.RunRowsInput{}
	input.SetMaintenanceBefore(request.InactiveBefore)
	input.SetMaintenanceBeforeSecond(request.InactiveBefore.UTC().Format("2006-01-02 15:04:05"))
	input.SetMaintenanceBeforeNano(int64(request.InactiveBefore.Nanosecond()))
	if request.AfterRunID != "" {
		input.SetMaintenanceAfterActivity(request.AfterActivity.UTC().Format("2006-01-02 15:04:05"))
		input.SetMaintenanceAfterNano(int64(request.AfterActivity.Nanosecond()))
		input.SetMaintenanceAfterId(request.AfterRunID)
	}
	input.SetFields([]string{"id", "activity_raw", "maintenance_owner_id"})
	input.SetLimit(request.Limit)
	rows, err := readRows[runread.RunRowsOutput](ctx, s.Invoker, runTarget, input, providers("runaccess", "scheduledMaintenance", false, nil), false)
	if err != nil {
		return nil, err
	}
	result := make([]Candidate, 0, 2*request.Limit)
	for _, row := range rows.Data {
		if row == nil {
			continue
		}
		activity, ok := parseActivity(deref(row.ActivityRaw))
		if !ok {
			return nil, fmt.Errorf("scheduled maintenance candidate %q has invalid activity time %q", row.Id, deref(row.ActivityRaw))
		}
		result = append(result, Candidate{sortID: row.Id, RunID: strings.TrimSpace(row.Id), ExpectedOwnerID: strings.TrimSpace(row.MaintenanceOwnerId), ActivityAt: activity})
	}
	schema := s.Schema
	if schema == nil {
		driver, _ := s.Invoker.(tree.DriverInspector)
		schema, err = tree.MaintenanceSchema(ctx, driver)
		if err != nil {
			return nil, err
		}
	}
	if schema == nil {
		return nil, fmt.Errorf("scheduled maintenance requires linked schema metadata")
	}
	legacyAvailable, err := schema.HasTable(ctx, "agently", "schedule_run")
	if err != nil {
		return nil, err
	}
	if legacyAvailable {
		query := &legacyread.Input{}
		query.SetMaintenanceBefore(request.InactiveBefore)
		query.SetMaintenanceBeforeSecond(request.InactiveBefore.UTC().Format("2006-01-02 15:04:05"))
		query.SetMaintenanceBeforeNano(int64(request.InactiveBefore.Nanosecond()))
		if request.AfterRunID != "" {
			query.SetMaintenanceAfterActivity(request.AfterActivity.UTC().Format("2006-01-02 15:04:05"))
			query.SetMaintenanceAfterNano(int64(request.AfterActivity.Nanosecond()))
			query.SetMaintenanceAfterId(request.AfterRunID)
		}
		query.SetFields([]string{"id", "activity_raw", "maintenance_owner_id"})
		query.SetLimit(request.Limit)
		rows, err := readRows[legacyread.Output](ctx, s.Invoker, legacyTarget, query, providers("schedulerunaccess", "rows", true, nil), false)
		if err != nil {
			return nil, err
		}
		for _, row := range rows.Data {
			if row == nil {
				continue
			}
			activity, ok := parseActivity(deref(row.ActivityRaw))
			if !ok {
				return nil, fmt.Errorf("scheduled maintenance candidate %q has invalid activity time %q", row.Id, deref(row.ActivityRaw))
			}
			result = append(result, Candidate{sortID: row.Id, RunID: strings.TrimSpace(row.Id), ExpectedOwnerID: strings.TrimSpace(row.MaintenanceOwnerId), ActivityAt: activity})
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].ActivityAt.Equal(result[j].ActivityAt) {
			return result[i].sortID < result[j].sortID
		}
		return result[i].ActivityAt.Before(result[j].ActivityAt)
	})
	if len(result) > request.Limit {
		result = result[:request.Limit]
	}
	return result, nil
}

func readRows[T any](ctx context.Context, invoker dexec.ComponentInvoker, target dexec.ComponentTarget, input any, providers []locator.Provider, lock bool) (*T, error) {
	value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{ReaderOptions: queryselectors.ForUpdateOptions(ctx, lock), Target: target, Input: input, Providers: providers})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*T)
	if !ok || out == nil {
		return nil, fmt.Errorf("scheduled maintenance reader returned %T", value)
	}
	return out, nil
}
func providers(kind, mode string, maintenance bool, fields []string) []locator.Provider {
	owner := ""
	result := []locator.Provider{provider.Named(kind, func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		switch name {
		case "internal":
			return true, true, nil
		case "mode":
			return mode, true, nil
		case "maintenance":
			return maintenance, true, nil
		case "graph":
			return true, true, nil
		case "list", "ascending", "enforceVisibility":
			return false, true, nil
		}
		return nil, false, nil
	}), provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil })}
	if len(fields) > 0 {
		result = append(result, queryselectors.Provider(state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Fields: fields}}}))
	}
	return result
}
func parseActivity(value string) (time.Time, bool) { return dbtime.ParseActivity(value) }
func deref(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
func later(left, right time.Time) time.Time {
	if right.After(left) {
		return right
	}
	return left
}
