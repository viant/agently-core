package executil

import (
	"context"

	asynccfg "github.com/viant/agently-core/protocol/async"
)

type asyncManagerKey struct{}

type AsyncManager interface {
	Register(ctx context.Context, input asynccfg.RegisterInput) *asynccfg.OperationRecord
	Update(ctx context.Context, input asynccfg.UpdateInput) (*asynccfg.OperationRecord, bool)
	Get(ctx context.Context, id string) (*asynccfg.OperationRecord, bool)
	TerminalFailure(ctx context.Context, convID, turnID string) (*asynccfg.OperationRecord, bool)
	RecordPollFailure(ctx context.Context, id, errMsg string, transient bool) (*asynccfg.OperationRecord, bool)
	ResetPollFailures(ctx context.Context, id string) (*asynccfg.OperationRecord, bool)
}

func WithAsyncManager(ctx context.Context, manager AsyncManager) context.Context {
	if manager == nil {
		return ctx
	}
	return context.WithValue(ctx, asyncManagerKey{}, manager)
}

func AsyncManagerFromContext(ctx context.Context) (AsyncManager, bool) {
	if ctx == nil {
		return nil, false
	}
	manager, ok := ctx.Value(asyncManagerKey{}).(AsyncManager)
	return manager, ok && manager != nil
}
