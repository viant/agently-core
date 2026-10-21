package maintenancetx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	dexec "github.com/viant/datly/exec"
	xhandler "github.com/viant/xdatly/handler"
)

type driver string

func (d driver) ConfiguredDriver(context.Context, string) (string, error) { return string(d), nil }

type invoker func(context.Context, dexec.ComponentRequest) (any, error)

func (f invoker) InvokeComponent(ctx context.Context, r dexec.ComponentRequest) (any, error) {
	return f(ctx, r)
}

type reporter []dexec.MutationResult

func (r reporter) MutationReport() dexec.MutationReport { return dexec.MutationReport{Results: r} }
func pass(ctx context.Context) (context.Context, error) { return ctx, nil }
func completion(state xhandler.TransactionState, err error) xhandler.Outcome {
	return xhandler.Outcome{Error: err, Transactions: []xhandler.TransactionOutcome{{Unit: 1, State: state}}}
}

type unrelatedCode int

func (e unrelatedCode) Error() string { return "not a SQLite driver error" }
func (e unrelatedCode) Code() int     { return int(e) }

func sqliteBusyError(t *testing.T) error {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "busy.db")
	first, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, first.Close()) })
	second, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, second.Close()) })
	_, err = first.Exec("CREATE TABLE busy (id INTEGER PRIMARY KEY)")
	require.NoError(t, err)
	tx, err := first.Begin()
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.Exec("INSERT INTO busy VALUES (1)")
	require.NoError(t, err)
	_, err = second.Exec("INSERT INTO busy VALUES (2)")
	require.Error(t, err)
	require.True(t, onlyRetryable(err), "real modernc SQLITE_BUSY must be recognized")
	return err
}

func TestPrepareBeforeBinding(t *testing.T) {
	base := queryselectors.ForUpdateOptions(context.Background(), true).Context(context.Background())
	base = dexec.WithTransactionIsolation(base, dexec.IsolationSerializable)
	ctx, err := Prepare(base, driver("mysql"))
	require.NoError(t, err)
	require.Empty(t, dexec.ReaderOptionsFromContext(ctx).ForUpdate)
	isolation, requested := dexec.RequestedTransactionIsolation(ctx)
	require.True(t, requested)
	require.Equal(t, dexec.IsolationReadCommitted, isolation)
	require.NotEmpty(t, dexec.ReaderOptionsFromContext(base).ForUpdate, "do not mutate the caller")
	_, err = Prepare(base, driver("sqlite3"))
	require.Error(t, err, "inherited isolation must not silently leak into SQLite cleanup")
	ctx, err = Prepare(context.Background(), driver("sqlite3"))
	require.NoError(t, err)
	_, requested = dexec.RequestedTransactionIsolation(ctx)
	require.False(t, requested)
	_, err = Prepare(context.Background(), driver("postgres"))
	require.Error(t, err)
	_, err = Prepare(context.Background(), nil)
	require.Error(t, err)
}

func TestRetryFreshPreflightAndEffects(t *testing.T) {
	var calls, checks int
	deadlock := &mysql.MySQLError{Number: 1213}
	value, effects, err := Invoke(context.Background(), invoker(func(ctx context.Context, r dexec.ComponentRequest) (any, error) {
		calls++
		require.Equal(t, calls, checks)
		require.Empty(t, dexec.ReaderOptionsFromContext(ctx).ForUpdate)
		require.Zero(t, Deleted(ctx, "call_payload"), "each attempt has a fresh ledger")
		Observe(ctx, reporter{{Operation: "delete", Table: "conversation", Affected: 1}, {Operation: "update", Table: "conversation", Affected: 1}})
		RecordImmediate(ctx, "call_payload", 2)
		if calls < 3 {
			r.Completion(completion(xhandler.TransactionRolledBack, deadlock))
			return nil, fmt.Errorf("SQL: %w", deadlock)
		}
		r.Completion(completion(xhandler.TransactionCommitted, nil))
		return "done", nil
	}), func(ctx context.Context) (context.Context, error) { checks++; return ctx, nil }, dexec.ComponentRequest{})
	require.NoError(t, err)
	require.Equal(t, "done", value)
	require.Equal(t, 3, calls)
	require.EqualValues(t, 1, effects.Deleted("conversation"))
	require.EqualValues(t, 2, effects.Deleted("call_payload"))
	require.EqualValues(t, 3, effects.DeletedRows())
}

func TestRetryOnlyKnownErrorAndConfirmedRollback(t *testing.T) {
	deadlock := &mysql.MySQLError{Number: 1213}
	guard := &xhandler.Conflict{Entity: "call_payload", Reason: "mutation predicate no longer matches persisted row"}
	for _, test := range []struct {
		name     string
		err      error
		outcome  xhandler.Outcome
		attempts int
		guarded  bool
	}{
		{"deadlock", deadlock, completion(xhandler.TransactionRolledBack, deadlock), 3, false},
		{"timeout", &mysql.MySQLError{Number: 1205}, completion(xhandler.TransactionRolledBack, nil), 3, false},
		{"sqlite", sqliteBusyError(t), completion(xhandler.TransactionRolledBack, nil), 3, false},
		{"unrelated coded error", unrelatedCode(5), completion(xhandler.TransactionRolledBack, nil), 1, false},
		{"guard", guard, completion(xhandler.TransactionRolledBack, guard), 3, true},
		{"permanent", &mysql.MySQLError{Number: 1142}, completion(xhandler.TransactionRolledBack, nil), 1, false},
		{"auth", errors.New("unauthorized"), completion(xhandler.TransactionRolledBack, nil), 1, false},
		{"business conflict", &xhandler.Conflict{Reason: "revision conflict"}, completion(xhandler.TransactionRolledBack, nil), 1, false},
		{"commit unknown", deadlock, completion(xhandler.TransactionCommitUnknown, deadlock), 1, false},
		{"rollback unknown", deadlock, completion(xhandler.TransactionRollbackUnknown, deadlock), 1, false},
		{"committed", deadlock, completion(xhandler.TransactionCommitted, deadlock), 1, false},
		{"caller pending", deadlock, completion(xhandler.TransactionCallerPending, deadlock), 1, false},
		{"no evidence", deadlock, xhandler.Outcome{}, 1, false},
		{"joined permanent", errors.Join(deadlock, sql.ErrTxDone), completion(xhandler.TransactionRolledBack, nil), 1, false},
		{"completion failure", deadlock, completion(xhandler.TransactionRolledBack, sql.ErrConnDone), 1, false},
		{"canceled", context.Canceled, completion(xhandler.TransactionRolledBack, nil), 1, false},
		{"multiple units", deadlock, xhandler.Outcome{Transactions: []xhandler.TransactionOutcome{{State: xhandler.TransactionRolledBack}, {State: xhandler.TransactionCommitted}}}, 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			value, effects, err := Invoke(context.Background(), invoker(func(ctx context.Context, r dexec.ComponentRequest) (any, error) {
				calls++
				RecordImmediate(ctx, "conversation", 42)
				r.Completion(test.outcome)
				return nil, test.err
			}), pass, dexec.ComponentRequest{})
			require.Error(t, err)
			require.Nil(t, value)
			require.Nil(t, effects)
			require.Equal(t, test.attempts, calls)
			var changed *GuardChanged
			require.Equal(t, test.guarded, errors.As(err, &changed))
		})
	}
}

func TestRetryLeaseFailureAndCancellation(t *testing.T) {
	failure := errors.New("lease lost")
	checks, calls := 0, 0
	_, _, err := Invoke(context.Background(), invoker(func(ctx context.Context, r dexec.ComponentRequest) (any, error) {
		calls++
		err := &mysql.MySQLError{Number: 1213}
		r.Completion(completion(xhandler.TransactionRolledBack, err))
		return nil, err
	}), func(ctx context.Context) (context.Context, error) {
		checks++
		if checks == 2 {
			return nil, failure
		}
		return ctx, nil
	}, dexec.ComponentRequest{})
	require.ErrorIs(t, err, failure)
	require.Equal(t, 1, calls)
	require.Equal(t, 2, checks)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls = 0
	_, _, err = Invoke(ctx, invoker(func(ctx context.Context, r dexec.ComponentRequest) (any, error) {
		calls++
		cancel()
		err := &mysql.MySQLError{Number: 1213}
		r.Completion(completion(xhandler.TransactionRolledBack, err))
		return nil, err
	}), pass, dexec.ComponentRequest{})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, calls)
}

func TestNeverOwnCallerTransaction(t *testing.T) {
	ctx := dexec.WithInvocationTransactionLookup(context.Background(), func(context.Context, *sql.DB) (*sql.Tx, error) {
		t.Fatal("must not resolve caller transaction")
		return nil, nil
	})
	_, _, err := Invoke(ctx, invoker(func(context.Context, dexec.ComponentRequest) (any, error) {
		t.Fatal("must not invoke")
		return nil, nil
	}), pass, dexec.ComponentRequest{})
	require.Error(t, err)
}
