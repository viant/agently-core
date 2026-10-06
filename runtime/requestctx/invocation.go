package requestctx

import (
	"context"
	"fmt"
	"github.com/viant/agently-core/runtime/clienttool"
)

// Invocation describes a real delegated Query call, not a conversation link.
// TurnID/ID identify one invocation even when its conversation is reused.
type Invocation struct {
	Detached             bool   `json:"detached,omitempty"`
	ExecutionMode        string `json:"executionMode,omitempty"`
	ID                   string `json:"id,omitempty"`
	ConversationID       string `json:"conversationId,omitempty"`
	TurnID               string `json:"turnId,omitempty"`
	Name                 string `json:"name,omitempty"`
	Description          string `json:"description,omitempty"`
	ParentConversationID string `json:"parentConversationId,omitempty"`
	ParentTurnID         string `json:"parentTurnId,omitempty"`
	ParentToolCallID     string `json:"parentToolCallId,omitempty"`
	ParentMessageID      string `json:"parentMessageId,omitempty"`
	ParentInvocationID   string `json:"parentInvocationId,omitempty"`
}
type InvocationResult struct {
	ClientToolCalls        []clienttool.PendingCall
	ClientToolDependencies []clienttool.Dependency
	InterruptIDs           []string
	Invocation             Invocation
	NativeStatus           string
	Content                string
	Error                  string
}
type InvocationObserver interface {
	BeforeInvocation(context.Context, Invocation) error
	InvocationReturned(context.Context, InvocationResult) error
}
type invocationObserverKey struct{}
type invocationIDKey struct{}

func WithInvocationObserver(ctx context.Context, observer InvocationObserver) context.Context {
	return context.WithValue(ctx, invocationObserverKey{}, observer)
}
func InvocationObserverFromContext(ctx context.Context) InvocationObserver {
	observer, _ := ctx.Value(invocationObserverKey{}).(InvocationObserver)
	return observer
}
func InvocationIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(invocationIDKey{}).(string)
	return id
}
func ObserveInvocation(ctx context.Context, invocation Invocation) (context.Context, error) {
	observer := InvocationObserverFromContext(ctx)
	if observer == nil {
		return ctx, nil
	}
	if invocation.ID == "" || invocation.TurnID == "" || invocation.ConversationID == "" || invocation.Name == "" || invocation.ParentConversationID == "" || invocation.ParentTurnID == "" {
		return ctx, fmt.Errorf("invocation observation requires child and causal parent identity")
	}
	if err := observer.BeforeInvocation(ctx, invocation); err != nil {
		return ctx, err
	}
	if provider, ok := observer.(interface {
		InvocationContext(context.Context, Invocation) context.Context
	}); ok {
		ctx = provider.InvocationContext(ctx, invocation)
	}
	return context.WithValue(ctx, invocationIDKey{}, invocation.ID), nil
}
func NotifyInvocationReturned(ctx context.Context, result InvocationResult) error {
	observer := InvocationObserverFromContext(ctx)
	if observer == nil {
		return nil
	}
	return observer.InvocationReturned(ctx, result)
}

type InvocationObserverFuncs struct {
	Before   func(context.Context, Invocation) error
	Returned func(context.Context, InvocationResult) error
}

func (f InvocationObserverFuncs) BeforeInvocation(ctx context.Context, invocation Invocation) error {
	if f.Before == nil {
		return nil
	}
	return f.Before(ctx, invocation)
}
func (f InvocationObserverFuncs) InvocationReturned(ctx context.Context, result InvocationResult) error {
	if f.Returned == nil {
		return nil
	}
	return f.Returned(ctx, result)
}
