// Package callback is the workspace repository for callback definitions.
// Files live under `<workspace>/callbacks/<eventName>.yaml`, one file per
// event. The repository is a thin wrapper around base.Repository with a
// LoadAll convenience that returns every callback sorted by id.
package callback

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/viant/afs"
	callbackdef "github.com/viant/agently-core/protocol/callback"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/agently-core/workspace/repository/base"
)

type Repository struct {
	*base.Repository[callbackdef.Callback]
}

// New builds a repository reading from the workspace's filesystem layer.
func New(fs afs.Service) *Repository {
	return &Repository{Repository: base.New[callbackdef.Callback](fs, workspace.KindCallback)}
}

// NewWithStore builds a repository reading from the workspace store abstraction.
func NewWithStore(store workspace.Store) *Repository {
	return &Repository{Repository: base.NewWithStore[callbackdef.Callback](store, workspace.KindCallback)}
}

// LoadAll returns every callback definition found under callbacks/, sorted
// by id. Callbacks that fail Validate are skipped with their names logged
// in the returned error chain rather than aborting the whole load.
func (r *Repository) LoadAll(ctx context.Context) ([]*callbackdef.Callback, error) {
	if r == nil || r.Repository == nil {
		return nil, fmt.Errorf("callback repository not configured")
	}
	names, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, nil
	}
	sort.Strings(names)
	out := make([]*callbackdef.Callback, 0, len(names))
	for _, name := range names {
		cb, err := r.Load(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("callback %q: %w", name, err)
		}
		if cb == nil {
			continue
		}
		if cb.ID == "" {
			// Default id to filename stem when the YAML omits it. Keeps
			// single-file setups terser.
			cb.ID = name
		}
		if err := cb.Validate(); err != nil {
			return nil, fmt.Errorf("callback %q invalid: %w", name, err)
		}
		out = append(out, cb)
	}
	return out, nil
}

// GetByEvent returns the callback whose id matches the provided eventName
// (case-insensitive). Returns nil without error when no match exists so
// callers can return a clean 404.
func (r *Repository) GetByEvent(ctx context.Context, eventName string) (*callbackdef.Callback, error) {
	target := strings.ToLower(strings.TrimSpace(eventName))
	if target == "" {
		return nil, fmt.Errorf("eventName is required")
	}
	all, err := r.LoadAll(ctx)
	if err != nil {
		return nil, err
	}
	for _, cb := range all {
		if cb != nil && strings.ToLower(strings.TrimSpace(cb.ID)) == target {
			return cb, nil
		}
	}
	return nil, nil
}
