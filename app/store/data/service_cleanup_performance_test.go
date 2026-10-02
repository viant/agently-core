package data

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Opt-in repeatable, isolated SQLite fixtures. Runtime construction and seeding
// are excluded; the first operation is reported separately from warmed ones.
// No wall-clock threshold is asserted on shared CI hosts.
func TestCleanupPerformanceFixture(t *testing.T) {
	if os.Getenv("AGENTLY_TEST_CLEANUP_PERFORMANCE") != "1" {
		t.Skip("set AGENTLY_TEST_CLEANUP_PERFORMANCE=1 to measure cleanup fixtures")
	}
	t.Setenv(conversationDeleteDiagnosticsEnv, "0")
	for _, messages := range []int{1, 20} {
		t.Run(fmt.Sprintf("interactive_%d_messages", messages), func(t *testing.T) {
			const roots = 8
			old := time.Now().UTC().Add(-60 * 24 * time.Hour).Truncate(time.Second)
			svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
				body := strings.Repeat("large message body ", 8192)
				for i := 0; i < roots; i++ {
					root := fmt.Sprintf("perf-root-%d", i)
					if _, err := db.Exec(`INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id) VALUES (?, ?, ?, 'succeeded', 'u1')`, root, old, old); err != nil {
						t.Fatal(err)
					}
					if _, err := db.Exec(`INSERT INTO turn (id, conversation_id, status) VALUES (?, ?, 'succeeded')`, root+"-turn", root); err != nil {
						t.Fatal(err)
					}
					for j := 0; j < messages; j++ {
						if _, err := db.Exec(`INSERT INTO message (id, conversation_id, turn_id, role, type, content) VALUES (?, ?, ?, 'assistant', 'text', ?)`, fmt.Sprintf("%s-message-%d", root, j), root, root+"-turn", body); err != nil {
							t.Fatal(err)
						}
					}
				}
			})
			lease := acquireTestMaintenanceLease(t, svc)
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			var warm time.Duration
			for i := 0; i < roots; i++ {
				start := time.Now()
				result, err := svc.MaintainConversationTree(context.Background(), ConversationMaintenanceRequest{RootID: fmt.Sprintf("perf-root-%d", i), ExpectedOwnerID: "u1", Kind: ConversationMaintenanceInteractive, InactiveBefore: old.Add(24 * time.Hour), Mode: ConversationMaintenanceDelete, Lease: lease})
				elapsed := time.Since(start)
				if err != nil || !result.Deleted {
					t.Fatalf("delete root %d: result=%+v err=%v", i, result, err)
				}
				if i == 0 {
					t.Logf("cold_duration=%s", elapsed)
				} else {
					warm += elapsed
				}
			}
			runtime.ReadMemStats(&after)
			t.Logf("warm_roots=%d warm_total=%s warm_mean=%s allocated_bytes=%d", roots-1, warm, warm/(roots-1), after.TotalAlloc-before.TotalAlloc)
			var remaining int
			if err := db.QueryRow(`SELECT COUNT(*) FROM conversation WHERE id LIKE 'perf-root-%'`).Scan(&remaining); err != nil || remaining != 0 {
				t.Fatalf("remaining=%d err=%v", remaining, err)
			}
		})
	}
	t.Run("reporting_dependencies", func(t *testing.T) {
		const roots, jobsPerRoot, auditsPerJob = 4, 10, 4
		old := time.Now().UTC().Add(-60 * 24 * time.Hour).Truncate(time.Second)
		svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
			for i := 0; i < roots; i++ {
				root := fmt.Sprintf("perf-report-root-%d", i)
				mustExecTechnical(t, db, `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id) VALUES (?, ?, ?, 'succeeded', 'u1')`, root, old, old)
				for j := 0; j < jobsPerRoot; j++ {
					jobID := fmt.Sprintf("%s-job-%d", root, j)
					artifactID := jobID + "-artifact"
					mustExecTechnical(t, db, `INSERT INTO report_export_job (job_id, artifact_ref, owner_id, conversation_id, format, scope, status, submitted_at) VALUES (?, 'external://retained-by-ttl', 'u1', ?, 'pdf', 'draft', 'succeeded', ?)`, jobID, root, old)
					mustExecTechnical(t, db, `INSERT INTO report_export_artifact (artifact_id, job_id, artifact_ref, owner_id, format, content_type) VALUES (?, ?, 'external://retained-by-ttl', 'u1', 'pdf', 'application/pdf')`, artifactID, jobID)
					for k := 0; k < auditsPerJob; k++ {
						mustExecTechnical(t, db, `INSERT INTO report_audit_event (event_id, event_type, artifact_ref, job_id, artifact_id, actor_id) VALUES (?, 'export', 'external://retained-by-ttl', ?, ?, 'u1')`, fmt.Sprintf("%s-event-%d", jobID, k), jobID, artifactID)
					}
				}
			}
		})
		lease := acquireTestMaintenanceLease(t, svc)
		var warm time.Duration
		for i := 0; i < roots; i++ {
			start := time.Now()
			result, err := svc.MaintainConversationTree(context.Background(), ConversationMaintenanceRequest{RootID: fmt.Sprintf("perf-report-root-%d", i), Kind: ConversationMaintenanceInteractive, InactiveBefore: old.Add(24 * time.Hour), Mode: ConversationMaintenanceDelete, Lease: lease})
			elapsed := time.Since(start)
			if err != nil || !result.Deleted {
				t.Fatalf("delete reporting root %d: result=%+v err=%v", i, result, err)
			}
			if i == 0 {
				t.Logf("cold_duration=%s", elapsed)
			} else {
				warm += elapsed
			}
		}
		t.Logf("warm_roots=%d jobs_per_root=%d artifacts_and_audits_per_root=%d warm_mean=%s", roots-1, jobsPerRoot, jobsPerRoot*(1+auditsPerJob), warm/(roots-1))
		for _, table := range []string{"conversation", "report_export_job", "report_export_artifact", "report_audit_event"} {
			var remaining int
			if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&remaining); err != nil || remaining != 0 {
				t.Fatalf("table=%s remaining=%d err=%v", table, remaining, err)
			}
		}
	})
	t.Run("orphan_payloads", func(t *testing.T) {
		const records = 8
		old := time.Now().UTC().Add(-60 * 24 * time.Hour).Truncate(time.Second)
		svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
			for i := 0; i < records; i++ {
				mustExecTechnical(t, db, `INSERT INTO call_payload (id, kind, mime_type, size_bytes, storage, compression, created_at) VALUES (?, 'test', 'text/plain', 0, 'inline', 'none', ?)`, fmt.Sprintf("perf-orphan-%d", i), old)
			}
		})
		lease := acquireTestMaintenanceLease(t, svc)
		var warm time.Duration
		for i := 0; i < records; i++ {
			start := time.Now()
			result, err := svc.MaintainOrphanCandidate(context.Background(), OrphanMaintenanceRequest{RuleID: "call_payload.unused", RecordID: fmt.Sprintf("perf-orphan-%d", i), OlderThan: old.Add(24 * time.Hour), Lease: lease})
			elapsed := time.Since(start)
			if err != nil || !result.Deleted {
				t.Fatalf("delete orphan %d: result=%+v err=%v", i, result, err)
			}
			if i == 0 {
				t.Logf("cold_duration=%s", elapsed)
			} else {
				warm += elapsed
			}
		}
		t.Logf("warm_records=%d warm_mean=%s", records-1, warm/(records-1))
		var remaining int
		if err := db.QueryRow(`SELECT COUNT(*) FROM call_payload`).Scan(&remaining); err != nil || remaining != 0 {
			t.Fatalf("remaining=%d err=%v", remaining, err)
		}
	})
}
