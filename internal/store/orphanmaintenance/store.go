package orphanmaintenance

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	read "github.com/viant/agently-core/internal/datly/orphanmaintenance/read"
	tree "github.com/viant/agently-core/internal/store/conversationtree"

	"github.com/viant/agently-core/internal/datly/dbtime"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
)

type Store struct {
	Invoker dexec.ComponentInvoker
	Driver  tree.DriverInspector
}

var reportTarget = dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[read.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/orphan-maintenance/report"}}
var applyTarget = dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[ApplyComponent]().PkgPath(), Name: "OrphanMaintenanceApply"}, Route: spec.RouteRef{Method: "POST", Path: "/v1/internal/agently/orphan-maintenance/apply"}}

func (s *Store) configuredDriver() (tree.DriverInspector, error) {
	if s == nil || s.Invoker == nil {
		return nil, fmt.Errorf("orphan maintenance runtime is required")
	}
	inspector := s.Driver
	if inspector == nil {
		inspector, _ = s.Invoker.(tree.DriverInspector)
	}
	if inspector == nil {
		return nil, fmt.Errorf("orphan maintenance configured driver is required")
	}
	return inspector, nil
}
func mysqlContract(ctx context.Context, inspector tree.DriverInspector) (bool, error) {
	driver, err := inspector.ConfiguredDriver(ctx, "agently")
	if err != nil {
		return false, err
	}
	driver = strings.ToLower(strings.TrimSpace(driver))
	switch {
	case strings.Contains(driver, "mysql"):
		return true, nil
	case strings.Contains(driver, "sqlite"):
		return false, nil
	}
	return false, fmt.Errorf("unsupported deletion database driver %q", driver)
}
func reportProviders(mysql bool) []locator.Provider {
	return []locator.Provider{provider.Named("orphanaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		switch name {
		case "internal":
			return true, true, nil
		case "mysql":
			return mysql, true, nil
		}
		return nil, false, nil
	})}
}
func readReport(ctx context.Context, invoker dexec.ComponentInvoker, mysql bool, input *read.ReportInput) ([]*read.OrphanRow, error) {
	value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: reportTarget, Input: input, Providers: reportProviders(mysql)})
	if err != nil {
		return nil, err
	}
	output, ok := value.(*read.ReportOutput)
	if !ok || output == nil {
		return nil, fmt.Errorf("orphan report reader returned %T", value)
	}
	return output.Data, nil
}
func (s *Store) List(ctx context.Context, request CandidateRequest) ([]Candidate, error) {
	request.OlderThan = request.OlderThan.UTC()
	request.AfterCursor = strings.TrimSpace(request.AfterCursor)
	if request.OlderThan.IsZero() {
		return nil, fmt.Errorf("%w: orphan grace-period cutoff is required", ErrInvalidRequest)
	}
	if request.Limit <= 0 {
		return nil, fmt.Errorf("%w: orphan candidate limit must be positive", ErrInvalidRequest)
	}
	inspector, err := s.configuredDriver()
	if err != nil {
		return nil, err
	}
	mysql, err := mysqlContract(ctx, inspector)
	if err != nil {
		return nil, err
	}
	rules := Rules(mysql)
	priority, rule, record, err := DecodeCursor(request.AfterCursor, rules)
	if err != nil {
		return nil, err
	}
	input := &read.ReportInput{}
	input.SetOlderThan(request.OlderThan)
	input.SetLimit(request.Limit)
	input.SetAfterPriority(priority)
	input.SetAfterRule(rule)
	input.SetAfterRecord(record)
	input.SetHasCursor(request.AfterCursor != "")
	rows, err := readReport(ctx, s.Invoker, mysql, input)
	if err != nil {
		return nil, err
	}
	result := make([]Candidate, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		rule, exists := FindRule(mysql, row.RuleId)
		if !exists || rule.Action != Action(row.Action) || rule.Table != row.TableName || rule.Priority != row.Priority || rule.ReferenceTable != row.ReferenceTable {
			return nil, fmt.Errorf("orphan report returned an unknown rule contract %q", row.RuleId)
		}
		observed, valid := dbtime.ParseActivity(row.ObservedAtRaw)
		if !valid {
			return nil, fmt.Errorf("orphan candidate rule=%q record=%q has invalid observation time %q", rule.ID, row.RecordId, row.ObservedAtRaw)
		}
		result = append(result, Candidate{CursorID: EncodeCursor(rule, row.RecordId), RuleID: rule.ID, Action: rule.Action, Table: rule.Table, RecordID: row.RecordId, ReferenceTable: rule.ReferenceTable, ReferenceID: row.ReferenceId, ObservedAt: observed})
	}
	return result, nil
}
func (s *Store) Apply(ctx context.Context, request Request) (*Result, error) {
	request, err := normalizeRequest(request)
	if err != nil {
		return nil, err
	}
	inspector, err := s.configuredDriver()
	if err != nil {
		return nil, err
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: applyTarget, Input: &ApplyInput{RuleID: request.RuleID, RecordID: request.RecordID, OlderThan: request.OlderThan, Lease: request.Lease}, Providers: []locator.Provider{provider.Named("orphanmaintenance", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		if name == "driver" {
			return inspector, true, nil
		}
		return nil, false, nil
	})}})
	if err != nil {
		return nil, err
	}
	output, ok := value.(*ApplyOutput)
	if !ok || output == nil || output.Result == nil {
		return nil, fmt.Errorf("orphan maintenance returned %T", value)
	}
	return output.Result, nil
}
