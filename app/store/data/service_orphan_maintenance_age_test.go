package data

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/viant/agently-core/internal/store/orphanmaintenance"
)

func TestOrphanAgeSelection_SQLite(t *testing.T) {
	svc, db := newSeededServiceWithDB(t)
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatal(err)
	}
	testOrphanAgeSelection(t, svc, db)
}

func TestOrphanAgeSelection_MySQL(t *testing.T) {
	dsn := os.Getenv("AGENTLY_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("AGENTLY_TEST_MYSQL_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("SET SESSION FOREIGN_KEY_CHECKS=0"); err != nil {
		t.Fatal(err)
	}
	testOrphanAgeSelection(t, NewService(newNativeMySQLRuntime(t, dsn)), db)
}

// A long run of old, valid messages precedes the orphans. This catches a
// selector that mistakes an empty internal qualification batch for EOF.
// Compare all public fields and ordering, including across rule boundaries.
func testOrphanAgeSelection(t *testing.T, svc Service, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	prefix := fmt.Sprintf("age-%d-", time.Now().UnixNano())
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cutoff := old.Add(24 * time.Hour)
	young := cutoff.Add(24 * time.Hour)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	valid := prefix + "valid-parent"
	exec("INSERT INTO conversation(id,created_at,updated_at,status) VALUES(?,?,?,?)", valid, young, young, "succeeded")
	t.Cleanup(func() {
		for _, table := range []string{"model_call", "tool_call", "message", "call_payload", "conversation"} {
			key := "id"
			if table == "model_call" || table == "tool_call" {
				key = "message_id"
			}
			if _, err := db.Exec("DELETE FROM "+table+" WHERE "+key+" LIKE ?", prefix+"%"); err != nil {
				t.Error(err)
			}
		}
	})
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare("INSERT INTO message(id,conversation_id,created_at,updated_at,role,type) VALUES(?,?,?,?,?,?)")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1200; i++ {
		if _, err := stmt.Exec(fmt.Sprintf("%s000-%04d", prefix, i), valid, old, old, "assistant", "text"); err != nil {
			t.Fatal(err)
		}
	}
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// Age limits apply to candidates, never to their parents or consumers.
	// Every old payload below is held exclusively by a young record.
	payloads := make([]string, 9)
	for i := range payloads {
		payloads[i] = fmt.Sprintf("%spayload-%d", prefix, i)
		exec("INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage,created_at) VALUES(?,?,?,?,?,?)", payloads[i], "attachment", "text/plain", 0, "inline", old)
	}
	consumers := []string{prefix + "young-message", prefix + "young-model", prefix + "young-tool"}
	for _, id := range consumers {
		exec("INSERT INTO message(id,conversation_id,created_at,updated_at,role,type) VALUES(?,?,?,?,?,?)", id, valid, young, young, "assistant", "text")
	}
	exec("UPDATE message SET attachment_payload_id=?, elicitation_payload_id=? WHERE id=?", payloads[0], payloads[1], consumers[0])
	exec("INSERT INTO model_call(message_id,provider,model,model_kind,status,started_at,completed_at,request_payload_id,response_payload_id,provider_request_payload_id,provider_response_payload_id,stream_payload_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", consumers[1], "test", "test", "chat", "completed", young, young, payloads[2], payloads[3], payloads[4], payloads[5], payloads[6])
	exec("INSERT INTO tool_call(message_id,op_id,tool_name,tool_kind,status,started_at,completed_at,request_payload_id,response_payload_id) VALUES(?,?,?,?,?,?,?,?,?)", consumers[2], consumers[2], "test", "general", "completed", young, young, payloads[7], payloads[8])
	// An old call and old message may also have a perfectly valid young parent.
	exec("INSERT INTO model_call(message_id,provider,model,model_kind,status,started_at,completed_at) VALUES(?,?,?,?,?,?,?)", consumers[0], "test", "test", "chat", "completed", old, old)
	exec("INSERT INTO tool_call(message_id,op_id,tool_name,tool_kind,status,started_at,completed_at) VALUES(?,?,?,?,?,?,?)", consumers[0], consumers[0], "test", "general", "completed", old, old)
	exec("UPDATE message SET parent_message_id=? WHERE id=?", consumers[0], fmt.Sprintf("%s000-%04d", prefix, 0))
	var want []OrphanMaintenanceCandidate
	addWant := func(ruleID, id, reference string, observed time.Time) {
		rule, ok := orphanmaintenance.FindRule(true, ruleID)
		if !ok {
			t.Fatal(ruleID)
		}
		want = append(want, OrphanMaintenanceCandidate{
			CursorID: orphanmaintenance.EncodeCursor(rule, id), RuleID: ruleID,
			Action: OrphanMaintenanceAction(rule.Action), Table: rule.Table, RecordID: id,
			ReferenceTable: rule.ReferenceTable, ReferenceID: reference, ObservedAt: observed,
		})
	}
	for _, row := range []struct {
		name     string
		created  time.Time
		updated  any
		eligible bool
		observed time.Time
	}{
		{"z-null-updated", old, nil, true, old},
		{"z-cutoff", old, cutoff, true, cutoff},
		{"z-created-cutoff", cutoff, nil, true, cutoff},
		{"z-updated-old-created-young", young, old, true, old},
		{"z-updated-young-created-old", old, young, false, time.Time{}},
		{"z-young", young, nil, false, time.Time{}},
		{"z-after-cutoff", old, cutoff.Add(time.Second), false, time.Time{}},
		{"z-é", old, old, true, old},
		{"z-Ω", old, old, true, old},
	} {
		id, missing := prefix+row.name, prefix+"missing-parent"
		exec("INSERT INTO message(id,conversation_id,created_at,updated_at,role,type) VALUES(?,?,?,?,?,?)", id, missing, row.created, row.updated, "assistant", "text")
		if row.eligible {
			addWant("message.missing_conversation", id, missing, row.observed)
		}
	}
	// One row must produce both a detach and a delete, in priority order.
	multi := prefix + "z-multiple"
	exec("INSERT INTO message(id,conversation_id,created_at,updated_at,role,type,linked_conversation_id) VALUES(?,?,?,?,?,?,?)", multi, prefix+"missing-parent", old, old, "assistant", "text", prefix+"missing-linked")
	addWant("message.missing_conversation", multi, prefix+"missing-parent", old)
	addWant("message.missing_linked_conversation", multi, prefix+"missing-linked", old)
	for index, reference := range []string{"", "  "} {
		id := fmt.Sprintf("%sz-blank-%d", prefix, index)
		exec("INSERT INTO message(id,conversation_id,created_at,updated_at,role,type) VALUES(?,?,?,?,?,?)", id, reference, old, old, "assistant", "text")
		addWant("message.missing_conversation", id, reference, old)
	}
	for _, row := range []struct {
		name               string
		started, completed any
		eligible           bool
		observed           time.Time
	}{
		{"fallback", old, nil, true, old}, {"boundary", young, cutoff, true, cutoff},
		{"young", old, young, false, time.Time{}}, {"no-time", nil, nil, false, time.Time{}},
	} {
		for _, table := range []string{"model_call", "tool_call"} {
			id := prefix + "z-" + table + "-" + row.name
			if table == "model_call" {
				exec("INSERT INTO model_call(message_id,provider,model,model_kind,status,started_at,completed_at) VALUES(?,?,?,?,?,?,?)", id, "test", "test", "chat", "completed", row.started, row.completed)
			} else {
				exec("INSERT INTO tool_call(message_id,op_id,tool_name,tool_kind,status,started_at,completed_at) VALUES(?,?,?,?,?,?,?)", id, id, "test", "general", "completed", row.started, row.completed)
			}
			if row.eligible {
				addWant(table+".missing_message", id, id, row.observed)
			}
		}
	}
	sort.Slice(want, func(i, j int) bool { return want[i].CursorID < want[j].CursorID })
	for _, pageSize := range []int{1, 2, 7, 100} {
		var got []OrphanMaintenanceCandidate
		cursor := ""
		for pageNumber := 0; ; pageNumber++ {
			if pageNumber > 100 {
				t.Fatal("orphan cursor did not terminate")
			}
			page, err := svc.ListOrphanMaintenanceCandidates(ctx, OrphanMaintenanceCandidateRequest{OlderThan: cutoff, AfterCursor: cursor, Limit: pageSize})
			if err != nil {
				t.Fatal(err)
			}
			if len(page) == 0 {
				break
			}
			for _, candidate := range page {
				if candidate.CursorID <= cursor {
					t.Fatal("non-advancing orphan cursor")
				}
				cursor = candidate.CursorID
				// Other tests may share the opt-in MySQL database.
				if len(candidate.RecordID) >= len(prefix) && candidate.RecordID[:len(prefix)] == prefix {
					got = append(got, candidate)
				}
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("page size %d: candidates=%+v want=%+v", pageSize, got, want)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := svc.ListOrphanMaintenanceCandidates(cancelled, OrphanMaintenanceCandidateRequest{OlderThan: cutoff, Limit: 1}); err == nil {
		t.Fatal("cancelled orphan scan succeeded")
	}
	// Selection is never permission to mutate: restoring the parent and making
	// a call recent after reporting must still be caught by exact Apply recheck.
	lease := acquireTestMaintenanceLease(t, svc, prefix+"lease", prefix+"worker")
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM maintenance_lease WHERE lease_key=?", lease.Key) })
	exec("INSERT INTO conversation(id,created_at,updated_at,status) VALUES(?,?,?,?)", prefix+"missing-parent", young, young, "succeeded")
	exec("UPDATE model_call SET completed_at=? WHERE message_id=?", young, prefix+"z-model_call-fallback")
	for _, item := range []struct{ rule, id string }{
		{"message.missing_conversation", prefix + "z-null-updated"},
		{"model_call.missing_message", prefix + "z-model_call-fallback"},
	} {
		result, err := svc.MaintainOrphanCandidate(ctx, OrphanMaintenanceRequest{RuleID: item.rule, RecordID: item.id, OlderThan: cutoff, Lease: lease})
		if err != nil {
			t.Fatal(err)
		}
		if result.Eligible || result.Mutated || result.Reason != OrphanMaintenanceNoLongerEligibleReason {
			t.Fatalf("stale candidate mutated: %+v", result)
		}
	}
}
