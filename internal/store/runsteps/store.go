package runsteps

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/viant/agently-core/internal/datly/queryselectors"
	read "github.com/viant/agently-core/internal/datly/runsteps/read"
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
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/run/steps"},
}

// ListTrusted leaves the data facade's conversation and run authorization in
// place while the canonical reader owns union, predicates and projection.
func (s *Store) ListTrusted(ctx context.Context, input *read.RunStepsInput, selectors state.Selectors) ([]*read.RunStepsView, error) {
	if s == nil || s.Invoker == nil || s.OwnerID == nil {
		return nil, fmt.Errorf("run steps component store is not configured")
	}
	if input == nil {
		input = &read.RunStepsInput{}
	}
	owner := strings.TrimSpace(s.OwnerID(ctx))
	providers := []locator.Provider{
		provider.Named("runaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "internal" {
				return true, true, nil
			}
			return nil, false, nil
		}),
		provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil }),
	}
	if len(selectors) > 0 {
		providers = append(providers, queryselectors.ProviderMapped(selectors, map[string]string{"RunSteps": "reader", "run_steps": "reader"}))
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: readerTarget, Input: input, Providers: providers})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*read.RunStepsOutput)
	if !ok || out == nil {
		return nil, fmt.Errorf("run steps reader returned %T", value)
	}
	return out.Data, nil
}
