// Package queryselectors forwards native selector capabilities to a component.
// It does not parse SQL, bind component input, or implement reader execution.
package queryselectors

import (
	"context"
	"fmt"

	"github.com/viant/bindly/locator"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/xdatly/handler"
	"github.com/viant/xdatly/state"
)

// Provider captures a selector collection for explicit invocation forwarding.
// Each invocation receives its own native clone; Datly owns selector validation.
func Provider(selectors state.Selectors) locator.Provider {
	captured := selectors.Clone()
	return provider.New(handler.SelectorsKey, func(ctx context.Context) (any, bool, error) {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		value := captured.Clone()
		return value, value != nil, nil
	})
}

// ProviderMapped forwards explicit view-name migrations alongside native values.
// Unknown names are retained so the target reader can reject them; no scope is
// guessed from an SQL alias or a field name.
func ProviderMapped(selectors state.Selectors, views map[string]string) locator.Provider {
	forwarded := selectors.Clone()
	for _, selector := range forwarded {
		if selector == nil {
			continue
		}
		if target, ok := views[selector.Name]; ok {
			selector.Name = target
		}
	}
	return Provider(forwarded)
}

// Forward copies a caller's selector capability into a child invocation provider.
// An absent capability stays absent and cannot replace the target's own bindings.
func Forward(ctx context.Context, binder handler.Binder) (locator.Provider, error) {
	if binder == nil {
		return nil, nil
	}
	value, present, err := binder.Lookup(ctx, handler.SelectorsKey)
	if err != nil || !present || value == nil {
		return nil, err
	}
	selectors, ok := value.(state.Selectors)
	if !ok {
		return nil, fmt.Errorf("query selector proxy requires state.Selectors, got %T", value)
	}
	return Provider(selectors), nil
}
