package data

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestMaintainOrphanCandidate_SQLiteRechecksMutatesAndIsIdempotent(t *testing.T) {
	svc, db := newSeededServiceWithDB(t, seedSQLiteOrphanMaintenanceFixtures)
	ctx := context.Background()
	lease := acquireTestMaintenanceLease(t, svc)
	cutoff := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	candidates, err := svc.ListOrphanMaintenanceCandidates(ctx, OrphanMaintenanceCandidateRequest{OlderThan: cutoff, Limit: 100})
	if err != nil {
		t.Fatalf("dry-run report: %v", err)
	}
	if len(candidates) != 5 {
		t.Fatalf("dry-run candidates = %d, want 5: %#v", len(candidates), candidates)
	}
	t.Logf("SQLite orphan dry-run: candidates=%d", len(candidates))
	assertOrphanCandidateDependencyOrder(t, candidates)

	// This parent appears after the report but before maintenance. The
	// transactional recheck must preserve the now-valid reference.
	if _, err = db.Exec(`INSERT INTO conversation (id, created_at, updated_at, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`,
		"missing-linked-conversation", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "succeeded", "owner-1"); err != nil {
		t.Fatalf("restore parent after dry-run: %v", err)
	}

	counts := map[OrphanMaintenanceReason]int{}
	for _, candidate := range candidates {
		result, maintainErr := svc.MaintainOrphanCandidate(ctx, OrphanMaintenanceRequest{
			RuleID: candidate.RuleID, RecordID: candidate.RecordID, OlderThan: cutoff, Lease: lease,
		})
		if maintainErr != nil {
			t.Fatalf("maintain rule=%s record=%s: %v", candidate.RuleID, candidate.RecordID, maintainErr)
		}
		counts[result.Reason]++
		switch result.Reason {
		case OrphanMaintenanceDeletedReason:
			if !result.Eligible || !result.Mutated || !result.Deleted || result.Detached {
				t.Fatalf("delete result = %#v", result)
			}
		case OrphanMaintenanceDetachedReason:
			if !result.Eligible || !result.Mutated || result.Deleted || !result.Detached {
				t.Fatalf("detach result = %#v", result)
			}
		case OrphanMaintenanceReportOnlyReason:
			if !result.Eligible || result.Mutated || result.Deleted || result.Detached {
				t.Fatalf("report-only result = %#v", result)
			}
		case OrphanMaintenanceNoLongerEligibleReason:
			if result.Eligible || result.Mutated {
				t.Fatalf("recovered result = %#v", result)
			}
		default:
			t.Fatalf("unexpected result = %#v", result)
		}
	}
	wantCounts := map[OrphanMaintenanceReason]int{
		OrphanMaintenanceDeletedReason:          3,
		OrphanMaintenanceDetachedReason:         1,
		OrphanMaintenanceNoLongerEligibleReason: 1,
	}
	for reason, want := range wantCounts {
		if counts[reason] != want {
			t.Fatalf("result count %s = %d, want %d; all=%v", reason, counts[reason], want, counts)
		}
	}
	t.Logf("SQLite orphan maintenance: results=%v", counts)

	assertSQLiteOrphanMaintenanceEffects(t, db)
	after, err := svc.ListOrphanMaintenanceCandidates(ctx, OrphanMaintenanceCandidateRequest{OlderThan: cutoff, Limit: 100})
	if err != nil {
		t.Fatalf("post-maintenance report: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("post-maintenance candidates = %#v", after)
	}

	result, err := svc.MaintainOrphanCandidate(ctx, OrphanMaintenanceRequest{
		RuleID: "call_payload.unused", RecordID: "payload-unused-old", OlderThan: cutoff, Lease: lease,
	})
	if err != nil {
		t.Fatalf("idempotent maintenance: %v", err)
	}
	if result.Reason != OrphanMaintenanceNoLongerEligibleReason || result.Mutated {
		t.Fatalf("idempotent result = %#v", result)
	}
	result, err = svc.MaintainOrphanCandidate(ctx, OrphanMaintenanceRequest{
		RuleID: "call_payload.unused", RecordID: "payload-unused-recent", OlderThan: cutoff, Lease: lease,
	})
	if err != nil {
		t.Fatalf("grace-period maintenance: %v", err)
	}
	if result.Reason != OrphanMaintenanceNoLongerEligibleReason || result.Mutated {
		t.Fatalf("grace-period result = %#v", result)
	}
}

func TestMaintainOrphanCandidate_ValidatesRuleAndCompositeKey(t *testing.T) {
	svc := newSeededService(t)
	cutoff := time.Now().UTC()
	lease := acquireTestMaintenanceLease(t, svc)
	for _, request := range []OrphanMaintenanceRequest{
		{RecordID: "record", OlderThan: cutoff, Lease: lease},
		{RuleID: "unknown", RecordID: "record", OlderThan: cutoff, Lease: lease},
		{RuleID: "conversation_report_context.missing_conversation", RecordID: "one-part", OlderThan: cutoff, Lease: lease},
	} {
		if _, err := svc.MaintainOrphanCandidate(context.Background(), request); err == nil {
			t.Fatalf("request %#v unexpectedly succeeded", request)
		}
	}
}

func TestMaintainOrphanCandidate_RejectsSupersededLeaseWithoutMutating(t *testing.T) {
	svc, db := newSeededServiceWithDB(t, seedSQLiteOrphanMaintenanceFixtures)
	stale := acquireTestMaintenanceLease(t, svc, "orphan-fence", "worker-a")
	released, err := svc.ReleaseMaintenanceLease(context.Background(), stale)
	if err != nil || !released {
		t.Fatalf("release stale lease: released=%t err=%v", released, err)
	}
	_ = acquireTestMaintenanceLease(t, svc, "orphan-fence", "worker-b")

	_, err = svc.MaintainOrphanCandidate(context.Background(), OrphanMaintenanceRequest{
		RuleID:    "call_payload.unused",
		RecordID:  "payload-unused-old",
		OlderThan: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		Lease:     stale,
	})
	if !errors.Is(err, ErrMaintenanceLeaseLost) {
		t.Fatalf("stale lease error = %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM call_payload WHERE id = ?`, "payload-unused-old").Scan(&count); err != nil || count != 1 {
		t.Fatalf("fenced orphan row count=%d err=%v", count, err)
	}
}

func assertOrphanCandidateDependencyOrder(t *testing.T, candidates []OrphanMaintenanceCandidate) {
	t.Helper()
	lastPriority := 0
	for _, candidate := range candidates {
		capabilities, err := deleteSchemaCapabilitiesForDriver("sqlite")
		if err != nil {
			t.Fatal(err)
		}
		rule := orphanRuleByID(orphanMaintenanceRules(capabilities), candidate.RuleID)
		if rule == nil || rule.Priority < lastPriority {
			t.Fatalf("candidates are not dependency ordered: %#v", candidates)
		}
		lastPriority = rule.Priority
	}
}

func assertSQLiteOrphanMaintenanceEffects(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, item := range []struct {
		query string
		want  int
	}{
		{`SELECT COUNT(*) FROM call_payload WHERE id = 'payload-unused-old'`, 0},
		{`SELECT COUNT(*) FROM tool_execution_claim WHERE claim_key = 'claim-old'`, 0},
		{`SELECT COUNT(*) FROM conversation_report_context WHERE owner_id = 'owner-1' AND conversation_id = 'conversation-old'`, 0},
		{`SELECT COUNT(*) FROM report_audit_event WHERE event_id = 'audit-old'`, 1},
		{`SELECT COUNT(*) FROM report_shared_artifact WHERE artifact_id = 'shared-old'`, 1},
	} {
		var got int
		if err := db.QueryRow(item.query).Scan(&got); err != nil || got != item.want {
			t.Fatalf("effect query=%q got=%d want=%d err=%v", item.query, got, item.want, err)
		}
	}
	var scheduleConversation sql.NullString
	if err := db.QueryRow(`SELECT conversation_id FROM schedule WHERE id = 'schedule-old'`).Scan(&scheduleConversation); err != nil || scheduleConversation.Valid {
		t.Fatalf("schedule conversation_id = %#v err=%v", scheduleConversation, err)
	}
	var linkedConversation sql.NullString
	if err := db.QueryRow(`SELECT linked_conversation_id FROM message WHERE id = 'message-old'`).Scan(&linkedConversation); err != nil || !linkedConversation.Valid || linkedConversation.String != "missing-linked-conversation" {
		t.Fatalf("restored linked conversation = %#v err=%v", linkedConversation, err)
	}
}
