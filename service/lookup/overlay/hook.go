package overlay

// NewElicitationHook returns a refiner.OverlayHook-compatible closure for a
// fixed render context. Useful when the elicitation dispatcher knows the
// render context ahead of time.
//
// Kept in the overlay package (rather than refiner) so that refiner stays
// dependency-free.
func (s *Service) NewElicitationHook(contextKind, contextID string) func(map[string]interface{}) {
	return func(props map[string]interface{}) {
		s.Apply(contextKind, contextID, props)
	}
}

// NewWildcardHook returns a refiner hook that applies every overlay whose
// Target admits *any* context (including Target.Kind filters — those are
// bypassed via the "*" wildcard; see matcher.go). This is the default
// wiring used when the refiner has no way to know the render context per
// request — overlays with a specific Target.Kind still fire, and library
// overlays with no Target still apply schema-wide.
//
// Authors who want strictly kind-scoped matching (e.g. "only fire this
// overlay when rendering a template") should invoke Apply themselves with
// an explicit contextKind rather than rely on the wildcard hook.
func (s *Service) NewWildcardHook() func(map[string]interface{}) {
	return func(props map[string]interface{}) {
		s.Apply("*", "*", props)
	}
}
