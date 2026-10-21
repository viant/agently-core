// Package maintenancetx contains policies used only by retention transactions.
// Ordinary writers and manual conversation deletion do not opt into them.
package maintenancetx

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	dexec "github.com/viant/datly/exec"
	xhandler "github.com/viant/xdatly/handler"
	moderncsqlite "modernc.org/sqlite"
)

type DriverInspector interface {
	ConfiguredDriver(context.Context, string) (string, error)
}

// Prepare must run before binding readers or starting the transaction. SQLite
// uses its native default; MySQL uses non-locking READ COMMITTED observations.
func Prepare(ctx context.Context, inspector DriverInspector) (context.Context, error) {
	ctx = queryselectors.ForUpdateOptions(ctx, false).Context(ctx)
	if inspector == nil {
		return nil, fmt.Errorf("cleanup configured database driver is required")
	}
	driver, err := inspector.ConfiguredDriver(ctx, "agently")
	if err != nil {
		return nil, err
	}
	switch driver = strings.ToLower(strings.TrimSpace(driver)); {
	case strings.Contains(driver, "mysql"):
		return dexec.WithTransactionIsolation(ctx, dexec.IsolationReadCommitted), nil
	case strings.Contains(driver, "sqlite"):
		if _, requested := dexec.RequestedTransactionIsolation(ctx); requested {
			return nil, fmt.Errorf("SQLite cleanup requires native default transaction isolation")
		}
		return ctx, nil
	default:
		return nil, fmt.Errorf("unsupported cleanup database driver %q", driver)
	}
}

type attemptKey struct{}

// Effects separates immediate SQL counts from the native journal. Counts are
// published only after a successful invocation; rollback never reports progress.
type Effects struct {
	reporter  dexec.MutationReporter
	immediate map[string]int64
}

func current(ctx context.Context) *Effects {
	effects, _ := ctx.Value(attemptKey{}).(*Effects)
	return effects
}

func Enabled(ctx context.Context) bool { return current(ctx) != nil }

// Deleted is also available inside an attempt for private bulk writers. The
// outer caller must not publish it until Invoke has completed successfully.
func Deleted(ctx context.Context, table string) int64 { return current(ctx).Deleted(table) }

func Observe(ctx context.Context, reporter dexec.MutationReporter) {
	if effects := current(ctx); effects != nil {
		effects.reporter = reporter
	}
}

// RecordImmediate excludes any anchor already counted in MutationReport.
func RecordImmediate(ctx context.Context, table string, affected int64) {
	if effects := current(ctx); effects != nil {
		effects.immediate[table] += affected
	}
}

func (e *Effects) Deleted(table string) int64 {
	if e == nil {
		return 0
	}
	n := e.immediate[table]
	if e.reporter != nil {
		for _, result := range e.reporter.MutationReport().Results {
			if result.Error == nil && result.Operation == "delete" && result.Table == table {
				n += result.Affected
			}
		}
	}
	return n
}

func (e *Effects) DeletedRows() int64 {
	if e == nil {
		return 0
	}
	var n int64
	for _, affected := range e.immediate {
		n += affected
	}
	if e.reporter != nil {
		for _, result := range e.reporter.MutationReport().Results {
			if result.Error == nil && result.Operation == "delete" {
				n += result.Affected
			}
		}
	}
	return n
}

// GuardChanged means an actual guarded write missed and the entire cleanup
// transaction was confirmed rolled back. It is a safe skip, never a deletion.
type GuardChanged struct{ Cause error }

func (e *GuardChanged) Error() string {
	return "cleanup protected by a changed write guard: " + e.Cause.Error()
}
func (e *GuardChanged) Unwrap() error { return e.Cause }

// Invoke owns the only retry loop. A fresh preflight and component invocation
// rebuild every read and plan. Completion evidence, not an SQL error alone,
// proves rollback. Caller-owned, unknown and committed outcomes never replay.
func Invoke(ctx context.Context, invoker dexec.ComponentInvoker, preflight func(context.Context) (context.Context, error), request dexec.ComponentRequest) (any, *Effects, error) {
	if Enabled(ctx) {
		return nil, nil, fmt.Errorf("cleanup requires a top-level transaction boundary")
	}
	if request.Completion != nil {
		return nil, nil, fmt.Errorf("cleanup owns its completion observer")
	}
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		scope, err := preflight(ctx)
		if err != nil {
			return nil, nil, err
		}
		if dexec.HasInvocationTransactionLookup(scope) {
			return nil, nil, fmt.Errorf("cleanup requires a top-level transaction boundary")
		}
		effects := &Effects{immediate: map[string]int64{}}
		scope = context.WithValue(scope, attemptKey{}, effects)
		scope = queryselectors.ForUpdateOptions(scope, false).Context(scope)
		var outcome xhandler.Outcome
		request.Completion = func(result xhandler.Outcome) { outcome = result.Clone() }
		value, err := invoker.InvokeComponent(scope, request)
		if err == nil {
			return value, effects, nil
		}
		guard := nativeGuardMiss(err)
		if !rolledBack(outcome) || !onlyRetryable(err) {
			return nil, nil, err
		}
		if attempt == 2 {
			if guard {
				return nil, nil, &GuardChanged{Cause: err}
			}
			return nil, nil, err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, nil, ctx.Err()
		case <-timer.C:
		}
	}
	panic("unreachable cleanup retry")
}

func rolledBack(outcome xhandler.Outcome) bool {
	// Cleanup is one database unit, not a distributed transaction.
	return len(outcome.Transactions) == 1 && outcome.Transactions[0].State == xhandler.TransactionRolledBack && onlyRetryable(outcome.Transactions[0].Error) && onlyRetryable(outcome.Error)
}

func nativeGuardMiss(err error) bool {
	var conflict *xhandler.Conflict
	return errors.As(err, &conflict) && conflict.Reason == "mutation predicate no longer matches persisted row"
}

// Walk every joined cause: an unrelated completion error must veto replay.
// sql.ErrTxDone/context cancellation are deliberately not contention evidence.
func onlyRetryable(err error) bool {
	if err == nil {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, cause := range joined.Unwrap() {
			if !onlyRetryable(cause) {
				return false
			}
		}
		return len(joined.Unwrap()) > 0
	}
	if cause, ok := err.(interface{ Unwrap() error }); ok && cause.Unwrap() != nil {
		return onlyRetryable(cause.Unwrap())
	}
	if conflict, ok := err.(*xhandler.Conflict); ok {
		return conflict.Reason == "mutation predicate no longer matches persisted row"
	}
	if failure, ok := err.(*mysql.MySQLError); ok {
		return failure.Number == 1213 || failure.Number == 1205
	}
	if cgoSQLiteContention(err) {
		return true
	}
	if failure, ok := err.(*moderncsqlite.Error); ok {
		code := failure.Code() & 0xff
		return code == 5 || code == 6
	}
	return false
}
