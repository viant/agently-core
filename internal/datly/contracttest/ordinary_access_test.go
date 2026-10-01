package tests

import (
	"context"
	"reflect"

	"github.com/viant/bindly/locator"
	"github.com/viant/datly/runtime/handler/provider"
)

// ordinaryAccess keeps baseline reader fixtures outside the dedicated locking
// path. Internal visibility alone does not authorize transaction row locks.
func ordinaryAccess(kind string, resolve func(context.Context, reflect.Type, string) (any, bool, error)) locator.Provider {
	return provider.Named(kind, func(ctx context.Context, target reflect.Type, name string) (any, bool, error) {
		if name == "lock" {
			return false, true, nil
		}
		return resolve(ctx, target, name)
	})
}
