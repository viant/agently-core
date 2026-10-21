package maintenancelease_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	read "github.com/viant/agently-core/internal/datly/maintenancelease/read"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	"github.com/viant/agently-core/internal/store/conversationmaintenance"
	"github.com/viant/agently-core/internal/store/maintenancelease"
	"github.com/viant/agently-core/internal/store/orphanmaintenance"
	"github.com/viant/agently-core/internal/store/scheduledmaintenance"
	"github.com/viant/agently-core/internal/store/technicalmaintenance"
	dexec "github.com/viant/datly/exec"
)

type checkInvoker func(context.Context, dexec.ComponentRequest) (any, error)

func (f checkInvoker) InvokeComponent(ctx context.Context, request dexec.ComponentRequest) (any, error) {
	return f(ctx, request)
}
func (f checkInvoker) ConfiguredDriver(context.Context, string) (string, error) {
	return "sqlite3", nil
}

type checkSchema struct{}

func (checkSchema) HasTable(context.Context, string, string) (bool, error) { return false, nil }

func leaseSnapshot(now time.Time, lease maintenancelease.Lease) *read.LeaseSnapshot {
	return &read.LeaseSnapshot{DbNow: now.Format(time.RFC3339Nano), LeaseKey: &lease.Key, OwnerId: &lease.OwnerID, LeaseToken: &lease.Token, LeaseUntil: &lease.LeaseUntil}
}

func TestCheckUsesPersistedLeaseAndDatabaseClock(t *testing.T) {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC) // Deliberately not the process clock.
	lease := maintenancelease.Lease{Key: "cleanup", OwnerID: "worker", Token: "token", LeaseUntil: now.Add(time.Hour)}
	for _, scenario := range []struct {
		name string
		edit func(*read.LeaseSnapshot)
		want error
	}{
		{name: "live"},
		{name: "missing", edit: func(row *read.LeaseSnapshot) { row.LeaseKey = nil }, want: maintenancelease.ErrLeaseLost},
		{name: "owner", edit: func(row *read.LeaseSnapshot) { other := "other"; row.OwnerId = &other }, want: maintenancelease.ErrLeaseLost},
		{name: "token", edit: func(row *read.LeaseSnapshot) { other := "other"; row.LeaseToken = &other }, want: maintenancelease.ErrLeaseLost},
		{name: "expired", edit: func(row *read.LeaseSnapshot) { at := now.Add(-time.Second); row.LeaseUntil = &at }, want: maintenancelease.ErrLeaseLost},
		{name: "expiry_equal", edit: func(row *read.LeaseSnapshot) { row.LeaseUntil = &now }, want: maintenancelease.ErrLeaseLost},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			row := leaseSnapshot(now, lease)
			if scenario.edit != nil {
				scenario.edit(row)
			}
			calls := 0
			store := &maintenancelease.Store{Invoker: checkInvoker(func(ctx context.Context, request dexec.ComponentRequest) (any, error) {
				calls++
				input, ok := request.Input.(*read.Input)
				require.True(t, ok, "check must only read, never guard/write")
				require.Equal(t, lease.Key, input.LeaseKey)
				require.Empty(t, dexec.ReaderOptionsFromContext(ctx).ForUpdate)
				return &read.Output{Data: []*read.LeaseSnapshot{row}}, nil
			})}
			// A stale client expiry must not veto a lease that heartbeat extended.
			client := lease
			client.LeaseUntil = now.Add(-time.Hour)
			ctx := queryselectors.ForUpdateOptions(context.Background(), true).Context(context.Background())
			at, err := store.Check(ctx, client)
			require.ErrorIs(t, err, scenario.want)
			if err == nil {
				require.Equal(t, now, at)
			}
			require.Equal(t, 1, calls)
		})
	}
}

func TestCheckRejectsUnsafeContextAndInvalidSnapshots(t *testing.T) {
	now := time.Now().UTC()
	lease := maintenancelease.Lease{Key: "cleanup", OwnerID: "worker", Token: "token", LeaseUntil: now.Add(time.Hour)}
	managed := dexec.WithInvocationTransactionLookup(context.Background(), func(context.Context, *sql.DB) (*sql.Tx, error) {
		t.Fatal("check must reject without resolving the inherited transaction")
		return nil, nil
	})
	store := &maintenancelease.Store{Invoker: checkInvoker(func(context.Context, dexec.ComponentRequest) (any, error) {
		t.Fatal("unsafe/invalid check must not invoke any component")
		return nil, nil
	})}
	_, err := store.Check(managed, lease)
	require.ErrorIs(t, err, maintenancelease.ErrTransactionContext)
	_, err = store.Check(context.Background(), maintenancelease.Lease{})
	require.ErrorIs(t, err, maintenancelease.ErrInvalidLease)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = store.Check(canceled, lease)
	require.ErrorIs(t, err, context.Canceled)

	for _, name := range []string{"empty", "nil", "multiple", "bad_clock", "incomplete", "sql_error", "wrong_output"} {
		t.Run(name, func(t *testing.T) {
			failure := errors.New("reader SQL failed")
			store := &maintenancelease.Store{Invoker: checkInvoker(func(context.Context, dexec.ComponentRequest) (any, error) {
				row := leaseSnapshot(now, lease)
				rows := []*read.LeaseSnapshot{row}
				switch name {
				case "empty":
					rows = nil
				case "nil":
					rows[0] = nil
				case "multiple":
					rows = append(rows, row)
				case "bad_clock":
					row.DbNow = "invalid"
				case "incomplete":
					row.OwnerId = nil
				case "sql_error":
					return nil, failure
				case "wrong_output":
					return "wrong", nil
				}
				return &read.Output{Data: rows}, nil
			})}
			_, err := store.Check(context.Background(), lease)
			require.Error(t, err)
			if name == "sql_error" {
				require.ErrorIs(t, err, failure)
			}
		})
	}
}

func TestPreflightReceiptIsInternalAndBoundToAcquisition(t *testing.T) {
	now := time.Now().UTC()
	lease := maintenancelease.Lease{Key: "cleanup", OwnerID: "worker", Token: "token", LeaseUntil: now.Add(time.Hour)}
	store := &maintenancelease.Store{Invoker: checkInvoker(func(context.Context, dexec.ComponentRequest) (any, error) {
		return &read.Output{Data: []*read.LeaseSnapshot{leaseSnapshot(now, lease)}}, nil
	})}
	_, err := maintenancelease.CheckedAt(context.Background(), lease)
	require.ErrorIs(t, err, maintenancelease.ErrUncheckedLease)
	ctx, err := store.Preflight(context.Background(), lease)
	require.NoError(t, err)
	at, err := maintenancelease.CheckedAt(ctx, lease)
	require.NoError(t, err)
	require.Equal(t, now, at)
	for _, field := range []string{"key", "owner", "token"} {
		other := lease
		switch field {
		case "key":
			other.Key += "-other"
		case "owner":
			other.OwnerID += "-other"
		case "token":
			other.Token += "-other"
		}
		_, err := maintenancelease.CheckedAt(ctx, other)
		require.ErrorIs(t, err, maintenancelease.ErrUncheckedLease)
	}
}

func TestMaintenanceEntryPointsCheckBeforeMutation(t *testing.T) {
	now := time.Now().UTC()
	lease := maintenancelease.Lease{Key: "cleanup", OwnerID: "worker", Token: "token", LeaseUntil: now.Add(time.Hour)}
	entries := map[string]func(context.Context, checkInvoker) error{
		"conversation": func(ctx context.Context, inv checkInvoker) error {
			_, err := (&conversationmaintenance.Store{Invoker: inv, Schema: checkSchema{}}).Maintain(ctx, &conversationmaintenance.Input{RootID: "root", Kind: conversationmaintenance.Interactive, InactiveBefore: now, Mode: scheduledmaintenance.DeleteMode, Lease: lease})
			return err
		},
		"scheduled": func(ctx context.Context, inv checkInvoker) error {
			_, err := (&scheduledmaintenance.Store{Invoker: inv, Schema: checkSchema{}}).Maintain(ctx, &scheduledmaintenance.Input{RunID: "run", InactiveBefore: now, Mode: scheduledmaintenance.DeleteMode, Lease: lease})
			return err
		},
		"orphan": func(ctx context.Context, inv checkInvoker) error {
			_, err := (&orphanmaintenance.Store{Invoker: inv}).Apply(ctx, orphanmaintenance.Request{RuleID: "call_payload.unused", RecordID: "payload", OlderThan: now, Lease: lease})
			return err
		},
		"technical": func(ctx context.Context, inv checkInvoker) error {
			_, err := (&technicalmaintenance.Store{Invoker: inv}).Maintain(ctx, technicalmaintenance.Request{Kind: technicalmaintenance.Session, Scope: technicalmaintenance.Unclassified, RecordID: "session", OlderThan: now, EvaluatedAt: now, Mode: technicalmaintenance.Delete, Lease: lease})
			return err
		},
		"purge": func(ctx context.Context, inv checkInvoker) error {
			_, err := (&maintenancelease.Store{Invoker: inv}).DeleteExpired(ctx, lease)
			return err
		},
	}
	for name, entry := range entries {
		for _, mode := range []string{"live", "lost", "expired", "missing", "managed", "sql_error"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				var events []string
				failure := errors.New("lease read failed")
				inv := checkInvoker(func(ctx context.Context, request dexec.ComponentRequest) (any, error) {
					if _, reading := request.Input.(*read.Input); reading {
						require.Empty(t, events)
						require.False(t, dexec.HasInvocationTransactionLookup(ctx))
						events = append(events, "check")
						row := leaseSnapshot(now, lease)
						switch mode {
						case "lost":
							token := "new-token"
							row.LeaseToken = &token
						case "expired":
							row.LeaseUntil = &now
						case "missing":
							row.LeaseKey = nil
						case "sql_error":
							return nil, failure
						}
						return &read.Output{Data: []*read.LeaseSnapshot{row}}, nil
					}
					require.Equal(t, []string{"check"}, events)
					_, err := maintenancelease.CheckedAt(ctx, lease)
					require.NoError(t, err)
					events = append(events, "mutation")
					switch request.Input.(type) {
					case *conversationmaintenance.Input:
						return &conversationmaintenance.Output{}, nil
					case *scheduledmaintenance.Input:
						return &scheduledmaintenance.Output{}, nil
					case *orphanmaintenance.ApplyInput:
						return &orphanmaintenance.ApplyOutput{Result: &orphanmaintenance.Result{}}, nil
					case *technicalmaintenance.Request:
						return &technicalmaintenance.Result{}, nil
					case *maintenancelease.PurgeInput:
						return &maintenancelease.PurgeOutput{}, nil
					}
					return nil, fmt.Errorf("unexpected input %T", request.Input)
				})
				ctx := context.Background()
				if mode == "managed" {
					ctx = dexec.WithInvocationTransactionLookup(ctx, func(context.Context, *sql.DB) (*sql.Tx, error) {
						t.Fatal("must not resolve inherited lookup")
						return nil, nil
					})
				}
				err := entry(ctx, inv)
				switch mode {
				case "live":
					require.NoError(t, err)
					require.Equal(t, []string{"check", "mutation"}, events)
				case "managed":
					require.ErrorIs(t, err, maintenancelease.ErrTransactionContext)
					require.Empty(t, events)
				case "sql_error":
					require.ErrorIs(t, err, failure)
					require.Equal(t, []string{"check"}, events)
				default:
					require.ErrorIs(t, err, maintenancelease.ErrLeaseLost)
					require.Equal(t, []string{"check"}, events)
				}
			})
		}
	}
}
