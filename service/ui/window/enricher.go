package window

import (
	"context"
	"sync"

	forgeTypes "github.com/viant/forge/backend/types"
)

// WorkspaceWindowEnricher lets the host application attach optional generic
// workspace extensions after the base Forge window, dialogs, and data sources
// have been resolved. Agently Core deliberately does not own extension schemas.
type WorkspaceWindowEnricher func(context.Context, *forgeTypes.Window) error

var workspaceWindowEnricherState struct {
	sync.RWMutex
	value WorkspaceWindowEnricher
}

// SetWorkspaceWindowEnricher installs the process-level workspace extension
// hook and returns a cleanup function suitable for tests and server shutdown.
func SetWorkspaceWindowEnricher(enricher WorkspaceWindowEnricher) func() {
	workspaceWindowEnricherState.Lock()
	workspaceWindowEnricherState.value = enricher
	workspaceWindowEnricherState.Unlock()
	return func() {
		workspaceWindowEnricherState.Lock()
		workspaceWindowEnricherState.value = nil
		workspaceWindowEnricherState.Unlock()
	}
}

func enrichWorkspaceWindow(ctx context.Context, window *forgeTypes.Window) error {
	workspaceWindowEnricherState.RLock()
	enricher := workspaceWindowEnricherState.value
	workspaceWindowEnricherState.RUnlock()
	if enricher == nil || window == nil {
		return nil
	}
	return enricher(ctx, window)
}
