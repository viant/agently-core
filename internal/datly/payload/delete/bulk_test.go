package delete

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/bindly"
	rh "github.com/viant/datly/runtime/handler"
	"github.com/viant/datly/runtime/handler/provider"
	xhandler "github.com/viant/xdatly/handler"
)

type bulkSQLProbe struct {
	calls, flushes int
	query          string
	args           []any
	affected       int64
	err, flushErr  error
}

func (p *bulkSQLProbe) Connector(_ context.Context, name string) (rh.TransactionSQL, error) {
	if name != "agently" {
		return nil, errors.New("unexpected connector")
	}
	return p, nil
}
func (p *bulkSQLProbe) Flush(context.Context, string) error { p.flushes++; return p.flushErr }
func (p *bulkSQLProbe) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	if p.flushes != 1 {
		return nil, errors.New("reference writes not flushed")
	}
	p.calls++
	p.query = query
	p.args = args
	return bulkResult(p.affected), p.err
}
func (*bulkSQLProbe) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	panic("unexpected read")
}
func (*bulkSQLProbe) QueryRowContext(context.Context, string, ...any) *sql.Row {
	panic("unexpected read")
}

type bulkResult int64

func (r bulkResult) RowsAffected() (int64, error) { return int64(r), nil }
func (bulkResult) LastInsertId() (int64, error)   { return 0, errors.New("not an insert") }

func TestBulkDeleteOneParameterizedStatement(t *testing.T) {
	for _, count := range []int{0, 1, 399, 400} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			probe := &bulkSQLProbe{affected: int64(count)}
			input := &Input{DeleteUnreferenced: true}
			for i := 0; i < count; i++ {
				input.Payloads = append(input.Payloads, &PayloadDelete{Id: strings.Repeat("x", i+1)})
			}
			value, err := invokeBulkProbe(t, input, probe)
			require.NoError(t, err)
			require.Len(t, value.(*Output).Data, count)
			if count == 0 {
				require.Zero(t, probe.calls)
				require.Zero(t, probe.flushes)
				return
			}
			require.Equal(t, 1, probe.calls)
			require.Equal(t, count, strings.Count(probe.query, "?"))
			require.Len(t, probe.args, count)
			require.Equal(t, 10, strings.Count(probe.query, "NOT EXISTS"))
			require.NotContains(t, probe.query, "inline_body")
		})
	}
	require.True(t, (&BulkDelete{}).RequiresPreBindingTransaction())
}

func TestBulkDeleteFailureNeverFallsBack(t *testing.T) {
	for _, kind := range []string{"predicate_mismatch", "exec_error", "flush_error", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			probe := &bulkSQLProbe{affected: 1}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failure := errors.New("injected failure")
			switch kind {
			case "exec_error":
				probe.err = failure
			case "flush_error":
				probe.flushErr = failure
			case "cancelled":
				cancel()
			}
			input := &Input{DeleteUnreferenced: true, Payloads: []*PayloadDelete{{Id: "one"}, {Id: "two"}}}
			value, err := invokeBulkProbeContext(t, ctx, input, probe)
			require.Error(t, err)
			require.Nil(t, value)
			if kind == "predicate_mismatch" {
				var conflict *xhandler.Conflict
				require.ErrorAs(t, err, &conflict)
			}
			if kind == "exec_error" || kind == "flush_error" {
				require.ErrorIs(t, err, failure)
			}
			if kind == "cancelled" {
				require.ErrorIs(t, err, context.Canceled)
			}
			require.LessOrEqual(t, probe.calls, 1)
		})
	}
}

func TestBulkGuardMatchesGeneratedRowGuard(t *testing.T) {
	field, _ := reflect.TypeFor[Input]().FieldByName("DeleteUnreferenced")
	guard := strings.ToLower(strings.ReplaceAll(field.Tag.Get("predicate"), " ", ""))
	bulk := strings.ToLower(strings.ReplaceAll(bulkDeleteSQL, " ", ""))
	for _, part := range strings.Split(guard, "notexists")[1:] {
		end := strings.Index(part, ")")
		require.GreaterOrEqual(t, end, 0)
		require.Contains(t, bulk, "notexists"+part[:end+1])
	}
	require.Equal(t, strings.Count(guard, "notexists"), strings.Count(bulk, "notexists"))
}

func invokeBulkProbe(t *testing.T, input *Input, probe *bulkSQLProbe) (any, error) {
	return invokeBulkProbeContext(t, context.Background(), input, probe)
}
func invokeBulkProbeContext(t *testing.T, ctx context.Context, input *Input, probe *bulkSQLProbe) (any, error) {
	t.Helper()
	injector, err := bindly.NewInjector(bindly.WithProviders(
		provider.New(rh.TransactionSQLCapabilityKey, func(context.Context) (any, bool, error) { return probe, true, nil }),
		provider.New(xhandler.FlusherKey, func(context.Context) (any, bool, error) { return probe, true, nil }),
	))
	require.NoError(t, err)
	return (&BulkDelete{}).Execute(ctx, rh.Invocation{Input: input, Binder: rh.NewBinder(injector, input)})
}
