package queryselectors

import (
	"context"
	dexec "github.com/viant/datly/exec"
)

// ForUpdateOptions retains caller cache/query scope settings and selects only
// this invocation's root reader. Discovery reads explicitly remain unlocked.
func ForUpdateOptions(ctx context.Context, enabled bool) *dexec.ReaderOptions {
	options := dexec.ReaderOptionsFromContext(ctx)
	options.ForUpdate = nil
	if enabled {
		options.ForUpdate = []string{dexec.RootView}
	}
	return &options
}
