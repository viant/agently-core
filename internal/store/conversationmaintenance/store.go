package conversationmaintenance

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	read "github.com/viant/agently-core/internal/datly/conversation/read"
	tree "github.com/viant/agently-core/internal/store/conversationtree"

	"github.com/viant/agently-core/internal/datly/dbtime"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
)

type CandidateRequest struct {
	Kind                          string
	InactiveBefore, AfterActivity time.Time
	AfterRootID                   string
	Limit                         int
}
type Candidate struct {
	RootID, ExpectedOwnerID string
	ActivityAt              time.Time
}
type Store struct {
	Invoker dexec.ComponentInvoker
	Schema  tree.TableInspector
}

var readerTarget = dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[read.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/conversation/{id}"}}
var maintainTarget = dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[Component]().PkgPath(), Name: "ConversationMaintenance"}, Route: spec.RouteRef{Method: "POST", Path: "/v1/internal/agently/conversation/maintenance"}}

func (s *Store) schema(ctx context.Context) (tree.TableInspector, error) {
	if s.Schema != nil {
		return s.Schema, nil
	}
	driver, _ := s.Invoker.(tree.DriverInspector)
	return tree.MaintenanceSchema(ctx, driver)
}
func (s *Store) Candidates(ctx context.Context, request CandidateRequest) ([]Candidate, error) {
	if s == nil || s.Invoker == nil {
		return nil, fmt.Errorf("conversation maintenance runtime is required")
	}
	if (request.Kind != Interactive && request.Kind != Scheduled && request.Kind != Fallback) || request.InactiveBefore.IsZero() || request.Limit <= 0 || request.AfterActivity.IsZero() != (strings.TrimSpace(request.AfterRootID) == "") {
		return nil, fmt.Errorf("invalid conversation maintenance candidate request")
	}
	schema, err := s.schema(ctx)
	if err != nil {
		return nil, err
	}
	legacy, err := schema.HasTable(ctx, "agently", "schedule_run")
	if err != nil {
		return nil, err
	}
	input := &read.ConversationInput{}
	input.SetMaintenanceBefore(request.InactiveBefore.UTC())
	input.SetMaintenanceBeforeSecond(request.InactiveBefore.UTC().Format("2006-01-02 15:04:05"))
	input.SetMaintenanceBeforeNano(int64(request.InactiveBefore.Nanosecond()))
	if request.AfterRootID != "" {
		input.SetMaintenanceAfterActivity(request.AfterActivity.UTC().Format("2006-01-02 15:04:05"))
		input.SetMaintenanceAfterNano(int64(request.AfterActivity.Nanosecond()))
		input.SetMaintenanceAfterRootId(strings.TrimSpace(request.AfterRootID))
	}
	input.SetFields([]string{"id", "created_by_user_id", "activity_raw"})
	input.SetLimit(request.Limit)
	owner := ""
	providers := []locator.Provider{provider.Named("conversationaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		switch name {
		case "maintenance":
			return true, true, nil
		case "kind":
			return request.Kind, true, nil
		case "legacyRuns":
			return legacy, true, nil
		case "list", "ascending", "enforceVisibility", "lock":
			return false, true, nil
		case "graph":
			return true, true, nil
		}
		return nil, false, nil
	}), provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil })}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: readerTarget, Input: input, Providers: providers})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*read.ConversationOutput)
	if !ok || out == nil {
		return nil, fmt.Errorf("conversation maintenance reader returned %T", value)
	}
	result := make([]Candidate, 0, len(out.Data))
	for _, row := range out.Data {
		if row == nil {
			continue
		}
		raw := valueString(row.ActivityRaw)
		activity, ok := dbtime.ParseActivity(raw)
		if !ok {
			return nil, fmt.Errorf("maintenance candidate %q has invalid activity time %q", row.Id, raw)
		}
		result = append(result, Candidate{RootID: strings.TrimSpace(row.Id), ExpectedOwnerID: valueString(row.CreatedByUserId), ActivityAt: activity})
	}
	return result, nil
}
func (s *Store) Maintain(ctx context.Context, input *Input) (*Output, error) {
	if s == nil || s.Invoker == nil || input == nil {
		return nil, fmt.Errorf("conversation maintenance runtime and input are required")
	}
	schema, err := s.schema(ctx)
	if err != nil {
		return nil, err
	}
	providers := []locator.Provider{provider.Named("conversationtreeSchema", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		if name == "inspector" {
			return schema, true, nil
		}
		return nil, false, nil
	})}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: maintainTarget, Input: input, Providers: providers})
	if err != nil {
		var decision *skipped
		if errors.As(err, &decision) {
			return decision.result, nil
		}
		return nil, err
	}
	out, ok := value.(*Output)
	if !ok || out == nil {
		return nil, fmt.Errorf("conversation maintenance returned %T", value)
	}
	return out, nil
}
func valueString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
