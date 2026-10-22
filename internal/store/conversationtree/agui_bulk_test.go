package conversationtree

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	eventdelete "github.com/viant/agently-core/internal/datly/agui/cleanup/event/delete"
	runread "github.com/viant/agently-core/internal/datly/agui/cleanup/run/read"
	"github.com/viant/agently-core/internal/store/maintenancetx"
	dexec "github.com/viant/datly/exec"
	rh "github.com/viant/datly/runtime/handler"
	xhandler "github.com/viant/xdatly/handler"
)

type aguiBulkSQL struct {
	execute func(string, []any) (sql.Result, error)
}

func (s aguiBulkSQL) Connector(_ context.Context, name string) (rh.TransactionSQL, error) {
	if name != "agently" {
		return nil, fmt.Errorf("unexpected connector %s", name)
	}
	return s, nil
}
func (s aguiBulkSQL) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	return s.execute(query, args)
}
func (aguiBulkSQL) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("unexpected query")
}
func (aguiBulkSQL) QueryRowContext(context.Context, string, ...any) *sql.Row {
	panic("unexpected query")
}

func bulkEventPlan(n int) *DeletePlan {
	plan := unitTestPlan(n)
	plan.PayloadIDs = nil
	run, thread := "protocol-run", "root"
	plan.AGUIRuns = []*runread.Run{{RunKey: &run, ThreadId: &thread}}
	return plan
}

func TestMaintenanceAGUIEventsBulkBoundsScopeAndConfirmedCounts(t *testing.T) {
	for _, n := range []int{0, 1, 399, 400, 401, 1001} {
		for _, missing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/missing=%t", n, missing), func(t *testing.T) {
				calls, previous := 0, ""
				provider := aguiBulkSQL{execute: func(query string, args []any) (sql.Result, error) {
					calls++
					for _, guard := range []string{"kind = 'agui.event'", "run_id IN", "r.run_kind = 'agui'", "r.conversation_id IN"} {
						require.Contains(t, query, guard)
					}
					require.NotContains(t, query, "/*")
					keys := len(args) - 2
					require.Greater(t, keys, 0)
					require.LessOrEqual(t, keys, 400)
					require.Equal(t, "protocol-run", args[keys])
					require.Equal(t, "root", args[keys+1])
					for _, key := range args[:keys] {
						require.Greater(t, strings.Compare(key.(string), previous), 0)
						previous = key.(string)
					}
					if missing {
						return driver.RowsAffected(0), nil
					}
					return driver.RowsAffected(keys), nil
				}}
				m := &Mutator{SQL: provider}
				_, effects, err := maintenancetx.Invoke(context.Background(), unitTestInvoker(func(ctx context.Context, req dexec.ComponentRequest) (any, error) {
					err := m.deleteAGUIEvents(ctx, bulkEventPlan(n))
					req.Completion(xhandler.Outcome{Transactions: []xhandler.TransactionOutcome{{Unit: 1, State: xhandler.TransactionCommitted}}})
					return nil, err
				}), func(ctx context.Context) (context.Context, error) { return ctx, nil }, dexec.ComponentRequest{})
				require.NoError(t, err)
				require.Equal(t, (n+399)/400, calls)
				summary, err := effects.CommitSummary()
				require.NoError(t, err)
				want := n
				if missing {
					want = 0
				}
				require.EqualValues(t, want, summary.DeletedRows)
			})
		}
	}
}

func TestMaintenanceAGUIEventsRejectScopeAndStopOnFailure(t *testing.T) {
	for _, badScope := range []bool{true, false} {
		plan := bulkEventPlan(401)
		if badScope {
			foreign := "foreign"
			plan.AGUIEvents[0].ThreadId = &foreign
		}
		calls := 0
		m := &Mutator{SQL: aguiBulkSQL{execute: func(string, []any) (sql.Result, error) {
			calls++
			if calls == 2 {
				return nil, errors.New("forced bulk failure")
			}
			return driver.RowsAffected(400), nil
		}}}
		_, effects, err := maintenancetx.Invoke(context.Background(), unitTestInvoker(func(ctx context.Context, req dexec.ComponentRequest) (any, error) {
			err := m.deleteAGUIEvents(ctx, plan)
			req.Completion(xhandler.Outcome{Transactions: []xhandler.TransactionOutcome{{Unit: 1, State: xhandler.TransactionRolledBack}}})
			return nil, err
		}), func(ctx context.Context) (context.Context, error) { return ctx, nil }, dexec.ComponentRequest{})
		if badScope {
			require.ErrorContains(t, err, "scope mismatch")
			require.Zero(t, calls)
		} else {
			require.ErrorContains(t, err, "forced bulk failure")
			require.Equal(t, 2, calls)
		}
		_, err = effects.CommitSummary()
		require.ErrorContains(t, err, "not confirmed")
	}
}

func TestManualAGUIEventsKeepCanonicalWriterEvenWithSQLCapability(t *testing.T) {
	calls := 0
	m := &Mutator{OwnerID: func(context.Context) string { return "owner" }, SQL: aguiBulkSQL{execute: func(string, []any) (sql.Result, error) {
		t.Fatal("manual deletion cannot opt into bulk SQL")
		return nil, nil
	}}, Invoker: mutationTestInvoker(func(_ context.Context, req dexec.ComponentRequest) (any, error) {
		calls++
		require.Equal(t, "/v1/internal/agently/ag-ui/cleanup/event/delete", req.Target.Route.Path)
		require.Len(t, req.Input.(*eventdelete.Input).Rows, 1)
		return &eventdelete.Output{}, nil
	})}
	require.NoError(t, m.deleteAGUIEvents(context.Background(), bulkEventPlan(1)))
	require.Equal(t, 1, calls)
}
