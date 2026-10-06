package agentrun

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/viant/agently-core/internal/datly/queryselectors"
	cube "github.com/viant/agently-core/internal/datly/run/cube"
	rundelete "github.com/viant/agently-core/internal/datly/run/delete"
	read "github.com/viant/agently-core/internal/datly/run/read"
	write "github.com/viant/agently-core/internal/datly/run/write"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"github.com/viant/xdatly/state"
)

type Store struct {
	Invoker dexec.ComponentInvoker
	OwnerID func(context.Context) string
}

var readerTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[read.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/api/agently/run/{id}"},
}
var writerTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/run"},
}
var cubeTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[cube.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/run/report"},
}

var schedulerRunFields = []string{
	"completed_at", "conversation_id", "conversation_kind", "created_at",
	"error_message", "id", "lease_owner", "lease_until", "precondition_passed",
	"precondition_ran_at", "precondition_result", "schedule_id", "scheduled_for",
	"started_at", "status", "updated_at",
}

func SchedulerRunFields() []string { return append([]string(nil), schedulerRunFields...) }

func (s *Store) PatchTrusted(ctx context.Context, rows []*write.MutableRunView) ([]*write.MutableRunView, error) {
	if s == nil || s.Invoker == nil {
		return nil, fmt.Errorf("run component store is not configured")
	}
	input := &write.Input{}
	input.SetRuns(rows)
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: writerTarget, Input: input})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*write.Output)
	if !ok || out == nil {
		return nil, fmt.Errorf("run writer returned %T", value)
	}
	return out.Data, nil
}

// DeleteTrusted ignores identities that are already absent, then submits only
// existing native execution IDs to the guarded generated writer. Missing rows
// after this read remain a strict mutation conflict, preserving race detection.
func (s *Store) DeleteTrusted(ctx context.Context, ids ...string) error {
	if s == nil || s.Invoker == nil {
		return fmt.Errorf("run component store is not configured")
	}
	wanted := map[string]bool{}
	selected := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" && !wanted[id] {
			wanted[id] = true
			selected = append(selected, id)
		}
	}
	if len(selected) == 0 {
		return nil
	}
	reader := *s
	if reader.OwnerID == nil {
		reader.OwnerID = func(context.Context) string { return "" }
	}
	rows := make([]*rundelete.RunDelete, 0, len(selected))
	// Bound each identity lookup below the reader's default page size. The id-only
	// projection deliberately avoids parsing unrelated legacy heartbeat values.
	for start := 0; start < len(selected); start += 64 {
		end := start + 64
		if end > len(selected) {
			end = len(selected)
		}
		batch := map[string]bool{}
		for _, id := range selected[start:end] {
			batch[id] = true
		}
		query := &read.RunRowsInput{}
		query.SetIds(selected[start:end])
		found, err := reader.ListTrusted(ctx, "rows", query, state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Fields: []string{"id"}, Limit: 64}}})
		if err != nil {
			return err
		}
		for _, item := range found {
			if item == nil || !batch[item.Id] {
				return fmt.Errorf("run deletion identity read escaped scope")
			}
			delete(batch, item.Id)
			row := &rundelete.RunDelete{}
			row.SetId(item.Id)
			row.SetShouldDelete(true)
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 {
		return nil
	}
	input := &rundelete.Input{}
	input.SetRuns(rows)
	target := dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[rundelete.WriterComponent]().PkgPath(), Name: "writer"}, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/agently/run/delete"}}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: target, Input: input})
	if err != nil {
		return err
	}
	if _, ok := value.(*rundelete.Output); !ok {
		return fmt.Errorf("run delete writer returned %T", value)
	}
	return nil
}

// GetTrusted reads one run; the caller preserves its conversation-level
// authorization rule after mapping the generated row.
func (s *Store) GetTrusted(ctx context.Context, input *read.RunRowsInput, selectors state.Selectors) (*read.RunRowsView, error) {
	if input == nil {
		return nil, fmt.Errorf("run input is required")
	}
	rows, err := s.ListTrusted(ctx, "rows", input, selectors)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	if len(rows) != 1 || rows[0] == nil {
		return nil, fmt.Errorf("run identity returned %d rows", len(rows))
	}
	return rows[0], nil
}

func (s *Store) ListTrusted(ctx context.Context, mode string, input *read.RunRowsInput, selectors state.Selectors) ([]*read.RunRowsView, error) {
	if s == nil || s.Invoker == nil || s.OwnerID == nil {
		return nil, fmt.Errorf("run component store is not configured")
	}
	if mode != "rows" && mode != "active" && mode != "stale" && mode != "schedulerList" && mode != "schedulerRuns" && mode != "schedulerDue" {
		return nil, fmt.Errorf("unsupported run read mode %q", mode)
	}
	if input == nil {
		input = &read.RunRowsInput{}
	}
	owner := strings.TrimSpace(s.OwnerID(ctx))
	providers := []locator.Provider{
		provider.Named("runaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			switch name {
			case "internal":
				return true, true, nil
			case "mode":
				return mode, true, nil
			}
			return nil, false, nil
		}),
		provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil }),
	}
	if len(selectors) > 0 {
		providers = append(providers, queryselectors.ProviderMapped(selectors, map[string]string{
			"run_rows": "reader", "RunRows": "reader", "active_runs": "reader",
			"ActiveRuns": "reader", "stale_runs": "reader", "StaleRuns": "reader",
		}))
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: readerTarget, Input: input, Providers: providers})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*read.RunRowsOutput)
	if !ok || out == nil {
		return nil, fmt.Errorf("run reader returned %T", value)
	}
	return out.Data, nil
}

func (s *Store) CountScheduler(ctx context.Context, input *cube.RunReportInput) (int, error) {
	if s == nil || s.Invoker == nil || s.OwnerID == nil {
		return 0, fmt.Errorf("run component store is not configured")
	}
	if input == nil {
		input = &cube.RunReportInput{}
	}
	owner := strings.TrimSpace(s.OwnerID(ctx))
	providers := []locator.Provider{
		provider.Named("runaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			switch name {
			case "internal":
				return true, true, nil
			case "reportMode":
				return "scheduler", true, nil
			}
			return nil, false, nil
		}),
		provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil }),
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: cubeTarget, Input: input, Providers: providers})
	if err != nil {
		return 0, err
	}
	out, ok := value.(*cube.RunReportOutput)
	if !ok || out == nil {
		return 0, fmt.Errorf("run cube returned %T", value)
	}
	total := 0
	for _, row := range out.Data {
		if row != nil {
			total += row.RecordCount
		}
	}
	return total, nil
}
