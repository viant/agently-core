package data

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	dexec "github.com/viant/datly/exec"
)

// A wrapper observes only completed, top-level unit commits. The component's
// nested readers/writers still use the real runtime and real transactions.
type cleanupUnitObserver struct {
	dexec.ComponentInvoker
	after func(int)
	units int
}

func (o *cleanupUnitObserver) ConfiguredDriver(ctx context.Context, connector string) (string, error) {
	return o.ComponentInvoker.(interface {
		ConfiguredDriver(context.Context, string) (string, error)
	}).ConfiguredDriver(ctx, connector)
}
func (o *cleanupUnitObserver) InvokeComponent(ctx context.Context, request dexec.ComponentRequest) (any, error) {
	value, err := o.ComponentInvoker.InvokeComponent(ctx, request)
	if request.Target.Route.Path == "/v1/internal/agently/conversation/maintenance-unit" && err == nil {
		o.units++
		if o.after != nil {
			o.after(o.units)
		}
	}
	return value, err
}

func oldAGUIRequest(t *testing.T, svc Service, db *sql.DB) ConversationMaintenanceRequest {
	t.Helper()
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err := db.Exec(`UPDATE conversation SET created_at=?, updated_at=?, last_activity=? WHERE id IN ('agui-root','agui-child')`, old, old, old)
	require.NoError(t, err)
	return ConversationMaintenanceRequest{RootID: "agui-root", Kind: ConversationMaintenanceInteractive, InactiveBefore: old.Add(time.Hour), Mode: ConversationMaintenanceDelete, Lease: acquireTestMaintenanceLease(t, svc)}
}

func countCleanupEvents(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM call_payload p JOIN run r ON r.id=p.run_id WHERE r.conversation_id IN ('agui-root','agui-child') AND p.kind='agui.event'`).Scan(&count))
	return count
}

func TestMaintainConversationTree_UnitBoundaryRollback(t *testing.T) {
	for _, n := range []int{399, 400, 401} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			svc, db, store := aguiDeleteFixture(t)
			aguiDeleteSeedRun(t, store, "agui-root", "u1", n-2, true)
			req := oldAGUIRequest(t, svc, db)
			_, err := db.Exec(`CREATE TRIGGER cleanup_core_fail BEFORE DELETE ON conversation WHEN OLD.id='agui-root' BEGIN SELECT RAISE(ABORT,'forced core rollback'); END`)
			require.NoError(t, err)
			out, err := svc.MaintainConversationTree(context.Background(), req)
			require.ErrorContains(t, err, "forced core rollback")
			if n <= 400 {
				require.Nil(t, out)
				require.Equal(t, n, countCleanupEvents(t, db))
			} else {
				require.NotNil(t, out)
				require.True(t, out.Partial)
				require.True(t, out.Eligible)
				require.False(t, out.Deleted)
				require.Equal(t, 2, out.CommittedBatches)
				require.EqualValues(t, 401, out.AffectedRows)
				require.Zero(t, out.ConversationCount)
				require.Zero(t, countCleanupEvents(t, db))
			}
			for _, id := range []string{"agui-root", "agui-child"} {
				assertStage1RowCount(t, db, "conversation", "id", id, 1)
			}
			_, err = db.Exec(`DROP TRIGGER cleanup_core_fail`)
			require.NoError(t, err)
			out, err = svc.MaintainConversationTree(context.Background(), req)
			require.NoError(t, err)
			require.True(t, out.Deleted)
			require.False(t, out.Partial)
			require.Equal(t, 2, out.ConversationCount)
			assertStage1RowCount(t, db, "conversation", "id", "agui-root", 0)
			assertStage1RowCount(t, db, "conversation", "id", "agui-child", 0)
			assertStage1RowCount(t, db, "call_payload", "id", "unrelated-payload", 1)
		})
	}
}

func TestMaintainConversationTree_UnitLeaseLossAndReactivation(t *testing.T) {
	for _, failure := range []string{"lease", "cancel", "event"} {
		t.Run(failure, func(t *testing.T) {
			svc, db, store := aguiDeleteFixture(t)
			aguiDeleteSeedRun(t, store, "agui-root", "u1", 399, true)
			req := oldAGUIRequest(t, svc, db)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var last string
			require.NoError(t, db.QueryRow(`SELECT MAX(p.id) FROM call_payload p JOIN run r ON r.id=p.run_id WHERE r.conversation_id='agui-root' AND p.kind='agui.event'`).Scan(&last))
			if failure == "event" {
				_, err := db.Exec(fmt.Sprintf(`CREATE TRIGGER cleanup_event_fail BEFORE DELETE ON call_payload WHEN OLD.id='%s' BEGIN SELECT RAISE(ABORT,'forced event rollback'); END`, last))
				require.NoError(t, err)
			}
			native := svc.(*datlyService)
			original := native.native
			observer := &cleanupUnitObserver{ComponentInvoker: original, after: func(n int) {
				if n != 1 {
					return
				}
				switch failure {
				case "lease":
					_, err := db.Exec(`UPDATE maintenance_lease SET owner_id='other-worker' WHERE lease_key=?`, req.Lease.Key)
					require.NoError(t, err)
				case "cancel":
					cancel()
				}
			}}
			native.native = observer
			out, err := svc.MaintainConversationTree(ctx, req)
			require.Error(t, err)
			if failure == "lease" {
				require.ErrorIs(t, err, ErrMaintenanceLeaseLost)
			} else if failure == "cancel" {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorContains(t, err, "forced event rollback")
			}
			require.NotNil(t, out)
			require.True(t, out.Partial)
			require.False(t, out.Deleted)
			require.Equal(t, 1, out.CommittedBatches)
			require.EqualValues(t, 400, out.AffectedRows)
			require.Equal(t, 1, observer.units)
			require.Equal(t, 1, countCleanupEvents(t, db))
			assertStage1RowCount(t, db, "conversation", "id", "agui-root", 1)
			assertStage1RowCount(t, db, "conversation", "id", "agui-child", 1)
			native.native = original
			_, err = db.Exec(`UPDATE conversation SET last_activity=? WHERE id='agui-root'`, time.Now().UTC())
			require.NoError(t, err)
			if failure == "lease" {
				req.Lease = acquireTestMaintenanceLease(t, svc, "resume-cleanup", "resume-worker")
			}
			out, err = svc.MaintainConversationTree(context.Background(), req)
			require.NoError(t, err)
			require.Equal(t, ConversationMaintenanceRecentActivity, out.Reason)
			require.False(t, out.Deleted)
			require.Zero(t, out.AffectedRows)
			require.Equal(t, 1, countCleanupEvents(t, db))
			if failure == "event" {
				_, err = db.Exec(`DROP TRIGGER cleanup_event_fail`)
				require.NoError(t, err)
			}
			_, err = db.Exec(`UPDATE conversation SET last_activity=created_at WHERE id='agui-root'`)
			require.NoError(t, err)
			out, err = svc.MaintainConversationTree(context.Background(), req)
			require.NoError(t, err)
			require.True(t, out.Deleted)
		})
	}
}

func TestMaintainConversationTree_UnitPayloadFailureAfterMetadataCommit(t *testing.T) {
	svc, db, _ := aguiDeleteFixture(t)
	req := oldAGUIRequest(t, svc, db)
	tx, err := db.Begin()
	require.NoError(t, err)
	defer tx.Rollback()
	for i := 0; i < 401; i++ {
		id := fmt.Sprintf("unit-payload-%03d", i)
		_, err = tx.Exec(`INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage,inline_body,compression,created_at) VALUES(?,'attachment','text/plain',4,'inline','body','none',?)`, id, time.Now().UTC().Add(-60*24*time.Hour))
		require.NoError(t, err)
		_, err = tx.Exec(`INSERT INTO message(id,conversation_id,turn_id,role,type,attachment_payload_id) VALUES(?,'agui-root','root-turn','assistant','text',?)`, fmt.Sprintf("unit-message-%03d", i), id)
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())
	_, err = db.Exec(`CREATE TRIGGER cleanup_payload_fail BEFORE DELETE ON call_payload WHEN OLD.id='unit-payload-400' BEGIN SELECT RAISE(ABORT,'forced final payload rollback'); END`)
	require.NoError(t, err)
	out, err := svc.MaintainConversationTree(context.Background(), req)
	require.ErrorContains(t, err, "forced final payload rollback")
	require.NotNil(t, out)
	require.True(t, out.Partial)
	require.False(t, out.Deleted, "a failed tree operation must not report full deletion")
	require.Equal(t, 2, out.ConversationCount, "the metadata transaction really committed")
	require.Equal(t, 2, out.CommittedBatches)
	// Explicit DML: 401 messages, 2 turns, 2 conversations, 400 payloads.
	require.EqualValues(t, 805, out.AffectedRows)
	assertStage1RowCount(t, db, "conversation", "id", "agui-root", 0)
	assertStage1RowCount(t, db, "call_payload", "id", "unit-payload-000", 0)
	assertStage1RowCount(t, db, "call_payload", "id", "unit-payload-400", 1)
	_, err = db.Exec(`DROP TRIGGER cleanup_payload_fail`)
	require.NoError(t, err)
	// Root-less leftovers use the existing unused-payload retention rule, not
	// a new broadened orphan deletion rule.
	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour)
	candidates, err := svc.ListOrphanMaintenanceCandidates(context.Background(), OrphanMaintenanceCandidateRequest{OlderThan: cutoff, Limit: 1000})
	require.NoError(t, err)
	found := false
	for _, candidate := range candidates {
		if candidate.RecordID != "unit-payload-400" {
			continue
		}
		found = true
		require.Equal(t, "call_payload.unused", candidate.RuleID)
		require.Equal(t, OrphanMaintenanceSafeDelete, candidate.Action)
		result, err := svc.MaintainOrphanCandidate(context.Background(), OrphanMaintenanceRequest{RuleID: candidate.RuleID, RecordID: candidate.RecordID, OlderThan: cutoff, Lease: req.Lease})
		require.NoError(t, err)
		require.True(t, result.Deleted)
	}
	require.True(t, found, "existing orphan scan must find the committed tree's leftover")
	assertStage1RowCount(t, db, "call_payload", "id", "unit-payload-400", 0)
}

func TestMaintainConversationTree_UnitProtectsNewDiscoveryEdges(t *testing.T) {
	for _, edge := range []string{"parent", "parent-turn", "outgoing-link", "incoming-link"} {
		t.Run(edge, func(t *testing.T) {
			svc, db, store := aguiDeleteFixture(t)
			aguiDeleteSeedRun(t, store, "agui-root", "u1", 399, true)
			req := oldAGUIRequest(t, svc, db)
			native := svc.(*datlyService)
			observer := &cleanupUnitObserver{ComponentInvoker: native.native, after: func(n int) {
				if n != 1 {
					return
				}
				statement := map[string]string{
					"parent":        `INSERT INTO conversation(id,conversation_parent_id) VALUES('late-child','agui-root')`,
					"parent-turn":   `INSERT INTO conversation(id,conversation_parent_turn_id) VALUES('late-child','root-turn')`,
					"outgoing-link": `INSERT INTO message(id,conversation_id,turn_id,role,type,linked_conversation_id) VALUES('late-link','agui-root','root-turn','assistant','text','agui-foreign')`,
					"incoming-link": `INSERT INTO message(id,conversation_id,turn_id,role,type,linked_conversation_id) VALUES('late-link','agui-foreign','foreign-turn','assistant','text','agui-root')`,
				}[edge]
				_, err := db.Exec(statement)
				require.NoError(t, err)
			}}
			native.native = observer
			out, err := svc.MaintainConversationTree(context.Background(), req)
			require.ErrorIs(t, err, ErrConversationGraphReferenced)
			require.NotNil(t, out)
			require.True(t, out.Partial)
			require.False(t, out.Deleted)
			require.Equal(t, 2, out.CommittedBatches)
			require.EqualValues(t, 401, out.AffectedRows)
			assertStage1RowCount(t, db, "conversation", "id", "agui-root", 1)
			assertStage1RowCount(t, db, "conversation", "id", "agui-child", 1)
		})
	}
}

func TestMaintainConversationTree_UnitsKeepLinkedOnlyCyclesOnFailure(t *testing.T) {
	svc, db, store := aguiDeleteFixture(t)
	aguiDeleteSeedRun(t, store, "agui-root", "u1", 399, true)
	req := oldAGUIRequest(t, svc, db)
	_, err := db.Exec(`UPDATE conversation SET conversation_parent_id=NULL WHERE id='agui-child';
INSERT INTO message(id,conversation_id,turn_id,role,type,linked_conversation_id) VALUES
 ('root-link','agui-root','root-turn','assistant','text','agui-child'),
 ('child-link','agui-child','child-turn','assistant','text','agui-root');
CREATE TRIGGER cleanup_cycle_fail BEFORE DELETE ON conversation WHEN OLD.id='agui-root' BEGIN SELECT RAISE(ABORT,'forced cycle rollback'); END;`)
	require.NoError(t, err)
	out, err := svc.MaintainConversationTree(context.Background(), req)
	require.ErrorContains(t, err, "forced cycle rollback")
	require.True(t, out.Partial)
	for _, id := range []string{"root-link", "child-link"} {
		assertStage1RowCount(t, db, "message", "id", id, 1)
	}
	_, err = db.Exec(`DROP TRIGGER cleanup_cycle_fail`)
	require.NoError(t, err)
	out, err = svc.MaintainConversationTree(context.Background(), req)
	require.NoError(t, err)
	require.True(t, out.Deleted)
	require.Equal(t, 2, out.ConversationCount)
	assertStage1RowCount(t, db, "conversation", "id", "agui-child", 0)
}

func TestMaintainConversationTree_UnitsTolerateOverlappingWorker(t *testing.T) {
	svc, db, store := aguiDeleteFixture(t)
	aguiDeleteSeedRun(t, store, "agui-root", "u1", 399, true)
	req := oldAGUIRequest(t, svc, db)
	native := svc.(*datlyService)
	original := native.native
	other := NewService(original)
	observer := &cleanupUnitObserver{ComponentInvoker: original, after: func(n int) {
		if n == 1 {
			out, err := other.MaintainConversationTree(context.Background(), req)
			require.NoError(t, err)
			require.True(t, out.Deleted)
		}
	}}
	native.native = observer
	out, err := svc.MaintainConversationTree(context.Background(), req)
	require.NoError(t, err)
	require.False(t, out.Deleted, "do not take credit for the other worker's deletion")
	require.False(t, out.Partial)
	require.Equal(t, 1, out.CommittedBatches)
	require.EqualValues(t, 400, out.AffectedRows)
	assertStage1RowCount(t, db, "conversation", "id", "agui-root", 0)
	assertStage1RowCount(t, db, "conversation", "id", "agui-child", 0)
}

func TestMaintainConversationTree_UnitsKeepNewSharedPayloadReference(t *testing.T) {
	svc, db, _ := aguiDeleteFixture(t)
	req := oldAGUIRequest(t, svc, db)
	tx, err := db.Begin()
	require.NoError(t, err)
	defer tx.Rollback()
	for i := 0; i < 401; i++ {
		id := fmt.Sprintf("shared-unit-%03d", i)
		_, err = tx.Exec(`INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage,inline_body,compression) VALUES(?,'attachment','text/plain',4,'inline','body','none')`, id)
		require.NoError(t, err)
		_, err = tx.Exec(`INSERT INTO message(id,conversation_id,turn_id,role,type,attachment_payload_id) VALUES(?,'agui-root','root-turn','assistant','text',?)`, id, id)
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())
	native := svc.(*datlyService)
	native.native = &cleanupUnitObserver{ComponentInvoker: native.native, after: func(n int) {
		if n == 1 { // metadata committed, payload units have not started
			_, err := db.Exec(`INSERT INTO message(id,conversation_id,turn_id,role,type,attachment_payload_id) VALUES('new-shared-reference','agui-foreign','foreign-turn','assistant','text','shared-unit-400')`)
			require.NoError(t, err)
		}
	}}
	out, err := svc.MaintainConversationTree(context.Background(), req)
	require.NoError(t, err)
	require.True(t, out.Deleted)
	require.False(t, out.Partial)
	require.Equal(t, 2, out.CommittedBatches, "a guarded no-op is not a mutation batch")
	require.EqualValues(t, 805, out.AffectedRows)
	assertStage1RowCount(t, db, "conversation", "id", "agui-root", 0)
	assertStage1RowCount(t, db, "message", "id", "new-shared-reference", 1)
	assertStage1RowCount(t, db, "call_payload", "id", "shared-unit-400", 1)
	assertStage1RowCount(t, db, "call_payload", "id", "shared-unit-000", 0)
}

func TestMaintainConversationTree_UnitsKeepEventMovedOutsideScope(t *testing.T) {
	svc, db, store := aguiDeleteFixture(t)
	aguiDeleteSeedRun(t, store, "agui-root", "u1", 399, true)
	foreign := aguiDeleteSeedRun(t, store, "agui-foreign", "u2", 0, true)
	req := oldAGUIRequest(t, svc, db)
	var last string
	require.NoError(t, db.QueryRow(`SELECT MAX(p.id) FROM call_payload p JOIN run r ON r.id=p.run_id WHERE r.conversation_id='agui-root' AND p.kind='agui.event'`).Scan(&last))
	native := svc.(*datlyService)
	native.native = &cleanupUnitObserver{ComponentInvoker: native.native, after: func(n int) {
		if n == 1 {
			_, err := db.Exec(`UPDATE call_payload SET run_id=? WHERE id=?`, foreign.RecordID, last)
			require.NoError(t, err)
		}
	}}
	out, err := svc.MaintainConversationTree(context.Background(), req)
	require.NoError(t, err)
	require.True(t, out.Deleted)
	require.False(t, out.Partial)
	assertStage1RowCount(t, db, "call_payload", "id", last, 1)
	assertStage1RowCount(t, db, "run", "id", foreign.RecordID, 1)
	assertStage1RowCount(t, db, "conversation", "id", "agui-foreign", 1)
}

func TestMaintainScheduledRun_UnitsPreservePartialProgress(t *testing.T) {
	svc, db, store := aguiDeleteFixture(t)
	aguiDeleteSeedRun(t, store, "agui-root", "u1", 399, true)
	req := oldAGUIRequest(t, svc, db)
	_, err := db.Exec(`INSERT INTO schedule(id,name,agent_ref) VALUES('unit-schedule','unit','agent');
INSERT INTO run(id,schedule_id,conversation_id,conversation_kind,status) VALUES('unit-scheduled-run','unit-schedule','agui-root','scheduled','completed');
UPDATE conversation SET schedule_id='unit-schedule',schedule_run_id='unit-scheduled-run' WHERE id='agui-root';
CREATE TRIGGER cleanup_scheduled_fail BEFORE DELETE ON conversation WHEN OLD.id='agui-root' BEGIN SELECT RAISE(ABORT,'forced scheduled rollback'); END;`)
	require.NoError(t, err)
	request := ScheduledRunMaintenanceRequest{RunID: "unit-scheduled-run", InactiveBefore: req.InactiveBefore, Mode: ConversationMaintenanceDelete, Lease: req.Lease}
	out, err := svc.MaintainScheduledRun(context.Background(), request)
	require.ErrorContains(t, err, "forced scheduled rollback")
	require.NotNil(t, out)
	require.True(t, out.Partial)
	require.False(t, out.Deleted)
	require.Equal(t, 2, out.CommittedBatches)
	require.EqualValues(t, 401, out.AffectedRows)
	assertStage1RowCount(t, db, "run", "id", "unit-scheduled-run", 1)
	assertStage1RowCount(t, db, "conversation", "id", "agui-root", 1)
	_, err = db.Exec(`DROP TRIGGER cleanup_scheduled_fail`)
	require.NoError(t, err)
	out, err = svc.MaintainScheduledRun(context.Background(), request)
	require.NoError(t, err)
	require.True(t, out.Deleted)
	assertStage1RowCount(t, db, "schedule", "id", "unit-schedule", 1)
}
