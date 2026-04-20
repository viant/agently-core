package toolexec

import (
	"context"

	asynccfg "github.com/viant/agently-core/protocol/async"
	asyncnarrator "github.com/viant/agently-core/protocol/async/narrator"
)

type asyncManagerKey struct{}
type asyncNarratorRunnerKey struct{}

type AsyncManager interface {
	Register(ctx context.Context, input asynccfg.RegisterInput) *asynccfg.OperationRecord
	Update(ctx context.Context, input asynccfg.UpdateInput) (*asynccfg.OperationRecord, bool)
	Get(ctx context.Context, id string) (*asynccfg.OperationRecord, bool)
	BindToolCarrier(ctx context.Context, id, toolCallID, toolMessageID, toolName string) (*asynccfg.OperationRecord, bool)
	Subscribe(opIDs []string) <-chan asynccfg.ChangeEvent
	AwaitTerminal(ctx context.Context, opIDs []string) <-chan asynccfg.AggregatedResult
	ActiveWaitOps(ctx context.Context, convID, turnID string) []*asynccfg.OperationRecord
	FindActiveByRequest(ctx context.Context, convID, turnID, toolName, requestArgsDigest string) (*asynccfg.OperationRecord, bool)
	TerminalFailure(ctx context.Context, convID, turnID string) (*asynccfg.OperationRecord, bool)
	RecordPollFailure(ctx context.Context, id, errMsg string, transient bool) (*asynccfg.OperationRecord, bool)
	ResetPollFailures(ctx context.Context, id string) (*asynccfg.OperationRecord, bool)
	WaitForNextPoll(ctx context.Context, convID, turnID string) error
	TryStartPoller(ctx context.Context, id string) bool
	FinishPoller(ctx context.Context, id string)
	// StorePollerCancel associates a cancel function with an operation id so that
	// CancelTurnPollers can stop the poller from outside the goroutine.
	StorePollerCancel(ctx context.Context, id string, cancel context.CancelFunc)
	// CancelTurnPollers cancels all autonomous pollers belonging to the given turn.
	// Called by the service layer when the turn is explicitly canceled.
	CancelTurnPollers(ctx context.Context, convID, turnID string)
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

func WithAsyncNarratorRunner(ctx context.Context, runner asyncnarrator.LLMRunner) context.Context {
	if runner == nil {
		return ctx
	}
	return context.WithValue(ctx, asyncNarratorRunnerKey{}, runner)
}

func AsyncNarratorRunnerFromContext(ctx context.Context) (asyncnarrator.LLMRunner, bool) {
	if ctx == nil {
		return nil, false
	}
	runner, ok := ctx.Value(asyncNarratorRunnerKey{}).(asyncnarrator.LLMRunner)
	return runner, ok && runner != nil
}
