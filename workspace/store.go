package workspace

import (
	"context"
	"time"
)

// Entry holds metadata about a workspace resource, used by polling watchers.
type Entry struct {
	Kind      string
	Name      string
	Data      []byte
	UpdatedAt time.Time
}

// Store is the workspace resource persistence abstraction.
// Implementations may store data on the local filesystem, in a database, or
// any other backend. Callers handle marshal/unmarshal — the Store works with
// raw []byte payloads.
type Store interface {
	// Root returns a canonical workspace identifier (FS path, DB URI, etc.).
	Root() string

	// List returns the names of all resources of the given kind.
	List(ctx context.Context, kind string) ([]string, error)

	// Load returns the raw bytes for a single resource.
	Load(ctx context.Context, kind, name string) ([]byte, error)

	// Save creates or overwrites a resource.
	Save(ctx context.Context, kind, name string, data []byte) error

	// Delete removes a resource. It is not an error if the resource does not exist.
	Delete(ctx context.Context, kind, name string) error

	// Exists reports whether a resource exists.
	Exists(ctx context.Context, kind, name string) (bool, error)

	// Entries returns metadata-enriched listings (for polling watchers).
	Entries(ctx context.Context, kind string) ([]Entry, error)
}
