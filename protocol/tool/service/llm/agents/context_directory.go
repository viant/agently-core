package agents

import "context"

// WithContextDirectoryProvider permits a fresh caller-scoped published
// directory. Lookup failures are returned and never replaced by a static list.
func WithContextDirectoryProvider(load func(context.Context) ([]ListItem, error)) Option {
	return func(s *Service) { s.contextDirectory = load }
}
