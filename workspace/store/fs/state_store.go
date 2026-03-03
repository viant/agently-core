package fs

import (
	"context"
	"os"
	"path/filepath"

	"github.com/viant/agently-core/workspace"
)

// StateStore is a filesystem-backed implementation of workspace.StateStore.
type StateStore struct {
	stateRoot string
}

// NewStateStore creates an FS-backed StateStore. If stateRoot is empty it
// defaults to workspace.StateRoot().
func NewStateStore(stateRoot string) *StateStore {
	if stateRoot == "" {
		stateRoot = workspace.StateRoot()
	}
	return &StateStore{stateRoot: stateRoot}
}

// StatePath returns the resolved state directory for a scope.
func (s *StateStore) StatePath(_ context.Context, scope string) (string, error) {
	dir := filepath.Join(s.stateRoot, scope)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// StateRoot returns the root state directory.
func (s *StateStore) StateRoot(_ context.Context) (string, error) {
	return s.stateRoot, nil
}
