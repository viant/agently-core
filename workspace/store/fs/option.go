package fs

import "github.com/viant/afs"

// Option configures an FS store.
type Option func(*Store)

// WithAFS overrides the default afs.Service used for file operations.
func WithAFS(fs afs.Service) Option {
	return func(s *Store) {
		if fs != nil {
			s.fs = fs
		}
	}
}
