package fs

import (
	"github.com/viant/agently-core/workspace"
	meta "github.com/viant/agently-core/workspace/service/meta"
)

type Option[T any] func(s *Service[T])

func WithMetaService[T any](meta *meta.Service) Option[T] {
	return func(s *Service[T]) {
		s.metaService = meta
	}
}

// WithStore configures the loader to read resources from a workspace.Store
// instead of the filesystem. When set, Load reads bytes via
// store.Load(ctx, kind, name) then runs the decoder.
func WithStore[T any](store workspace.Store, kind string) Option[T] {
	return func(s *Service[T]) {
		s.store = store
		s.storeKind = kind
	}
}
