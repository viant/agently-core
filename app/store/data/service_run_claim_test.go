package data

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/viant/agently-core/internal/testutil/dbtest"
	agrunstale "github.com/viant/agently-core/pkg/agently/run/stale"
	agrunwrite "github.com/viant/agently-core/pkg/agently/run/write"
)

func seedForRunClaim(t *testing.T, db *sql.DB) {
	t.Helper()
	created := time.Date(2026, 1, 1, 9, 10, 0, 0, time.UTC)
	heartbeat := time.Date(2026, 1, 1, 9, 11, 0, 0, time.UTC)
	leaseUntil := time.Date(2026, 1, 1, 9, 12, 0, 0, time.UTC)
	items := []dbtest.ParameterizedSQL{
		{SQL: `INSERT INTO conversation (id, created_at) VALUES (?, ?)`, Params: []interface{}{"c-claim", created.Add(-10 * time.Minute)}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, status, run_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"run-claim", "c-claim", created, "running", "run-claim"}},
		{SQL: `INSERT INTO run (id, turn_id, conversation_id, status, attempt, lease_owner, lease_until, created_at, last_heartbeat_at, iteration, conversation_kind) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"run-claim", "run-claim", "c-claim", "running", 1, "dead-owner", leaseUntil, created, heartbeat, 1, "interactive"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, status, run_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"run-legacy", "c-claim", created, "running", "run-legacy"}},
		{SQL: `INSERT INTO run (id, turn_id, conversation_id, status, attempt, created_at, iteration, conversation_kind) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"run-legacy", "run-legacy", "c-claim", "running", 1, created, 1, "interactive"}},
	}
	dbtest.ExecAll(t, db, items)
}

func claimRow(runID, observedOwner, newOwner string, observedAttempt int, now time.Time) *agrunwrite.MutableRunView {
	row := &agrunwrite.MutableRunView{}
	row.SetId(runID)
	row.SetLeaseOwner(newOwner)
	row.SetLeaseUntil(now.Add(2 * time.Minute))
	row.SetLastHeartbeatAt(now)
	row.SetAttempt(observedAttempt + 1)
	cond := agrunwrite.RunPatchCondition{Status: "running", Attempt: &observedAttempt, LeaseOwner: &observedOwner}
	row.SetCondition(cond)
	return row
}

func TestDataService_ConditionalRunPatch_OneClaimWinner(t *testing.T) {
	svc := newSeededService(t, seedForRunClaim)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 9, 30, 0, 0, time.UTC)

	if _, err := svc.PatchRuns(ctx, []*agrunwrite.MutableRunView{claimRow("run-claim", "dead-owner", "pod-a", 1, now)}); err != nil {
		t.Fatalf("claim A error: %v", err)
	}
	run, err := svc.GetRun(ctx, "run-claim", nil)
	if err != nil || run == nil {
		t.Fatalf("GetRun() error: %v run=%v", err, run)
	}
	if run.LeaseOwner == nil || *run.LeaseOwner != "pod-a" || run.Attempt != 2 {
		t.Fatalf("claim A did not win: owner=%v attempt=%d", run.LeaseOwner, run.Attempt)
	}

	// Pod B observed the same stale state and races with the same criteria.
	if _, err := svc.PatchRuns(ctx, []*agrunwrite.MutableRunView{claimRow("run-claim", "dead-owner", "pod-b", 1, now)}); err != nil {
		t.Fatalf("claim B error: %v", err)
	}
	run, err = svc.GetRun(ctx, "run-claim", nil)
	if err != nil || run == nil {
		t.Fatalf("GetRun() error: %v", err)
	}
	if run.LeaseOwner == nil || *run.LeaseOwner != "pod-a" || run.Attempt != 2 {
		t.Fatalf("claim B must lose: owner=%v attempt=%d", run.LeaseOwner, run.Attempt)
	}

	// Owner-conditioned renewal by the loser is a no-op; by the winner it applies.
	loser := "pod-b"
	renew := &agrunwrite.MutableRunView{}
	renew.SetId("run-claim")
	renew.SetLastHeartbeatAt(now.Add(time.Minute))
	renew.SetLeaseUntil(now.Add(3 * time.Minute))
	renew.SetCondition(agrunwrite.RunPatchCondition{LeaseOwner: &loser})
	if _, err := svc.PatchRuns(ctx, []*agrunwrite.MutableRunView{renew}); err != nil {
		t.Fatalf("loser renew error: %v", err)
	}
	run, _ = svc.GetRun(ctx, "run-claim", nil)
	if run.LastHeartbeatAt == nil || !run.LastHeartbeatAt.Equal(time.Date(2026, 1, 1, 9, 30, 0, 0, time.UTC)) {
		t.Fatalf("loser renewal must not apply: heartbeat=%v", run.LastHeartbeatAt)
	}
	winner := "pod-a"
	finalize := &agrunwrite.MutableRunView{}
	finalize.SetId("run-claim")
	finalize.SetStatus("completed")
	finalize.SetCompletedAt(now.Add(2 * time.Minute))
	finalize.SetCondition(agrunwrite.RunPatchCondition{LeaseOwner: &winner})
	if _, err := svc.PatchRuns(ctx, []*agrunwrite.MutableRunView{finalize}); err != nil {
		t.Fatalf("winner finalize error: %v", err)
	}
	run, _ = svc.GetRun(ctx, "run-claim", nil)
	if run.Status != "completed" || run.CompletedAt == nil {
		t.Fatalf("winner finalize must apply: status=%q completed_at=%v", run.Status, run.CompletedAt)
	}

}

func TestDataService_ConditionalRunPatch_HeartbeatRenewalDefeatsStaleClaim(t *testing.T) {
	svc := newSeededService(t, seedForRunClaim)
	ctx := context.Background()
	renewedLeaseUntil := time.Date(2026, 1, 1, 9, 40, 0, 0, time.UTC)
	owner := "dead-owner"
	renewedOwner := "renewed-owner"
	renew := &agrunwrite.MutableRunView{}
	renew.SetId("run-claim")
	renew.SetLeaseOwner(renewedOwner)
	renew.SetLeaseUntil(renewedLeaseUntil)
	renew.SetLastHeartbeatAt(time.Date(2026, 1, 1, 9, 20, 0, 0, time.UTC))
	renew.SetCondition(agrunwrite.RunPatchCondition{LeaseOwner: &owner})
	if _, err := svc.PatchRuns(ctx, []*agrunwrite.MutableRunView{renew}); err != nil {
		t.Fatalf("renew error: %v", err)
	}
	if _, err := svc.PatchRuns(ctx, []*agrunwrite.MutableRunView{claimRow("run-claim", owner, "pod-a", 1, time.Date(2026, 1, 1, 9, 30, 0, 0, time.UTC))}); err != nil {
		t.Fatalf("stale claim error: %v", err)
	}
	run, err := svc.GetRun(ctx, "run-claim", nil)
	if err != nil || run == nil {
		t.Fatalf("GetRun error=%v run=%v", err, run)
	}
	if run.LeaseOwner == nil || *run.LeaseOwner != renewedOwner || run.Attempt != 1 || run.LeaseUntil == nil || !run.LeaseUntil.Equal(renewedLeaseUntil) {
		t.Fatalf("renewal must defeat stale claim: owner=%v attempt=%d lease=%v", run.LeaseOwner, run.Attempt, run.LeaseUntil)
	}
}

func seedForRootStaleView(t *testing.T, db *sql.DB) {
	t.Helper()
	current := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	created := current.Add(-50 * time.Minute)
	heartbeat := current.Add(-49 * time.Minute)
	expired := current.Add(-48 * time.Minute)
	old := time.Date(2025, 12, 1, 9, 10, 0, 0, time.UTC)
	items := []dbtest.ParameterizedSQL{
		{SQL: `INSERT INTO conversation (id, created_at) VALUES (?, ?)`, Params: []interface{}{"c-root", created.Add(-10 * time.Minute)}},
		{SQL: `INSERT INTO conversation (id, created_at, conversation_parent_id) VALUES (?, ?, ?)`, Params: []interface{}{"c-child", created.Add(-10 * time.Minute), "c-root"}},
		{SQL: `INSERT INTO schedule (id, name, agent_ref, enabled, timezone) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"sch-1", "schedule-1", "agent-1", 1, "UTC"}},
		{SQL: `INSERT INTO run (id, conversation_id, status, lease_owner, lease_until, created_at, last_heartbeat_at, iteration, conversation_kind) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"r-root-expired", "c-root", "running", "dead", expired, created, heartbeat, 1, "interactive"}},
		{SQL: `INSERT INTO run (id, conversation_id, status, created_at, last_heartbeat_at, iteration, conversation_kind) VALUES (?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"r-root-legacy", "c-root", "running", created, heartbeat, 1, "interactive"}},
		{SQL: `INSERT INTO run (id, conversation_id, status, lease_owner, lease_until, created_at, last_heartbeat_at, iteration, conversation_kind) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"r-root-live", "c-root", "running", "alive", current.Add(13 * time.Hour), created, heartbeat, 1, "interactive"}},
		{SQL: `INSERT INTO run (id, conversation_id, status, lease_until, created_at, last_heartbeat_at, iteration, conversation_kind) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"r-child", "c-child", "running", expired, created, heartbeat, 1, "interactive"}},
		{SQL: `INSERT INTO run (id, conversation_id, schedule_id, status, lease_until, created_at, last_heartbeat_at, iteration, conversation_kind) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"r-scheduled", "c-root", "sch-1", "running", expired, created, heartbeat, 1, "scheduled"}},
		{SQL: `INSERT INTO run (id, conversation_id, status, lease_until, created_at, last_heartbeat_at, iteration, conversation_kind) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"r-old", "c-root", "running", old.Add(2 * time.Minute), old, old.Add(time.Minute), 1, "interactive"}},
	}
	dbtest.ExecAll(t, db, items)
}

func TestDataService_StaleRuns_RootInteractiveExpiredWithinLookback(t *testing.T) {
	svc := newSeededService(t, seedForRootStaleView)
	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	rows, err := svc.ListStaleRuns(context.Background(), &agrunstale.StaleRunsInput{
		HeartbeatBefore:    now.Add(-2 * time.Minute),
		LeaseExpiredBefore: now,
		ActivityAfter:      now.Add(-24 * time.Hour),
		ConversationKind:   "interactive",
		RootInteractive:    true,
		Has:                &agrunstale.StaleRunsInputHas{HeartbeatBefore: true, LeaseExpiredBefore: true, ActivityAfter: true, ConversationKind: true, RootInteractive: true},
	})
	if err != nil {
		t.Fatalf("ListStaleRuns() error: %v", err)
	}
	got := map[string]bool{}
	for _, row := range rows {
		got[row.Id] = true
	}
	for _, want := range []string{"r-root-expired"} {
		if !got[want] {
			t.Fatalf("expected %s in stale view, got %v", want, got)
		}
	}
	for _, excluded := range []string{"r-root-legacy", "r-root-live", "r-child", "r-scheduled", "r-old"} {
		if got[excluded] {
			t.Fatalf("%s must be excluded from root recovery view, got %v", excluded, got)
		}
	}
}
