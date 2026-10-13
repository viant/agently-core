package delete

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/bindly"
	dexec "github.com/viant/datly/exec"
	rh "github.com/viant/datly/runtime/handler"
	"github.com/viant/datly/runtime/handler/provider"
	xhandler "github.com/viant/xdatly/handler"
)

type sqlProbe struct {
	calls, flushes int
	query          string
	args           []any
	err, flushErr  error
	anchorTable    string
	anchorKey      any
	anchorAffected int64
	bulkAffected   int64
	report         dexec.MutationReport
}

func (p *sqlProbe) Connector(_ context.Context, name string) (rh.TransactionSQL, error) {
	if name != "agently" {
		return nil, errors.New("wrong connector")
	}
	return p, nil
}
func (p *sqlProbe) Insert(string, any) error     { panic("unexpected insert") }
func (p *sqlProbe) Update(string, any) error     { panic("unexpected update") }
func (p *sqlProbe) Execute(string, ...any) error { panic("unexpected SQL") }
func (p *sqlProbe) Delete(table string, key any) error {
	p.anchorTable, p.anchorKey = table, key
	return nil
}
func (p *sqlProbe) MutationReport() dexec.MutationReport { return p.report }
func (p *sqlProbe) Flush(_ context.Context, table string) error {
	p.flushes++
	if table != p.anchorTable || p.anchorKey == nil {
		return errors.New("flush lacks deletion anchor")
	}
	if p.flushErr == nil {
		p.report.Results = append(p.report.Results, dexec.MutationResult{Operation: "delete", Table: table, Affected: p.anchorAffected})
	}
	return p.flushErr
}
func (p *sqlProbe) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	if p.flushes != 1 {
		return nil, errors.New("queued writes not flushed")
	}
	p.calls++
	p.query = query
	p.args = args
	return result(p.bulkAffected), p.err
}
func (*sqlProbe) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	panic("unexpected pre-read")
}
func (*sqlProbe) QueryRowContext(context.Context, string, ...any) *sql.Row {
	panic("unexpected pre-read")
}

type result int64

func (r result) RowsAffected() (int64, error) { return int64(r), nil }
func (result) LastInsertId() (int64, error)   { return 0, errors.New("not insert") }

func invoke(t *testing.T, ctx context.Context, input *Input, probe *sqlProbe) (any, error) {
	t.Helper()
	injector, err := bindly.NewInjector(bindly.WithProviders(
		provider.New(rh.TransactionSQLCapabilityKey, func(context.Context) (any, bool, error) { return probe, true, nil }),
		provider.New(xhandler.DMLKey, func(context.Context) (any, bool, error) { return probe, true, nil }),
		provider.New(xhandler.FlusherKey, func(context.Context) (any, bool, error) { return probe, true, nil }),
		provider.New(dexec.MutationReporterKey, func(context.Context) (any, bool, error) { return probe, true, nil }),
	))
	require.NoError(t, err)
	return (&Delete{}).Execute(ctx, rh.Invocation{Input: input, Binder: rh.NewBinder(injector, input)})
}

func TestBulkChildrenOnlyFixedSQLNoReadsAndBoundedKeys(t *testing.T) {
	for table, query := range queries {
		for _, count := range []int{0, 1, 399, 400} {
			t.Run(fmt.Sprintf("%s/%d", table, count), func(t *testing.T) {
				input := &Input{Trusted: true, Table: table}
				for i := 0; i < count; i++ {
					input.IDs = append(input.IDs, fmt.Sprintf("key-%03d", i))
				}
				probe := &sqlProbe{}
				value, err := invoke(t, context.Background(), input, probe)
				require.NoError(t, err)
				require.Zero(t, value.(*Output).Affected)
				if count == 0 {
					require.Zero(t, probe.calls)
					require.Zero(t, probe.flushes)
					return
				}
				require.Equal(t, table, probe.anchorTable)
				require.Equal(t, 1, probe.flushes)
				if count == 1 {
					require.Zero(t, probe.calls)
					return
				}
				require.Equal(t, 1, probe.calls)
				require.Equal(t, count-1, strings.Count(probe.query, "?"))
				require.Equal(t, strings.Replace(query, "/*IDS*/", strings.TrimSuffix(strings.Repeat("?,", count-1), ","), 1), probe.query)
				require.Len(t, probe.args, count-1)
			})
		}
	}
	require.True(t, (&Delete{}).RequiresPreBindingTransaction())
}

func TestBulkChildrenAffectedIncludesAnchorAndRemainingKeys(t *testing.T) {
	probe := &sqlProbe{anchorAffected: 1, bulkAffected: 2}
	value, err := invoke(t, context.Background(), &Input{Trusted: true, Table: "message", IDs: []string{"a", "b", "c"}}, probe)
	require.NoError(t, err)
	require.EqualValues(t, 3, value.(*Output).Affected)
	require.Len(t, probe.args, 2)
}

func TestBulkChildrenRejectsUntrustedUnboundedAndUnsupportedRequests(t *testing.T) {
	for _, input := range []*Input{
		{Table: "message", IDs: []string{"one"}},
		{Trusted: true, Table: "conversation", IDs: []string{"one"}},
		{Trusted: true, Table: "message WHERE 1=1", IDs: []string{"one"}},
		{Trusted: true, Table: "message", IDs: []string{" "}},
		{Trusted: true, Table: "message", IDs: make([]string, 401)},
	} {
		probe := &sqlProbe{}
		_, err := invoke(t, context.Background(), input, probe)
		require.Error(t, err)
		require.Zero(t, probe.calls)
		require.Zero(t, probe.flushes)
	}
	input := &Input{Trusted: true, Table: "message", IDs: []string{"z", "a", "z"}}
	require.NoError(t, input.Init(context.Background()))
	require.Equal(t, []string{"a", "z"}, input.IDs)
}

func TestBulkChildrenPropagatesCancellationFlushAndSQLFailures(t *testing.T) {
	failure := errors.New("injected failure")
	for _, phase := range []string{"cancel", "flush", "exec"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			probe := &sqlProbe{}
			switch phase {
			case "cancel":
				cancel()
			case "flush":
				probe.flushErr = failure
			case "exec":
				probe.err = failure
			}
			value, err := invoke(t, ctx, &Input{Trusted: true, Table: "message", IDs: []string{"one", "two"}}, probe)
			require.Error(t, err)
			require.Nil(t, value)
			if phase == "cancel" {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorIs(t, err, failure)
			}
			require.LessOrEqual(t, probe.calls, 1)
		})
	}
}
