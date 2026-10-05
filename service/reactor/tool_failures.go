package reactor

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/viant/agently-core/runtime/recovery"
	"github.com/viant/agently-core/service/shared/toolexec"
)

type toolFailuresKey struct{}
type toolFailures struct {
	mu        sync.Mutex
	errs      []error
	proactive bool
}

func withToolFailures(ctx context.Context) (context.Context, *toolFailures) {
	failures := &toolFailures{proactive: recovery.IsProactive(ctx)}
	return context.WithValue(ctx, toolFailuresKey{}, failures), failures
}
func (f *toolFailures) record(name, id string, err error) bool {
	if err == nil {
		return false
	}
	var infrastructure *toolexec.InfrastructureError
	// Ordinary tool errors can be returned to the model. Proactive removal is
	// a prerequisite for the fresh full-history generation, so every failure
	// must stop that continuation rather than appear to be a completed pass.
	if !f.proactive && !errors.As(err, &infrastructure) {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs = append(f.errs, fmt.Errorf("tool %s (%s): %w", name, id, err))
	return true
}
func (f *toolFailures) err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return errors.Join(f.errs...)
}
