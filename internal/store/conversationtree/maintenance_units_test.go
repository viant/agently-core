package conversationtree

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	eventread "github.com/viant/agently-core/internal/datly/agui/cleanup/event/read"
	leaseread "github.com/viant/agently-core/internal/datly/maintenancelease/read"
	lease "github.com/viant/agently-core/internal/store/maintenancelease"
	"github.com/viant/agently-core/internal/store/maintenancetx"
	dexec "github.com/viant/datly/exec"
	xhandler "github.com/viant/xdatly/handler"
)

type unitTestInvoker func(context.Context, dexec.ComponentRequest) (any, error)

func (f unitTestInvoker) InvokeComponent(ctx context.Context, r dexec.ComponentRequest) (any, error) {
	return f(ctx, r)
}
func (unitTestInvoker) ConfiguredDriver(context.Context, string) (string, error) { return "mysql", nil }

func unitTestPlan(n int) *DeletePlan {
	plan := &DeletePlan{Graph: &Graph{Nodes: map[string]*Node{"root": {ID: "root"}}}, Tables: map[string]bool{}, ConversationIDs: []string{"root"}}
	for i := n - 1; i >= 0; i-- {
		id, run, thread := fmt.Sprintf("event-%04d", i), "protocol-run", "root"
		plan.AGUIEvents = append(plan.AGUIEvents, &eventread.Event{EventKey: &id, RunKey: &run, ThreadId: &thread})
		plan.PayloadIDs = append(plan.PayloadIDs, fmt.Sprintf("payload-%04d", i))
	}
	return plan
}

func TestMaintenanceUnitsBoundedOrderedAndPrivate(t *testing.T) {
	for _, n := range []int{399, 400, 401} {
		plan := unitTestPlan(n)
		units, err := maintenanceUnits(plan)
		require.NoError(t, err)
		batchCount := (n + 399) / 400
		require.Len(t, units, batchCount*2+1)
		for i, unit := range units {
			require.LessOrEqual(t, len(unit.events), 400)
			require.LessOrEqual(t, len(unit.payloads), 400)
			switch {
			case i < batchCount:
				require.Equal(t, eventUnit, unit.kind)
			case i == batchCount:
				require.Equal(t, metadataUnit, unit.kind)
			default:
				require.Equal(t, payloadUnit, unit.kind)
			}
		}
		require.Equal(t, "event-0000", *units[0].events[0].EventKey)
		require.Equal(t, "payload-0000", units[batchCount+1].payloads[0])
		ctx, capture := WithMaintenancePlanCapture(context.Background())
		require.False(t, CaptureMaintenancePlan(ctx, plan), "manual/plain context cannot capture")
		plan.PayloadIDs = nil
		_, _, err = maintenancetx.Invoke(ctx, unitTestInvoker(func(ctx context.Context, r dexec.ComponentRequest) (any, error) {
			require.Equal(t, n > 400, CaptureMaintenancePlan(ctx, plan))
			return nil, nil
		}), func(ctx context.Context) (context.Context, error) { return ctx, nil }, dexec.ComponentRequest{})
		require.NoError(t, err)
		require.Equal(t, n > 400, capture.HasPlan())
		capture.Reset()
		require.False(t, capture.HasPlan())
	}
	ctx := context.Background()
	err := (&MaintenanceUnit{}).Exec(ctx, nil, &MaintenanceUnitInput{}, &MaintenanceUnitOutput{})
	require.ErrorContains(t, err, "private maintenance plan")
	plan := unitTestPlan(1)
	*plan.AGUIEvents[0].ThreadId = "foreign"
	_, err = maintenanceUnits(plan)
	require.ErrorContains(t, err, "scope mismatch")
}

func TestMaintenanceUnitsRetryOnlyCurrentKnownRollback(t *testing.T) {
	for _, state := range []xhandler.TransactionState{xhandler.TransactionRolledBack, xhandler.TransactionCommitUnknown, xhandler.TransactionCommitted} {
		t.Run(string(state), func(t *testing.T) {
			now := time.Now().UTC()
			ownership := lease.Lease{Key: "cleanup", OwnerID: "worker", Token: "token", LeaseUntil: now.Add(time.Hour)}
			plan := unitTestPlan(401)
			plan.PayloadIDs = nil
			capture := &PlanCapture{plan: plan}
			calls := map[string]int{}
			checks, units := 0, 0
			fake := unitTestInvoker(func(ctx context.Context, r dexec.ComponentRequest) (any, error) {
				if r.Target.Route.Method == "GET" {
					checks++
					require.False(t, dexec.HasInvocationTransactionLookup(ctx))
					return &leaseread.Output{Data: []*leaseread.LeaseSnapshot{{DbNow: now.Format(time.RFC3339Nano), LeaseKey: &ownership.Key, OwnerId: &ownership.OwnerID, LeaseToken: &ownership.Token, LeaseUntil: &ownership.LeaseUntil}}}, nil
				}
				unit := ctx.Value(unitKey{}).(*maintenanceUnit)
				id := "metadata"
				if unit.kind == eventUnit {
					id = *unit.events[0].EventKey
				}
				units++
				calls[id]++
				require.Equal(t, checks, units, "every attempt needs a fresh standalone lease")
				_, err := lease.CheckedAt(ctx, ownership)
				require.NoError(t, err)
				if id == "event-0400" && calls[id] == 1 {
					failure := &mysql.MySQLError{Number: 1213}
					maintenancetx.RecordImmediate(ctx, "call_payload", 1)
					r.Completion(xhandler.Outcome{Error: failure, Transactions: []xhandler.TransactionOutcome{{Unit: 1, State: state}}})
					return nil, failure
				}
				if unit.kind == eventUnit {
					maintenancetx.RecordImmediate(ctx, "call_payload", int64(len(unit.events)))
				} else {
					maintenancetx.RecordImmediate(ctx, "conversation", 1)
				}
				r.Completion(xhandler.Outcome{Transactions: []xhandler.TransactionOutcome{{Unit: 1, State: xhandler.TransactionCommitted}}})
				return &MaintenanceUnitOutput{}, nil
			})
			progress, err := capture.Apply(context.Background(), fake, ownership)
			require.Equal(t, 1, calls["event-0000"], "never replay a committed predecessor")
			if state == xhandler.TransactionRolledBack {
				require.NoError(t, err)
				require.False(t, progress.Partial)
				require.Equal(t, 2, calls["event-0400"])
				require.Equal(t, 1, calls["metadata"])
				require.Equal(t, 3, progress.CommittedBatches)
				require.EqualValues(t, 402, progress.AffectedRows)
			} else {
				require.Error(t, err)
				require.True(t, progress.Partial)
				require.Equal(t, 1, calls["event-0400"])
				require.Zero(t, calls["metadata"])
				want := int64(400)
				batches := 1
				if state == xhandler.TransactionCommitted {
					want, batches = 401, 2
				}
				require.Equal(t, batches, progress.CommittedBatches)
				require.EqualValues(t, want, progress.AffectedRows)
			}
		})
	}
}

func TestMaintenanceProgressExcludesNoops(t *testing.T) {
	p := MaintenanceProgress{}
	p.Add(maintenancetx.Summary{})
	require.Zero(t, p.CommittedBatches)
	p.Add(maintenancetx.Summary{AffectedRows: 5, DeletedRows: 3, DeletedByTable: map[string]int64{"conversation": 2}})
	require.Equal(t, 1, p.CommittedBatches)
	require.Equal(t, 2, p.ConversationCount)
}
