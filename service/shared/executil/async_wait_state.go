package executil

import (
	"context"
	"strings"
	"sync"
)

type asyncWaitStateKey struct{}

type asyncWaitState struct {
	mu  sync.Mutex
	ops []string
}

func WithAsyncWaitState(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	if _, ok := ctx.Value(asyncWaitStateKey{}).(*asyncWaitState); ok {
		return ctx
	}
	return context.WithValue(ctx, asyncWaitStateKey{}, &asyncWaitState{})
}

func MarkAsyncWaitAfterStatus(ctx context.Context, opID string) {
	if ctx == nil {
		return
	}
	state, _ := ctx.Value(asyncWaitStateKey{}).(*asyncWaitState)
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

func ConsumeAsyncWaitAfterStatus(ctx context.Context) []string {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(asyncWaitStateKey{}).(*asyncWaitState)
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
