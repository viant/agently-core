package data

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/viant/datly"
	"github.com/viant/datly/view"
)

func TestMaintainOrphanCandidate_MySQLReportsRechecksAndMutatesAllClasses(t *testing.T) {
	dsn := os.Getenv("AGENTLY_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("AGENTLY_TEST_MYSQL_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("sql.Open(mysql): %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err = db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping MySQL: %v", err)
	}
	if _, err = db.Exec(`SET FOREIGN_KEY_CHECKS = 0`); err != nil {
		t.Fatalf("disable MySQL foreign-key checks: %v", err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	ids := mysqlOrphanFixtureIDs{
		conversation:         "orphan-conversation-" + suffix,
		restoredConversation: "orphan-restored-conversation-" + suffix,
		message:              "orphan-message-" + suffix,
		payloadOld:           "orphan-payload-old-" + suffix,
		payloadRecent:        "orphan-payload-recent-" + suffix,
		claim:                "orphan-claim-" + suffix,
		schedule:             "orphan-schedule-" + suffix,
		scheduleRun:          "orphan-schedule-run-" + suffix,
		investigation:        "orphan-investigation-" + suffix,
		audit:                "orphan-audit-" + suffix,
		shared:               "orphan-shared-" + suffix,
		payloadConsumers: []string{
			"orphan-payload-message-attachment-" + suffix,
			"orphan-payload-message-elicitation-" + suffix,
			"orphan-payload-model-request-" + suffix,
			"orphan-payload-model-response-" + suffix,
			"orphan-payload-model-provider-request-" + suffix,
			"orphan-payload-model-provider-response-" + suffix,
			"orphan-payload-model-stream-" + suffix,
			"orphan-payload-tool-request-" + suffix,
			"orphan-payload-tool-response-" + suffix,
			"orphan-payload-generated-file-" + suffix,
		},
		consumerMessages: []string{
			"orphan-payload-message-" + suffix,
			"orphan-payload-model-message-" + suffix,
			"orphan-payload-tool-message-" + suffix,
		},
		generatedFile: "orphan-payload-generated-file-record-" + suffix,
	}
	t.Cleanup(func() { cleanupMySQLOrphanFixtures(t, db, ids) })
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	missing := func(kind string) string { return "missing-" + kind + "-" + suffix }
	statements := []struct {
		query string
		args  []interface{}
	}{
		{`INSERT INTO conversation (id, created_at, updated_at, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, []interface{}{ids.conversation, old, old, "succeeded", "owner-1"}},
		{`INSERT INTO call_payload (id, kind, mime_type, size_bytes, storage, uri, compression, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, []interface{}{ids.payloadOld, "attachment", "text/plain", 0, "object", "test://old", "none", old}},
		{`INSERT INTO call_payload (id, kind, mime_type, size_bytes, storage, uri, compression, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, []interface{}{ids.payloadRecent, "attachment", "text/plain", 0, "object", "test://recent", "none", recent}},
		{`INSERT INTO message (id, conversation_id, created_at, updated_at, role, type, linked_conversation_id) VALUES (?, ?, ?, ?, ?, ?, ?)`, []interface{}{ids.message, ids.conversation, old, old, "assistant", "text", ids.restoredConversation}},
		{`INSERT INTO tool_execution_claim (claim_key, rule_id, canonical_tool_name, turn_id, semantic_request_hash, state, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, []interface{}{ids.claim, "rule", "tool", missing("turn"), "hash", "failed", old, old}},
		{`INSERT INTO schedule (id, name, conversation_id, agent_ref, schedule_type, timezone, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, []interface{}{ids.schedule, ids.schedule, missing("conversation"), "agent", "adhoc", "UTC", old, old}},
		{`INSERT INTO schedule_run (id, schedule_id, conversation_id, status, conversation_kind, created_at, updated_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, []interface{}{ids.scheduleRun, missing("schedule"), missing("conversation"), "failed", "scheduled", old, old, old}},
		{`INSERT INTO investigation (id, title, created_by, conversation_id, created) VALUES (?, ?, ?, ?, ?)`, []interface{}{ids.investigation, "orphan", "owner-1", missing("conversation"), old}},
		{`INSERT INTO conversation_report_context (owner_id, conversation_id, active_report_run_id, revision, updated_at) VALUES (?, ?, ?, ?, ?)`, []interface{}{"owner-1", ids.conversation, missing("report-run"), 1, old}},
		{`INSERT INTO report_audit_event (event_id, event_type, artifact_ref, version, job_id, actor_id, occurred_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, []interface{}{ids.audit, "test", "artifact", 1, missing("job"), "owner-1", old}},
		{`INSERT INTO report_shared_artifact (artifact_id, artifact_ref, owner_id, kind, lifecycle, source_artifact_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, []interface{}{ids.shared, "artifact", "owner-1", "report", "saved", missing("source"), old, old}},
	}
	for _, statement := range statements {
		if _, err = db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("seed MySQL orphan fixture with %q: %v", statement.query, err)
		}
	}
	for _, payloadID := range ids.payloadConsumers {
		if _, err = db.Exec(`INSERT INTO call_payload (id, kind, mime_type, size_bytes, storage, uri, compression, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			payloadID, "attachment", "text/plain", 0, "object", "test://used", "none", old); err != nil {
			t.Fatalf("seed referenced MySQL payload %q: %v", payloadID, err)
		}
	}
	consumerStatements := []struct {
		query string
		args  []interface{}
	}{
		{`INSERT INTO message (id, conversation_id, created_at, updated_at, role, type, attachment_payload_id, elicitation_payload_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, []interface{}{ids.consumerMessages[0], ids.conversation, old, old, "assistant", "text", ids.payloadConsumers[0], ids.payloadConsumers[1]}},
		{`INSERT INTO message (id, conversation_id, created_at, updated_at, role, type) VALUES (?, ?, ?, ?, ?, ?)`, []interface{}{ids.consumerMessages[1], ids.conversation, old, old, "assistant", "text"}},
		{`INSERT INTO model_call (message_id, provider, model, model_kind, status, started_at, completed_at, request_payload_id, response_payload_id, provider_request_payload_id, provider_response_payload_id, stream_payload_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, []interface{}{ids.consumerMessages[1], "test", "test", "chat", "completed", old, old, ids.payloadConsumers[2], ids.payloadConsumers[3], ids.payloadConsumers[4], ids.payloadConsumers[5], ids.payloadConsumers[6]}},
		{`INSERT INTO message (id, conversation_id, created_at, updated_at, role, type) VALUES (?, ?, ?, ?, ?, ?)`, []interface{}{ids.consumerMessages[2], ids.conversation, old, old, "assistant", "text"}},
		{`INSERT INTO tool_call (message_id, op_id, attempt, tool_name, tool_kind, status, started_at, completed_at, request_payload_id, response_payload_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, []interface{}{ids.consumerMessages[2], "test", 1, "test", "general", "completed", old, old, ids.payloadConsumers[7], ids.payloadConsumers[8]}},
		{`INSERT INTO generated_file (id, conversation_id, provider, mode, copy_mode, status, payload_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, []interface{}{ids.generatedFile, ids.conversation, "test", "inline", "eager", "ready", ids.payloadConsumers[9], old, old}},
	}
	for _, statement := range consumerStatements {
		if _, err = db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("seed MySQL payload consumer with %q: %v", statement.query, err)
		}
	}
	before := mysqlOrphanFixtureCounts(t, db, ids)

	ctx := context.Background()
	dao, err := datly.New(ctx)
	if err != nil {
		t.Fatalf("datly.New(): %v", err)
	}
	if err = dao.AddConnectors(ctx, view.NewConnector("agently", "mysql", dsn)); err != nil {
		t.Fatalf("AddConnectors(): %v", err)
	}
	if err = registerReadComponents(ctx, dao); err != nil {
		t.Fatalf("registerReadComponents(): %v", err)
	}
	svc := NewService(dao)
	leaseKey := "test-orphan-maintenance-" + suffix
	lease := acquireTestMaintenanceLease(t, svc, leaseKey, "test-worker-"+suffix)
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM maintenance_lease WHERE lease_key = ?`, leaseKey) })
	cutoff := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	var candidates []OrphanMaintenanceCandidate
	cursor := ""
	for {
		page, listErr := svc.ListOrphanMaintenanceCandidates(ctx, OrphanMaintenanceCandidateRequest{
			OlderThan: cutoff, AfterCursor: cursor, Limit: 2,
		})
		if listErr != nil {
			t.Fatalf("ListOrphanMaintenanceCandidates(MySQL, cursor=%q): %v", cursor, listErr)
		}
		if len(page) == 0 {
			break
		}
		for _, candidate := range page {
			if cursor != "" && candidate.CursorID <= cursor {
				t.Fatalf("MySQL orphan cursor did not advance: previous=%q candidate=%#v", cursor, candidate)
			}
			candidates = append(candidates, candidate)
			cursor = candidate.CursorID
		}
		if len(candidates) > 10000 {
			t.Fatal("MySQL orphan pagination did not terminate")
		}
	}

	want := map[string]OrphanMaintenanceAction{
		"call_payload.unused\x1e" + ids.payloadOld:                                                OrphanMaintenanceSafeDelete,
		"conversation_report_context.missing_active_report_run\x1eowner-1\x1f" + ids.conversation: OrphanMaintenanceSafeDelete,
		"investigation.missing_conversation\x1e" + ids.investigation:                              OrphanMaintenanceSafeDelete,
		"message.missing_linked_conversation\x1e" + ids.message:                                   OrphanMaintenanceSafeDetach,
		"schedule.missing_conversation\x1e" + ids.schedule:                                        OrphanMaintenanceSafeDetach,
		"schedule_run.missing_conversation\x1e" + ids.scheduleRun:                                 OrphanMaintenanceSafeDetach,
		"schedule_run.missing_schedule\x1e" + ids.scheduleRun:                                     OrphanMaintenanceSafeDelete,
		"tool_execution_claim.missing_turn\x1e" + ids.claim:                                       OrphanMaintenanceSafeDelete,
	}
	found := map[string]OrphanMaintenanceAction{}
	for _, candidate := range candidates {
		key := candidate.RuleID + orphanMaintenanceCursorSeparator + candidate.RecordID
		if _, expected := want[key]; expected {
			found[key] = candidate.Action
		}
		if candidate.RecordID == ids.payloadRecent {
			t.Fatalf("grace-period candidate was reported: %#v", candidate)
		}
		if candidate.RecordID == ids.shared {
			t.Fatalf("logical shared-artifact source was reported as an orphan: %#v", candidate)
		}
		for _, payloadID := range ids.payloadConsumers {
			if candidate.RuleID == "call_payload.unused" && candidate.RecordID == payloadID {
				t.Fatalf("referenced payload was reported as unused: %#v", candidate)
			}
		}
	}
	if !reflect.DeepEqual(found, want) {
		t.Fatalf("reported MySQL fixtures = %#v, want %#v", found, want)
	}
	t.Logf("MySQL orphan dry-run: controlled_candidates=%d", len(found))
	if after := mysqlOrphanFixtureCounts(t, db, ids); !reflect.DeepEqual(after, before) {
		t.Fatalf("orphan report mutated MySQL: before=%v after=%v", before, after)
	}

	// The parent is created after the report. Per-candidate transactional
	// recheck must keep the reference intact instead of detaching it.
	if _, err = db.Exec(`INSERT INTO conversation (id, created_at, updated_at, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`,
		ids.restoredConversation, old, old, "succeeded", "owner-1"); err != nil {
		t.Fatalf("restore MySQL parent after report: %v", err)
	}

	results := map[OrphanMaintenanceReason]int{}
	for _, candidate := range candidates {
		key := candidate.RuleID + orphanMaintenanceCursorSeparator + candidate.RecordID
		if _, expected := want[key]; !expected {
			continue
		}
		result, maintainErr := svc.MaintainOrphanCandidate(ctx, OrphanMaintenanceRequest{
			RuleID: candidate.RuleID, RecordID: candidate.RecordID, OlderThan: cutoff, Lease: lease,
		})
		if maintainErr != nil {
			t.Fatalf("MaintainOrphanCandidate(MySQL) rule=%s record=%s: %v", candidate.RuleID, candidate.RecordID, maintainErr)
		}
		results[result.Reason]++
	}
	wantResults := map[OrphanMaintenanceReason]int{
		OrphanMaintenanceDeletedReason:          5,
		OrphanMaintenanceDetachedReason:         2,
		OrphanMaintenanceNoLongerEligibleReason: 1,
	}
	if !reflect.DeepEqual(results, wantResults) {
		t.Fatalf("MySQL maintenance results = %v, want %v", results, wantResults)
	}
	t.Logf("MySQL orphan maintenance: results=%v", results)
	wantAfter := map[string]int{
		"conversation": 1, "message": 1, "payload": 1, "claim": 0, "schedule": 1,
		"schedule_run": 0, "investigation": 0, "report_context": 0, "audit": 1, "shared": 1,
	}
	if after := mysqlOrphanFixtureCounts(t, db, ids); !reflect.DeepEqual(after, wantAfter) {
		t.Fatalf("MySQL maintenance effects = %v, want %v", after, wantAfter)
	}
	assertMySQLOrphanMaintenanceReferences(t, db, ids)

	result, err := svc.MaintainOrphanCandidate(ctx, OrphanMaintenanceRequest{
		RuleID: "call_payload.unused", RecordID: ids.payloadOld, OlderThan: cutoff, Lease: lease,
	})
	if err != nil {
		t.Fatalf("idempotent MySQL maintenance: %v", err)
	}
	if result.Reason != OrphanMaintenanceNoLongerEligibleReason || result.Mutated {
		t.Fatalf("idempotent MySQL result = %#v", result)
	}
}

func TestMaintainOrphanCandidate_MySQLInvestigationEligibilityAndRecheck(t *testing.T) {
	dsn := os.Getenv("AGENTLY_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("AGENTLY_TEST_MYSQL_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("sql.Open(mysql): %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err = db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping MySQL: %v", err)
	}
	if _, err = db.Exec(`SET FOREIGN_KEY_CHECKS = 0`); err != nil {
		t.Fatalf("disable MySQL foreign-key checks: %v", err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	validConversationID := "investigation-valid-conversation-" + suffix
	restoredConversationID := "investigation-restored-conversation-" + suffix
	investigationIDs := map[string]string{
		"missing":  "investigation-missing-" + suffix,
		"null":     "investigation-null-" + suffix,
		"empty":    "investigation-empty-" + suffix,
		"recent":   "investigation-recent-" + suffix,
		"valid":    "investigation-valid-" + suffix,
		"restored": "investigation-restored-" + suffix,
	}
	t.Cleanup(func() {
		for _, id := range investigationIDs {
			_, _ = db.Exec(`DELETE FROM investigation WHERE id = ?`, id)
		}
		_, _ = db.Exec(`DELETE FROM conversation WHERE id IN (?, ?)`, validConversationID, restoredConversationID)
		_, _ = db.Exec(`SET FOREIGN_KEY_CHECKS = 1`)
	})

	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	cutoff := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if _, err = db.Exec(`INSERT INTO conversation (id, created_at, updated_at, status) VALUES (?, ?, ?, ?)`, validConversationID, old, old, "succeeded"); err != nil {
		t.Fatalf("seed valid investigation conversation: %v", err)
	}
	for _, item := range []struct {
		id             string
		conversationID interface{}
		created        time.Time
	}{
		{investigationIDs["missing"], "missing-investigation-conversation-" + suffix, old},
		{investigationIDs["null"], nil, old},
		{investigationIDs["empty"], "", old},
		{investigationIDs["recent"], "missing-recent-investigation-conversation-" + suffix, recent},
		{investigationIDs["valid"], validConversationID, old},
		{investigationIDs["restored"], restoredConversationID, old},
	} {
		if _, err = db.Exec(`INSERT INTO investigation (id, title, created_by, conversation_id, created) VALUES (?, ?, ?, ?, ?)`, item.id, item.id, "owner-1", item.conversationID, item.created); err != nil {
			t.Fatalf("seed investigation %q: %v", item.id, err)
		}
	}

	ctx := context.Background()
	dao, err := datly.New(ctx)
	if err != nil {
		t.Fatalf("datly.New(): %v", err)
	}
	if err = dao.AddConnectors(ctx, view.NewConnector("agently", "mysql", dsn)); err != nil {
		t.Fatalf("AddConnectors(): %v", err)
	}
	if err = registerReadComponents(ctx, dao); err != nil {
		t.Fatalf("registerReadComponents(): %v", err)
	}
	svc := NewService(dao)
	leaseKey := "test-investigation-orphan-maintenance-" + suffix
	lease := acquireTestMaintenanceLease(t, svc, leaseKey, "test-worker-"+suffix)
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM maintenance_lease WHERE lease_key = ?`, leaseKey) })

	candidates, err := svc.ListOrphanMaintenanceCandidates(ctx, OrphanMaintenanceCandidateRequest{OlderThan: cutoff, Limit: 10000})
	if err != nil {
		t.Fatalf("ListOrphanMaintenanceCandidates(MySQL): %v", err)
	}
	wantCandidates := map[string]bool{
		investigationIDs["missing"]:  true,
		investigationIDs["null"]:     true,
		investigationIDs["empty"]:    true,
		investigationIDs["restored"]: true,
	}
	foundCandidates := map[string]bool{}
	for _, candidate := range candidates {
		if candidate.RuleID != "investigation.missing_conversation" {
			continue
		}
		if wantCandidates[candidate.RecordID] {
			if candidate.Action != OrphanMaintenanceSafeDelete {
				t.Fatalf("investigation candidate action = %q, want safe-delete: %#v", candidate.Action, candidate)
			}
			foundCandidates[candidate.RecordID] = true
		}
		if candidate.RecordID == investigationIDs["recent"] || candidate.RecordID == investigationIDs["valid"] {
			t.Fatalf("ineligible investigation was reported: %#v", candidate)
		}
	}
	if !reflect.DeepEqual(foundCandidates, wantCandidates) {
		t.Fatalf("investigation candidates = %v, want %v", foundCandidates, wantCandidates)
	}

	// A parent created after listing must make the candidate ineligible when the
	// destructive operation rechecks the exact rule in its transaction.
	if _, err = db.Exec(`INSERT INTO conversation (id, created_at, updated_at, status) VALUES (?, ?, ?, ?)`, restoredConversationID, old, old, "succeeded"); err != nil {
		t.Fatalf("restore investigation conversation before mutation: %v", err)
	}
	for id := range wantCandidates {
		result, maintainErr := svc.MaintainOrphanCandidate(ctx, OrphanMaintenanceRequest{
			RuleID: "investigation.missing_conversation", RecordID: id, OlderThan: cutoff, Lease: lease,
		})
		if maintainErr != nil {
			t.Fatalf("MaintainOrphanCandidate(MySQL, %q): %v", id, maintainErr)
		}
		wantReason := OrphanMaintenanceDeletedReason
		if id == investigationIDs["restored"] {
			wantReason = OrphanMaintenanceNoLongerEligibleReason
		}
		if result.Reason != wantReason {
			t.Fatalf("investigation %q result = %#v, want reason %q", id, result, wantReason)
		}
	}

	for _, kind := range []string{"missing", "null", "empty"} {
		assertStage1RowCount(t, db, "investigation", "id", investigationIDs[kind], 0)
	}
	for _, kind := range []string{"recent", "valid", "restored"} {
		assertStage1RowCount(t, db, "investigation", "id", investigationIDs[kind], 1)
	}
}

type mysqlOrphanFixtureIDs struct {
	conversation, restoredConversation, message, payloadOld, payloadRecent, claim, schedule, scheduleRun, investigation, audit, shared string
	payloadConsumers                                                                                                                   []string
	consumerMessages                                                                                                                   []string
	generatedFile                                                                                                                      string
}

func cleanupMySQLOrphanFixtures(t *testing.T, db *sql.DB, ids mysqlOrphanFixtureIDs) {
	t.Helper()
	statements := []struct {
		query string
		id    string
	}{
		{"DELETE FROM conversation_report_context WHERE conversation_id = ?", ids.conversation},
		{"DELETE FROM report_audit_event WHERE event_id = ?", ids.audit},
		{"DELETE FROM report_shared_artifact WHERE artifact_id = ?", ids.shared},
		{"DELETE FROM generated_file WHERE id = ?", ids.generatedFile},
		{"DELETE FROM model_call WHERE message_id = ?", ids.consumerMessages[1]},
		{"DELETE FROM tool_call WHERE message_id = ?", ids.consumerMessages[2]},
		{"DELETE FROM schedule_run WHERE id = ?", ids.scheduleRun},
		{"DELETE FROM investigation WHERE id = ?", ids.investigation},
		{"DELETE FROM message WHERE id = ?", ids.message},
		{"DELETE FROM tool_execution_claim WHERE claim_key = ?", ids.claim},
		{"DELETE FROM schedule WHERE id = ?", ids.schedule},
		{"DELETE FROM call_payload WHERE id IN (?, ?)", ids.payloadOld},
		{"DELETE FROM conversation WHERE id = ?", ids.conversation},
		{"DELETE FROM conversation WHERE id = ?", ids.restoredConversation},
	}
	for _, statement := range statements {
		var err error
		if statement.query == "DELETE FROM call_payload WHERE id IN (?, ?)" {
			_, err = db.Exec(statement.query, ids.payloadOld, ids.payloadRecent)
		} else {
			_, err = db.Exec(statement.query, statement.id)
		}
		if err != nil {
			t.Errorf("cleanup MySQL orphan fixture with %q: %v", statement.query, err)
		}
	}
	for _, messageID := range ids.consumerMessages {
		if _, err := db.Exec(`DELETE FROM message WHERE id = ?`, messageID); err != nil {
			t.Errorf("cleanup MySQL payload consumer message %q: %v", messageID, err)
		}
	}
	for _, payloadID := range ids.payloadConsumers {
		if _, err := db.Exec(`DELETE FROM call_payload WHERE id = ?`, payloadID); err != nil {
			t.Errorf("cleanup referenced MySQL payload %q: %v", payloadID, err)
		}
	}
	_, _ = db.Exec(`SET FOREIGN_KEY_CHECKS = 1`)
}

func assertMySQLOrphanMaintenanceReferences(t *testing.T, db *sql.DB, ids mysqlOrphanFixtureIDs) {
	t.Helper()
	for _, item := range []struct {
		query string
		id    string
		want  sql.NullString
	}{
		{`SELECT linked_conversation_id FROM message WHERE id = ?`, ids.message, sql.NullString{String: ids.restoredConversation, Valid: true}},
		{`SELECT conversation_id FROM schedule WHERE id = ?`, ids.schedule, sql.NullString{}},
	} {
		var got sql.NullString
		if err := db.QueryRow(item.query, item.id).Scan(&got); err != nil || got != item.want {
			t.Fatalf("reference query=%q got=%#v want=%#v err=%v", item.query, got, item.want, err)
		}
	}
}

func mysqlOrphanFixtureCounts(t *testing.T, db *sql.DB, ids mysqlOrphanFixtureIDs) map[string]int {
	t.Helper()
	queries := map[string]struct {
		query string
		args  []interface{}
	}{
		"conversation":   {`SELECT COUNT(*) FROM conversation WHERE id = ?`, []interface{}{ids.conversation}},
		"message":        {`SELECT COUNT(*) FROM message WHERE id = ?`, []interface{}{ids.message}},
		"payload":        {`SELECT COUNT(*) FROM call_payload WHERE id IN (?, ?)`, []interface{}{ids.payloadOld, ids.payloadRecent}},
		"claim":          {`SELECT COUNT(*) FROM tool_execution_claim WHERE claim_key = ?`, []interface{}{ids.claim}},
		"schedule":       {`SELECT COUNT(*) FROM schedule WHERE id = ?`, []interface{}{ids.schedule}},
		"schedule_run":   {`SELECT COUNT(*) FROM schedule_run WHERE id = ?`, []interface{}{ids.scheduleRun}},
		"investigation":  {`SELECT COUNT(*) FROM investigation WHERE id = ?`, []interface{}{ids.investigation}},
		"report_context": {`SELECT COUNT(*) FROM conversation_report_context WHERE conversation_id = ?`, []interface{}{ids.conversation}},
		"audit":          {`SELECT COUNT(*) FROM report_audit_event WHERE event_id = ?`, []interface{}{ids.audit}},
		"shared":         {`SELECT COUNT(*) FROM report_shared_artifact WHERE artifact_id = ?`, []interface{}{ids.shared}},
	}
	result := make(map[string]int, len(queries))
	for name, item := range queries {
		var count int
		if err := db.QueryRow(item.query, item.args...).Scan(&count); err != nil {
			t.Fatalf("count MySQL orphan fixture %s: %v", name, err)
		}
		result[name] = count
	}
	return result
}
