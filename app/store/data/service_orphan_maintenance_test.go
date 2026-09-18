package data

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestListOrphanMaintenanceCandidates_SQLiteReportsClassesGraceAndPaginationWithoutMutation(t *testing.T) {
	svc, db := newSeededServiceWithDB(t, seedSQLiteOrphanMaintenanceFixtures)
	before := sqliteOrphanFixtureCounts(t, db)
	cutoff := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)

	var candidates []OrphanMaintenanceCandidate
	cursor := ""
	for {
		page, err := svc.ListOrphanMaintenanceCandidates(context.Background(), OrphanMaintenanceCandidateRequest{
			OlderThan: cutoff, AfterCursor: cursor, Limit: 2,
		})
		if err != nil {
			t.Fatalf("ListOrphanMaintenanceCandidates(cursor=%q): %v", cursor, err)
		}
		if len(page) == 0 {
			break
		}
		for _, candidate := range page {
			if cursor != "" && candidate.CursorID <= cursor {
				t.Fatalf("cursor did not advance: previous=%q candidate=%#v", cursor, candidate)
			}
			candidates = append(candidates, candidate)
			cursor = candidate.CursorID
		}
		if len(candidates) > 100 {
			t.Fatal("orphan pagination did not terminate")
		}
	}

	want := map[string]OrphanMaintenanceAction{
		"call_payload.unused\x1epayload-unused-old":                                            OrphanMaintenanceSafeDelete,
		"conversation_report_context.missing_active_report_run\x1eowner-1\x1fconversation-old": OrphanMaintenanceSafeDelete,
		"message.missing_linked_conversation\x1emessage-old":                                   OrphanMaintenanceSafeDetach,
		"schedule.missing_conversation\x1eschedule-old":                                        OrphanMaintenanceSafeDetach,
		"tool_execution_claim.missing_turn\x1eclaim-old":                                       OrphanMaintenanceSafeDelete,
	}
	found := map[string]OrphanMaintenanceAction{}
	for _, candidate := range candidates {
		key := candidate.RuleID + orphanMaintenanceCursorSeparator + candidate.RecordID
		if _, expected := want[key]; expected {
			found[key] = candidate.Action
		}
		if candidate.RecordID == "payload-unused-recent" {
			t.Fatalf("grace-period candidate was reported: %#v", candidate)
		}
		if candidate.RecordID == "payload-used-old" {
			t.Fatalf("used payload was reported: %#v", candidate)
		}
		if candidate.RecordID == "shared-old" {
			t.Fatalf("logical shared-artifact source was reported as an orphan: %#v", candidate)
		}
	}
	if !reflect.DeepEqual(found, want) {
		t.Fatalf("reported fixtures = %#v, want %#v; all=%#v", found, want, candidates)
	}
	if after := sqliteOrphanFixtureCounts(t, db); !reflect.DeepEqual(after, before) {
		t.Fatalf("orphan report mutated SQLite: before=%v after=%v", before, after)
	}
}

func TestListOrphanMaintenanceCandidates_SQLitePaginatesWithinAndAcrossRules(t *testing.T) {
	svc, _ := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		t.Helper()
		old := "2026-01-01T00:00:00Z"
		statements := []struct {
			query string
			args  []interface{}
		}{
			{`INSERT INTO conversation (id, created_at, updated_at, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, []interface{}{"pagination-conversation", old, old, "succeeded", "owner-1"}},
			{`INSERT INTO message (id, conversation_id, created_at, updated_at, role, type, linked_conversation_id) VALUES (?, ?, ?, ?, ?, ?, ?)`, []interface{}{"pagination-message-a", "pagination-conversation", old, old, "assistant", "text", "missing-linked-a"}},
			{`INSERT INTO message (id, conversation_id, created_at, updated_at, role, type, linked_conversation_id) VALUES (?, ?, ?, ?, ?, ?, ?)`, []interface{}{"pagination-message-b", "pagination-conversation", old, old, "assistant", "text", "missing-linked-b"}},
			{`INSERT INTO message (id, conversation_id, created_at, updated_at, role, type, linked_conversation_id) VALUES (?, ?, ?, ?, ?, ?, ?)`, []interface{}{"pagination-message-c", "pagination-conversation", old, old, "assistant", "text", "missing-linked-c"}},
			{`INSERT INTO schedule (id, name, conversation_id, agent_ref, schedule_type, timezone, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, []interface{}{"pagination-schedule-a", "pagination-schedule-a", "missing-schedule-conversation", "agent", "adhoc", "UTC", old, old}},
		}
		for _, statement := range statements {
			if _, err := db.Exec(statement.query, statement.args...); err != nil {
				t.Fatalf("seed SQLite pagination fixture with %q: %v", statement.query, err)
			}
		}
	})

	cutoff := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	wantPages := [][]string{
		{
			"message.missing_linked_conversation\x1epagination-message-a",
			"message.missing_linked_conversation\x1epagination-message-b",
		},
		{
			"message.missing_linked_conversation\x1epagination-message-c",
			"schedule.missing_conversation\x1epagination-schedule-a",
		},
	}
	cursor := ""
	for pageIndex, want := range wantPages {
		page, err := svc.ListOrphanMaintenanceCandidates(context.Background(), OrphanMaintenanceCandidateRequest{
			OlderThan: cutoff, AfterCursor: cursor, Limit: 2,
		})
		if err != nil {
			t.Fatalf("list SQLite pagination page %d: %v", pageIndex+1, err)
		}
		got := make([]string, 0, len(page))
		for _, candidate := range page {
			got = append(got, candidate.RuleID+orphanMaintenanceCursorSeparator+candidate.RecordID)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("SQLite pagination page %d = %v, want %v", pageIndex+1, got, want)
		}
		cursor = page[len(page)-1].CursorID
	}

	page, err := svc.ListOrphanMaintenanceCandidates(context.Background(), OrphanMaintenanceCandidateRequest{
		OlderThan: cutoff, AfterCursor: cursor, Limit: 2,
	})
	if err != nil {
		t.Fatalf("list final SQLite pagination page: %v", err)
	}
	if len(page) != 0 {
		t.Fatalf("final SQLite pagination page = %#v, want empty", page)
	}
}

func TestListOrphanMaintenanceCandidates_ValidatesRequestAndCursor(t *testing.T) {
	svc := newSeededService(t)
	_, err := svc.ListOrphanMaintenanceCandidates(context.Background(), OrphanMaintenanceCandidateRequest{})
	if !errors.Is(err, ErrInvalidConversationMaintenanceRequest) {
		t.Fatalf("missing cutoff error = %v", err)
	}
	_, err = svc.ListOrphanMaintenanceCandidates(context.Background(), OrphanMaintenanceCandidateRequest{
		OlderThan: time.Now(), Limit: 1, AfterCursor: "unknown\x1erecord",
	})
	if !errors.Is(err, ErrInvalidConversationMaintenanceRequest) {
		t.Fatalf("unknown cursor error = %v", err)
	}
}

func TestOrphanMaintenanceRulesUseStaticDriverContractAndStableOrder(t *testing.T) {
	sqliteCapabilities, err := deleteSchemaCapabilitiesForDriver("sqlite")
	if err != nil {
		t.Fatal(err)
	}
	mysqlCapabilities, err := deleteSchemaCapabilitiesForDriver("mysql")
	if err != nil {
		t.Fatal(err)
	}
	sqliteRules := orphanMaintenanceRules(sqliteCapabilities)
	mysqlRules := orphanMaintenanceRules(mysqlCapabilities)
	if len(sqliteRules) == 0 || len(mysqlRules) <= len(sqliteRules) {
		t.Fatalf("unexpected rule counts: sqlite=%d mysql=%d", len(sqliteRules), len(mysqlRules))
	}
	for _, rules := range [][]orphanMaintenanceRule{sqliteRules, mysqlRules} {
		if !sort.SliceIsSorted(rules, func(i, j int) bool {
			if rules[i].Priority != rules[j].Priority {
				return rules[i].Priority < rules[j].Priority
			}
			return rules[i].ID < rules[j].ID
		}) {
			t.Fatalf("rules are not sorted: %#v", rules)
		}
		knownTables := map[string]bool{}
		for _, table := range conversationDeleteSchemaTables {
			knownTables[table] = true
		}
		seen := map[string]bool{}
		for _, rule := range rules {
			if seen[rule.ID] {
				t.Fatalf("duplicate orphan rule %q", rule.ID)
			}
			seen[rule.ID] = true
			if rule.Priority <= 0 {
				t.Fatalf("rule %q has invalid priority %d", rule.ID, rule.Priority)
			}
			if !knownTables[rule.Table] {
				t.Fatalf("rule %q uses table outside deletion schema contract: %q", rule.ID, rule.Table)
			}
			for _, table := range rule.RequiredTables {
				if !knownTables[table] {
					t.Fatalf("rule %q requires table outside deletion schema contract: %q", rule.ID, table)
				}
			}
			switch rule.Action {
			case OrphanMaintenanceSafeDelete, OrphanMaintenanceSafeDetach, OrphanMaintenanceReportOnly:
			default:
				t.Fatalf("rule %q has invalid action %q", rule.ID, rule.Action)
			}
			if err := validateOrphanMaintenanceMutationRule(rule); err != nil {
				t.Fatalf("rule %q has invalid mutation contract: %v", rule.ID, err)
			}
			keyValues := make([]interface{}, len(rule.KeyColumns))
			for i := range keyValues {
				keyValues[i] = "key"
			}
			if rule.Action != OrphanMaintenanceReportOnly {
				query, args := orphanMaintenanceMutationStatement(rule, keyValues)
				if strings.TrimSpace(query) == "" || len(args) != len(rule.KeyColumns) {
					t.Fatalf("rule %q produced invalid mutation statement query=%q args=%v", rule.ID, query, args)
				}
			}
		}
	}
	if orphanRuleByID(sqliteRules, "investigation.missing_conversation") != nil || orphanRuleByID(sqliteRules, "schedule_run.missing_schedule") != nil {
		t.Fatalf("SQLite rules include unavailable tables")
	}
	if rule := orphanRuleByID(mysqlRules, "investigation.missing_conversation"); rule == nil || rule.Action != OrphanMaintenanceSafeDelete || rule.DetachColumn != "" {
		t.Fatalf("MySQL investigation rule = %#v", rule)
	}
	if rule := orphanRuleByID(mysqlRules, "report_audit_event.missing_job"); rule != nil {
		t.Fatalf("disabled audit pseudo-orphan rule was registered: %#v", rule)
	}
	if rule := orphanRuleByID(mysqlRules, "report_audit_event.missing_artifact"); rule != nil {
		t.Fatalf("disabled audit pseudo-orphan rule was registered: %#v", rule)
	}
	if rule := orphanRuleByID(mysqlRules, "report_shared_artifact.missing_source"); rule != nil {
		t.Fatalf("disabled shared artifact rule was registered: %#v", rule)
	}
}

func seedSQLiteOrphanMaintenanceFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	old := "2026-01-01T00:00:00Z"
	recent := "2026-01-03T00:00:00Z"
	statements := []struct {
		query string
		args  []interface{}
	}{
		{`INSERT INTO conversation (id, created_at, updated_at, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, []interface{}{"conversation-old", old, old, "succeeded", "owner-1"}},
		{`INSERT INTO call_payload (id, kind, mime_type, size_bytes, storage, compression, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, []interface{}{"payload-unused-old", "test", "text/plain", 0, "inline", "none", old}},
		{`INSERT INTO call_payload (id, kind, mime_type, size_bytes, storage, compression, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, []interface{}{"payload-unused-recent", "test", "text/plain", 0, "inline", "none", recent}},
		{`INSERT INTO call_payload (id, kind, mime_type, size_bytes, storage, compression, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, []interface{}{"payload-used-old", "test", "text/plain", 0, "inline", "none", old}},
		{`INSERT INTO message (id, conversation_id, created_at, updated_at, role, type, linked_conversation_id, attachment_payload_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, []interface{}{"message-old", "conversation-old", old, old, "assistant", "text", "missing-linked-conversation", "payload-used-old"}},
		{`INSERT INTO tool_execution_claim (claim_key, rule_id, canonical_tool_name, turn_id, semantic_request_hash, state, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, []interface{}{"claim-old", "rule", "tool", "missing-turn", "hash", "failed", old, old}},
		{`INSERT INTO schedule (id, name, conversation_id, agent_ref, schedule_type, timezone, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, []interface{}{"schedule-old", "schedule-old", "missing-schedule-conversation", "agent", "adhoc", "UTC", old, old}},
		{`INSERT INTO report_audit_event (event_id, event_type, artifact_ref, version, job_id, actor_id, occurred_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, []interface{}{"audit-old", "test", "artifact", 1, "missing-job", "owner-1", old}},
		{`INSERT INTO report_shared_artifact (artifact_id, artifact_ref, owner_id, kind, lifecycle, source_artifact_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, []interface{}{"shared-old", "artifact", "owner-1", "report", "saved", "missing-source", old, old}},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("seed SQLite orphan fixture with %q: %v", statement.query, err)
		}
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatalf("disable SQLite foreign keys: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO conversation_report_context (owner_id, conversation_id, active_report_run_id, revision, updated_at) VALUES (?, ?, ?, ?, ?)`, "owner-1", "conversation-old", "missing-report-run", 1, old); err != nil {
		t.Fatalf("seed orphan report context: %v", err)
	}
}

func sqliteOrphanFixtureCounts(t *testing.T, db *sql.DB) map[string]int {
	t.Helper()
	queries := map[string]string{
		"payload":  `SELECT COUNT(*) FROM call_payload WHERE id IN ('payload-unused-old', 'payload-unused-recent', 'payload-used-old')`,
		"message":  `SELECT COUNT(*) FROM message WHERE id = 'message-old'`,
		"claim":    `SELECT COUNT(*) FROM tool_execution_claim WHERE claim_key = 'claim-old'`,
		"schedule": `SELECT COUNT(*) FROM schedule WHERE id = 'schedule-old'`,
		"audit":    `SELECT COUNT(*) FROM report_audit_event WHERE event_id = 'audit-old'`,
		"shared":   `SELECT COUNT(*) FROM report_shared_artifact WHERE artifact_id = 'shared-old'`,
		"context":  `SELECT COUNT(*) FROM conversation_report_context WHERE owner_id = 'owner-1' AND conversation_id = 'conversation-old'`,
	}
	result := make(map[string]int, len(queries))
	for name, query := range queries {
		var count int
		if err := db.QueryRow(query).Scan(&count); err != nil {
			t.Fatalf("count SQLite orphan fixture %s: %v", name, err)
		}
		result[name] = count
	}
	return result
}
