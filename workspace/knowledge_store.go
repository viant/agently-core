package workspace

import "context"

// KnowledgeStore abstracts paths/URIs for embedding indexes, vector storage,
// and snapshot caches. FS implementations return local paths; a DB
// implementation could return dsn:// URIs.
type KnowledgeStore interface {
	// IndexBasePath returns the base path/URI for a user's embedding index.
	IndexBasePath(ctx context.Context, user string) (string, error)

	// SnapshotBasePath returns the base path/URI for MCP snapshot caches.
	SnapshotBasePath(ctx context.Context) (string, error)

	// DBPath returns the application database path.
	DBPath(ctx context.Context) (string, error)
}
