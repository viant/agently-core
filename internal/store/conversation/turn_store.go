package conversation

import (
	"context"
	"fmt"
	"reflect"

	"github.com/viant/agently-core/internal/datly/queryselectors"
	read "github.com/viant/agently-core/internal/datly/turn/read"
	write "github.com/viant/agently-core/internal/datly/turn/write"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"github.com/viant/xdatly/state"
)

type TurnStore struct{ Invoker dexec.ComponentInvoker }

var turnWriterTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/turn"},
}
var turnRowsTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[read.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/api/agently/turn/list/list"},
}

func (s *TurnStore) ListRows(ctx context.Context, input *read.TurnRowsInput, selectors state.Selectors) ([]*read.TurnRowsView, error) {
	if s == nil || s.Invoker == nil {
		return nil, fmt.Errorf("turn component store is not configured")
	}
	if input == nil {
		input = &read.TurnRowsInput{}
	}
	providers := []locator.Provider{provider.Named("turnaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		if name == "mode" {
			return "rows", true, nil
		}
		return nil, false, nil
	})}
	if len(selectors) > 0 {
		providers = append(providers, queryselectors.ProviderMapped(selectors, map[string]string{"TurnRows": "reader", "turn_rows": "reader"}))
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: turnRowsTarget, Input: input, Providers: providers})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*read.TurnRowsOutput)
	if !ok || out == nil {
		return nil, fmt.Errorf("turn rows reader returned %T", value)
	}
	return out.Data, nil
}

func (s *TurnStore) PatchTrusted(ctx context.Context, row *write.Turn) error {
	_, err := s.PatchTrustedResult(ctx, row)
	return err
}

// PatchTrustedResult returns successful generated mutation rows, including lifecycle
// defaults and presence, for callers that expose mutable input objects.
func (s *TurnStore) PatchTrustedResult(ctx context.Context, row *write.Turn) (*write.Output, error) {
	if s == nil || s.Invoker == nil {
		return nil, fmt.Errorf("turn component store is not configured")
	}
	if row == nil {
		return nil, fmt.Errorf("turn mutation is required")
	}
	input := &write.Input{}
	input.SetTurns([]*write.Turn{row})
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: turnWriterTarget, Input: input})
	if err != nil {
		return nil, err
	}
	output, ok := value.(*write.Output)
	if !ok || output == nil {
		return nil, fmt.Errorf("turn writer returned %T", value)
	}
	return output, nil
}
