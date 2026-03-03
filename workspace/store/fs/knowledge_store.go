package fs

import (
	"context"
	"os"
	"path/filepath"

	"github.com/viant/agently-core/workspace"
)

// KnowledgeStore is a filesystem-backed implementation of workspace.KnowledgeStore.
type KnowledgeStore struct {
	runtimeRoot string
}

// NewKnowledgeStore creates an FS-backed KnowledgeStore. If runtimeRoot is
// empty it defaults to workspace.RuntimeRoot().
func NewKnowledgeStore(runtimeRoot string) *KnowledgeStore {
	if runtimeRoot == "" {
		runtimeRoot = workspace.RuntimeRoot()
	}
	return &KnowledgeStore{runtimeRoot: runtimeRoot}
}

// IndexBasePath returns the base path for a user's embedding index.
func (s *KnowledgeStore) IndexBasePath(_ context.Context, user string) (string, error) {
	dir := filepath.Join(s.runtimeRoot, "index", user)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// SnapshotBasePath returns the base path for MCP snapshot caches.
func (s *KnowledgeStore) SnapshotBasePath(_ context.Context) (string, error) {
	return filepath.Join(s.runtimeRoot, "snapshots"), nil
}

// DBPath returns the application database path.
func (s *KnowledgeStore) DBPath(_ context.Context) (string, error) {
	return filepath.Join(s.runtimeRoot, "db", "agently.db"), nil
}
