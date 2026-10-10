package dispatchpayload

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// DispatchPayloadResolver runs only at an authorized server dispatch boundary.
// sanitize must remove payload echoes before results enter logs or history.
type DispatchPayloadResolver func(context.Context, string, map[string]interface{}) (map[string]interface{}, func(string) string, error)
type dispatchPayloadKey struct{}

func WithDispatchPayloadResolver(ctx context.Context, resolver DispatchPayloadResolver) context.Context {
	if state, ok := ctx.Value(stateKey{}).(*State); ok {
		state.Lock()
		state.resolver = resolver
		state.Unlock()
	}
	return context.WithValue(ctx, dispatchPayloadKey{}, resolver)
}
func ResolveDispatchPayload(ctx context.Context, name string, args map[string]interface{}) (map[string]interface{}, func(string) string, error) {
	identity := func(s string) string { return s }
	if !HasArtifactReference(args) && !Active(ctx) {
		return args, identity, nil
	}
	resolver, _ := ctx.Value(dispatchPayloadKey{}).(DispatchPayloadResolver)
	if state, ok := ctx.Value(stateKey{}).(*State); ok {
		state.Lock()
		if state.resolver != nil {
			resolver = state.resolver
		}
		state.Unlock()
	}
	if resolver == nil {
		return nil, identity, fmt.Errorf("artifact payload resolver unavailable at server dispatch")
	}
	return resolver(ctx, name, args)
}

// HasArtifactReference scans existing JSON arguments without cloning/serialization.
func HasArtifactReference(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.Contains(typed, "${artifact[")
	case map[string]interface{}:
		for _, child := range typed {
			if HasArtifactReference(child) {
				return true
			}
		}
	case []interface{}:
		for _, child := range typed {
			if HasArtifactReference(child) {
				return true
			}
		}
	}
	return false
}

type stateKey struct{}
type State struct {
	sync.Mutex
	resolver DispatchPayloadResolver
	original map[string]interface{}
	sessions map[string]*sessionLease
	closed   bool
}

func WithState(ctx context.Context) (context.Context, func()) {
	if _, ok := ctx.Value(stateKey{}).(*State); ok {
		return ctx, func() {}
	}
	s := &State{sessions: map[string]*sessionLease{}}
	return context.WithValue(ctx, stateKey{}, s), func() { closeState(s) }
}
func Active(ctx context.Context) bool {
	s, _ := ctx.Value(stateKey{}).(*State)
	if s == nil {
		return false
	}
	s.Lock()
	defer s.Unlock()
	return !s.closed && s.resolver != nil
}
func Session(ctx context.Context, name string) (any, bool) {
	s, _ := ctx.Value(stateKey{}).(*State)
	if s == nil {
		return nil, false
	}
	s.Lock()
	defer s.Unlock()
	if s.closed {
		return nil, false
	}
	v, ok := s.sessions[name]
	if !ok {
		return nil, false
	}
	return v.value, true
}
func StoreSession(ctx context.Context, name string, session any, close func()) bool {
	s, _ := ctx.Value(stateKey{}).(*State)
	if s == nil {
		return false
	}
	s.Lock()
	defer s.Unlock()
	if s.closed {
		return false
	}
	s.sessions[name] = &sessionLease{value: session, close: close, owners: 1}
	return true
}

type sessionLease struct {
	sync.Mutex
	value  any
	close  func()
	owners int
}

func closeState(s *State) {
	s.Lock()
	defer s.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	for _, lease := range s.sessions {
		lease.Lock()
		lease.owners--
		if lease.owners == 0 {
			lease.close()
		}
		lease.Unlock()
	}
	s.sessions = nil
}

// CloneProvenance transfers only process-owned original references and a lease
// on the same isolated RPC session to an existing owned async lifecycle.
func CloneProvenance(src, dst context.Context) context.Context {
	s, _ := src.Value(stateKey{}).(*State)
	if s == nil {
		return dst
	}
	s.Lock()
	defer s.Unlock()
	if s.closed || s.resolver == nil {
		return dst
	}
	copy := &State{resolver: s.resolver, original: s.original, sessions: map[string]*sessionLease{}}
	for name, lease := range s.sessions {
		lease.Lock()
		lease.owners++
		lease.Unlock()
		copy.sessions[name] = lease
	}
	return context.WithValue(dst, stateKey{}, copy)
}
func CloseProvenance(ctx context.Context) {
	if s, ok := ctx.Value(stateKey{}).(*State); ok {
		closeState(s)
	}
}

func SetOriginalArguments(ctx context.Context, args map[string]interface{}) {
	s, _ := ctx.Value(stateKey{}).(*State)
	if s == nil {
		return
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return
	}
	var cloned map[string]interface{}
	if json.Unmarshal(raw, &cloned) != nil {
		return
	}
	s.Lock()
	s.original = cloned
	s.Unlock()
}
func OriginalArguments(ctx context.Context) map[string]interface{} {
	s, _ := ctx.Value(stateKey{}).(*State)
	if s == nil {
		return nil
	}
	s.Lock()
	defer s.Unlock()
	raw, _ := json.Marshal(s.original)
	var cloned map[string]interface{}
	_ = json.Unmarshal(raw, &cloned)
	return cloned
}
