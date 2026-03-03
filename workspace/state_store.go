package workspace

import "context"

// StateStore abstracts per-machine state (auth tokens, cookies, etc.).
// State is always local and is not shared across machines.
type StateStore interface {
	// StatePath returns the resolved state directory for a scope
	// (e.g. "mcp/<server>/<user>").
	StatePath(ctx context.Context, scope string) (string, error)

	// StateRoot returns the root state directory.
	StateRoot(ctx context.Context) (string, error)
}
