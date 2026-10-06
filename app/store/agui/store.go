package agui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"github.com/mattn/go-sqlite3"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
	"github.com/viant/sqlx/io/errx"
	xhandler "github.com/viant/xdatly/handler"
	"time"
)

type ComponentStore struct{ Invoker dexec.ComponentInvoker }

func New(invoker dexec.ComponentInvoker) *ComponentStore { return &ComponentStore{Invoker: invoker} }

var manageTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: "github.com/viant/agently-core/internal/datly/agui/manage", Name: "Manage"},
	Route:     spec.RouteRef{Method: "POST", Path: "/v1/internal/agently/ag-ui/manage"},
}

func (s *ComponentStore) invoke(ctx context.Context, request *Request) (*Response, error) {
	if s == nil || s.Invoker == nil {
		return nil, fmt.Errorf("AG-UI component invoker is required")
	}
	var value any
	var err error
	for attempt := 0; attempt < 8; attempt++ {
		var completion xhandler.Outcome
		completed := false
		value, err = s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: manageTarget, Input: request, Completion: func(outcome xhandler.Outcome) { completion = outcome.Clone(); completed = true }})
		if err == nil {
			break
		}
		// Only replay a failed, fully rolled-back owned transaction. Caller-pending,
		// unknown or mixed completion never proves that another attempt is safe.
		canRetry := completed && len(completion.Transactions) == 1 && completion.State() == xhandler.TransactionRolledBack
		duplicate := request.Operation == "admit" && errx.IsDuplicateKey(err)
		if attempt == 7 || !canRetry || (!transactionContention(err) && !duplicate) {
			var conflict *xhandler.Conflict
			if errors.As(err, &conflict) || duplicate {
				return nil, fmt.Errorf("%w: %v", ErrConflict, err)
			}
			return nil, err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	output, ok := value.(*Response)
	if !ok || output == nil {
		return nil, fmt.Errorf("AG-UI store returned %T", value)
	}
	return output, nil
}
func (s *ComponentStore) Admit(ctx context.Context, admission Admission) (*Run, bool, error) {
	out, err := s.invoke(ctx, &Request{Operation: "admit", Principal: admission.Principal, ThreadID: admission.ThreadID, RunID: admission.RunID, Admission: &admission})
	if err != nil {
		return nil, false, err
	}
	return out.Run, out.New, nil
}
func (s *ComponentStore) GetRun(ctx context.Context, principal, threadID, runID string) (*Run, error) {
	out, err := s.invoke(ctx, &Request{Operation: "get", Principal: principal, ThreadID: threadID, RunID: runID})
	if err != nil {
		return nil, err
	}
	return out.Run, nil
}
func (s *ComponentStore) Append(ctx context.Context, principal, threadID, runID string, expectedRevision int64, events []json.RawMessage, change *Change) (*Run, error) {
	out, err := s.invoke(ctx, &Request{Operation: "append", Principal: principal, ThreadID: threadID, RunID: runID, ExpectedRevision: expectedRevision, Events: events, Change: change})
	if err != nil {
		return nil, err
	}
	return out.Run, nil
}
func (s *ComponentStore) Replay(ctx context.Context, principal, threadID, runID string, after int64, limit int) ([]JournalEvent, error) {
	out, err := s.invoke(ctx, &Request{Operation: "replay", Principal: principal, ThreadID: threadID, RunID: runID, After: after, Limit: limit})
	if err != nil {
		return nil, err
	}
	return out.Events, nil
}
func (s *ComponentStore) GetThread(ctx context.Context, principal, threadID string) (*Thread, error) {
	out, err := s.invoke(ctx, &Request{Operation: "thread", Principal: principal, ThreadID: threadID})
	if err != nil {
		return nil, err
	}
	return out.Thread, nil
}

var _ Store = (*ComponentStore)(nil)

func (s *ComponentStore) ListPending(ctx context.Context, principal, threadID string) ([]*Run, error) {
	out, err := s.invoke(ctx, &Request{Operation: "pending", Principal: principal, ThreadID: threadID})
	if err != nil {
		return nil, err
	}
	return out.Runs, nil
}

func (s *ComponentStore) ListActive(ctx context.Context, principal, threadID string) ([]*Run, error) {
	out, err := s.invoke(ctx, &Request{Operation: "active", Principal: principal, ThreadID: threadID})
	if err != nil {
		return nil, err
	}
	return out.Runs, nil
}

var _ ActiveRunLister = (*ComponentStore)(nil)

// Driver-coded transaction contention is eligible for a bounded fresh native
// invocation. Error text alone is never sufficient to replay a mutation.
func transactionContention(err error) bool {
	var sqliteError sqlite3.Error
	if errors.As(err, &sqliteError) && (sqliteError.Code == sqlite3.ErrBusy || sqliteError.Code == sqlite3.ErrLocked) {
		return true
	}
	var modern interface{ Code() int }
	if errors.As(err, &modern) && (modern.Code()&255 == 5 || modern.Code()&255 == 6) {
		return true
	}
	var mysqlError *mysql.MySQLError
	if errors.As(err, &mysqlError) && (mysqlError.Number == 1205 || mysqlError.Number == 1213) {
		return true
	}
	var state interface{ SQLState() string }
	return errors.As(err, &state) && (state.SQLState() == "40001" || state.SQLState() == "40P01")
}

func (s *ComponentStore) Claim(ctx context.Context, principal, threadID, runID string, expectedRunRevision int64, owner string, ttl time.Duration) (*Run, error) {
	out, err := s.invoke(ctx, &Request{Operation: "claim", Principal: principal, ThreadID: threadID, RunID: runID, ExpectedRevision: expectedRunRevision, LeaseOwner: owner, TTL: ttl})
	if err != nil {
		return nil, err
	}
	return out.Run, nil
}
func (s *ComponentStore) Renew(ctx context.Context, principal, threadID, runID string, expectedLeaseRevision int64, owner string, ttl time.Duration) (*Run, error) {
	out, err := s.invoke(ctx, &Request{Operation: "renew", Principal: principal, ThreadID: threadID, RunID: runID, ExpectedRevision: expectedLeaseRevision, LeaseOwner: owner, TTL: ttl})
	if err != nil {
		return nil, err
	}
	return out.Run, nil
}

func (s *ComponentStore) ListRunsByTurn(ctx context.Context, principal, threadID, turnID string) ([]*Run, error) {
	out, err := s.invoke(ctx, &Request{Operation: "turnruns", Principal: principal, ThreadID: threadID, TurnID: turnID})
	if err != nil {
		return nil, err
	}
	return out.Runs, nil
}

var _ TurnRunLister = (*ComponentStore)(nil)

func (s *ComponentStore) GetThreadByConversationID(ctx context.Context, principal, conversationID string) (*Thread, error) {
	out, err := s.invoke(ctx, &Request{Operation: "nativeThread", Principal: principal, ThreadID: conversationID})
	if err != nil {
		return nil, err
	}
	return out.Thread, nil
}
func (s *ComponentStore) PromoteThread(ctx context.Context, principal, threadID string) (*Thread, error) {
	out, err := s.invoke(ctx, &Request{Operation: "promote", Principal: principal, ThreadID: threadID})
	if err != nil {
		return nil, err
	}
	return out.Thread, nil
}
