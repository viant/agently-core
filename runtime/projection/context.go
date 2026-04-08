package projection

import (
	"context"
	"strings"
	"sync"
)

// ContextProjection carries model-facing prompt projection decisions for the
// current request. It does not mutate transcript truth; it only records what
// should be hidden from the active prompt build.
type ContextProjection struct {
	// Scope mirrors Agent.Tool.CallExposure ("turn" | "conversation").
	Scope string
	// HiddenTurnIDs lists turns hidden from active prompt history.
	HiddenTurnIDs []string
	// HiddenMessageIDs lists message-level prompt suppressions, including
	// superseded tool-call results and turn-expanded hides.
	HiddenMessageIDs []string
	// Reason is a short human-readable explanation for observability.
	Reason string
	// TokensFreed is an approximate count of tokens removed by projection.
	TokensFreed int
}

// ProjectionState is a mutable request-scoped holder for ContextProjection.
// Callers should update it during decision phases and consume a Snapshot()
// during prompt-history construction.
type ProjectionState struct {
	mu    sync.RWMutex
	value ContextProjection
}

type stateKey struct{}

// WithState ensures a mutable ProjectionState exists in context.
func WithState(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := StateFromContext(ctx); ok {
		return ctx
	}
	return context.WithValue(ctx, stateKey{}, &ProjectionState{})
}

// StateFromContext returns the mutable state holder when present.
func StateFromContext(ctx context.Context) (*ProjectionState, bool) {
	if ctx == nil {
		return nil, false
	}
	state, ok := ctx.Value(stateKey{}).(*ProjectionState)
	return state, ok && state != nil
}

// SnapshotFromContext returns a copy of the current projection value.
func SnapshotFromContext(ctx context.Context) (ContextProjection, bool) {
	state, ok := StateFromContext(ctx)
	if !ok {
		return ContextProjection{}, false
	}
	return state.Snapshot(), true
}

// Snapshot returns a stable copy of the current projection value.
func (s *ProjectionState) Snapshot() ContextProjection {
	if s == nil {
		return ContextProjection{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneProjection(s.value)
}

// SetScope sets the projection scope.
func (s *ProjectionState) SetScope(scope string) {
	if s == nil {
		return
	}
	scope = strings.TrimSpace(scope)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value.Scope = scope
}

// HideTurns adds turn IDs to the hidden set, preserving insertion order.
func (s *ProjectionState) HideTurns(turnIDs ...string) {
	if s == nil || len(turnIDs) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value.HiddenTurnIDs = appendUniqueStrings(s.value.HiddenTurnIDs, turnIDs...)
}

// HideMessages adds message IDs to the hidden set, preserving insertion order.
func (s *ProjectionState) HideMessages(messageIDs ...string) {
	if s == nil || len(messageIDs) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value.HiddenMessageIDs = appendUniqueStrings(s.value.HiddenMessageIDs, messageIDs...)
}

// SetReason replaces the reason string when non-empty.
func (s *ProjectionState) SetReason(reason string) {
	if s == nil {
		return
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value.Reason = reason
}

// AddReason appends a distinct reason segment for observability.
func (s *ProjectionState) AddReason(reason string) {
	if s == nil {
		return
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch existing := strings.TrimSpace(s.value.Reason); {
	case existing == "":
		s.value.Reason = reason
	case strings.EqualFold(existing, reason):
		return
	case strings.Contains(existing, reason):
		return
	default:
		s.value.Reason = existing + "; " + reason
	}
}

// AddTokensFreed accumulates projected token savings.
func (s *ProjectionState) AddTokensFreed(tokens int) {
	if s == nil || tokens == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value.TokensFreed += tokens
}

func cloneProjection(in ContextProjection) ContextProjection {
	out := in
	if len(in.HiddenTurnIDs) > 0 {
		out.HiddenTurnIDs = append([]string(nil), in.HiddenTurnIDs...)
	}
	if len(in.HiddenMessageIDs) > 0 {
		out.HiddenMessageIDs = append([]string(nil), in.HiddenMessageIDs...)
	}
	return out
}

func appendUniqueStrings(dst []string, values ...string) []string {
	if len(values) == 0 {
		return dst
	}
	seen := make(map[string]struct{}, len(dst))
	for _, existing := range dst {
		key := strings.TrimSpace(existing)
		if key == "" {
			continue
		}
		seen[key] = struct{}{}
	}
	for _, raw := range values {
		key := strings.TrimSpace(raw)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		dst = append(dst, key)
	}
	return dst
}
