package asyncwait

import (
	"context"
	"strings"
	"sync"
)

type stateKey struct{}

type State struct {
	mu  sync.Mutex
	ops []string
}

func WithState(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	if _, ok := ctx.Value(stateKey{}).(*State); ok {
		return ctx
	}
	return context.WithValue(ctx, stateKey{}, &State{})
}

func MarkAfterStatus(ctx context.Context, opID string) {
	if ctx == nil {
		return
	}
	state, _ := ctx.Value(stateKey{}).(*State)
	if state == nil {
		return
	}
	opID = strings.TrimSpace(opID)
	if opID == "" {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	for _, existing := range state.ops {
		if strings.EqualFold(existing, opID) {
			return
		}
	}
	state.ops = append(state.ops, opID)
}

func ConsumeAfterStatus(ctx context.Context) []string {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(stateKey{}).(*State)
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.ops) == 0 {
		return nil
	}
	result := append([]string(nil), state.ops...)
	state.ops = nil
	return result
}
