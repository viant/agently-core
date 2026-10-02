package conversation

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/viant/agently-core/internal/datly/queryselectors"
	cube "github.com/viant/agently-core/internal/datly/toolapprovalqueue/cube"
	read "github.com/viant/agently-core/internal/datly/toolapprovalqueue/read"
	write "github.com/viant/agently-core/internal/datly/toolapprovalqueue/write"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"github.com/viant/xdatly/state"
)

type ApprovalStore struct {
	Invoker dexec.ComponentInvoker
	OwnerID func(context.Context) string
}

var approvalReaderTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[read.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/tool-approval"},
}
var approvalCubeTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[cube.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/tool-approval/report"},
}
var approvalWriterTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/toolapprovalqueue"},
}

func (s *ApprovalStore) providers(ctx context.Context, mode string) ([]locator.Provider, error) {
	if s == nil || s.Invoker == nil || s.OwnerID == nil {
		return nil, fmt.Errorf("approval component store is not configured")
	}
	owner := strings.TrimSpace(s.OwnerID(ctx))
	return []locator.Provider{
		provider.Named("approvalaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			switch name {
			case "internal":
				return true, true, nil
			case "mode":
				return mode, true, nil
			}
			return nil, false, nil
		}),
		provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil }),
	}, nil
}

func (s *ApprovalStore) List(ctx context.Context, mode string, input *read.ApprovalRowsInput, selectors state.Selectors) ([]*read.ApprovalView, error) {
	if mode != "rows" && mode != "outcome" {
		return nil, fmt.Errorf("unsupported approval read mode %q", mode)
	}
	providers, err := s.providers(ctx, mode)
	if err != nil {
		return nil, err
	}
	if len(selectors) > 0 {
		providers = append(providers, queryselectors.ProviderMapped(selectors, map[string]string{"queue_rows": "reader"}))
	}
	if input == nil {
		input = &read.ApprovalRowsInput{}
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: approvalReaderTarget, Input: input, Providers: providers})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*read.ApprovalRowsOutput)
	if !ok || out == nil {
		return nil, fmt.Errorf("approval reader returned %T", value)
	}
	return out.Data, nil
}

func (s *ApprovalStore) Count(ctx context.Context, input *cube.ApprovalReportInput) (int, error) {
	providers, err := s.providers(ctx, "")
	if err != nil {
		return 0, err
	}
	if input == nil {
		input = &cube.ApprovalReportInput{}
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: approvalCubeTarget, Input: input, Providers: providers})
	if err != nil {
		return 0, err
	}
	out, ok := value.(*cube.ApprovalReportOutput)
	if !ok || out == nil {
		return 0, fmt.Errorf("approval cube returned %T", value)
	}
	total := 0
	for _, row := range out.Data {
		if row != nil {
			total += row.TotalCount
		}
	}
	return total, nil
}

func (s *ApprovalStore) PatchTrusted(ctx context.Context, row *write.ToolApprovalQueue) error {
	_, err := s.PatchTrustedResult(ctx, row)
	return err
}

// PatchTrustedResult returns successful generated mutation rows, including lifecycle
// defaults and presence, for callers that expose mutable input objects.
func (s *ApprovalStore) PatchTrustedResult(ctx context.Context, row *write.ToolApprovalQueue) (*write.Output, error) {
	if s == nil || s.Invoker == nil {
		return nil, fmt.Errorf("approval component store is not configured")
	}
	if row == nil {
		return nil, fmt.Errorf("approval mutation is required")
	}
	input := &write.Input{}
	input.SetQueues([]*write.ToolApprovalQueue{row})
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: approvalWriterTarget, Input: input})
	if err != nil {
		return nil, err
	}
	output, ok := value.(*write.Output)
	if !ok || output == nil {
		return nil, fmt.Errorf("approval writer returned %T", value)
	}
	return output, nil
}

// DeleteTrusted submits one generated batch so a late failure rolls back
// earlier approval deletes under the component's transaction policy.
func (s *ApprovalStore) DeleteTrusted(ctx context.Context, ids ...string) error {
	if s == nil || s.Invoker == nil {
		return fmt.Errorf("approval component store is not configured")
	}
	seen := map[string]bool{}
	rows := make([]*write.ToolApprovalQueue, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		row := &write.ToolApprovalQueue{}
		row.SetId(id)
		row.SetShouldDelete(true)
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil
	}
	input := &write.Input{}
	input.SetQueues(rows)
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: approvalWriterTarget, Input: input})
	if err != nil {
		return err
	}
	if _, ok := value.(*write.Output); !ok {
		return fmt.Errorf("approval delete writer returned %T", value)
	}
	return nil
}
