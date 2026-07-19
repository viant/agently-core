package data

import (
	"context"
	"database/sql"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTerminalArtifactCleanup_StatusAndLinkageMatrix(t *testing.T) {
	service, db := newSeededServiceWithDB(t)
	now := time.Now().UTC()
	seedTerminalArtifactMatrix(t, db, now)
	store := service.(TerminalArtifactCleanupStore)

	candidates, err := store.SnapshotTerminalArtifactCandidates(context.Background(), now.Add(-60*24*time.Hour), 5000)
	if err != nil {
		t.Fatalf("SnapshotTerminalArtifactCandidates() error: %v", err)
	}
	if len(candidates) != 9 {
		t.Fatalf("candidate count=%d, want 9: %+v", len(candidates), candidates)
	}
	wantLinkages := map[string]TerminalArtifactLinkage{
		"model_call/model-direct":  TerminalArtifactDirectTurn,
		"model_call/model-message": TerminalArtifactMessageTurn,
		"model_call/model-run":     TerminalArtifactRun,
		"model_call/model-legacy":  TerminalArtifactLegacyRun,
		"tool_call/tool-direct":    TerminalArtifactDirectTurn,
		"tool_call/tool-message":   TerminalArtifactMessageTurn,
		"tool_call/tool-run":       TerminalArtifactRun,
		"tool_call/tool-legacy":    TerminalArtifactLegacyRun,
		"message/message-only":     TerminalArtifactDirectTurn,
	}
	for _, candidate := range candidates {
		key := string(candidate.Kind) + "/" + candidate.ID
		if got, ok := wantLinkages[key]; !ok || got != candidate.Linkage {
			t.Fatalf("candidate %s linkage=%q, want %q", key, candidate.Linkage, got)
		}
		if candidate.ConversationID != "cleanup-conversation" || candidate.TurnID == "" || candidate.TerminalStatus == "" || candidate.Reason == "" {
			t.Fatalf("candidate did not freeze required guard values: %+v", candidate)
		}
		delete(wantLinkages, key)
	}
	if len(wantLinkages) != 0 {
		t.Fatalf("missing candidates: %+v", wantLinkages)
	}
	if _, ok := reflect.TypeOf(TerminalArtifactCandidate{}).FieldByName("Content"); ok {
		t.Fatalf("cleanup candidate must not retain message content")
	}

	dispositions, err := store.CleanupTerminalArtifactCandidates(context.Background(), candidates, now)
	if err != nil {
		t.Fatalf("CleanupTerminalArtifactCandidates() error: %v", err)
	}
	if len(dispositions) != len(candidates) {
		t.Fatalf("disposition count=%d, want %d", len(dispositions), len(candidates))
	}
	for i, disposition := range dispositions {
		if disposition != TerminalArtifactRepaired {
			t.Fatalf("disposition[%d]=%v, want repaired", i, disposition)
		}
	}

	for _, id := range []string{"model-direct", "model-message", "model-run", "model-legacy"} {
		assertTerminalCallRow(t, db, "model_call", id, false)
	}
	for _, id := range []string{"tool-direct", "tool-message", "tool-run", "tool-legacy"} {
		assertTerminalCallRow(t, db, "tool_call", id, id == "tool-message")
	}
	var messageStatus string
	var updatedAt sql.NullTime
	if err := db.QueryRow(`SELECT status, updated_at FROM message WHERE id = ?`, "message-only").Scan(&messageStatus, &updatedAt); err != nil {
		t.Fatalf("load repaired tool message: %v", err)
	}
	if messageStatus != "failed" || !updatedAt.Valid {
		t.Fatalf("tool message status=%q updated_at=%v", messageStatus, updatedAt)
	}
}

func TestTerminalArtifactCleanup_TerminalStatusChangeInvalidatesEveryLinkage(t *testing.T) {
	service, db := newSeededServiceWithDB(t)
	now := time.Now().UTC()
	seedTerminalArtifactMatrix(t, db, now)
	store := service.(TerminalArtifactCleanupStore)

	candidates, err := store.SnapshotTerminalArtifactCandidates(context.Background(), now.Add(-60*24*time.Hour), 5000)
	if err != nil {
		t.Fatalf("SnapshotTerminalArtifactCandidates() error: %v", err)
	}
	if len(candidates) != 9 {
		t.Fatalf("candidate count=%d, want 9", len(candidates))
	}
	for _, candidate := range candidates {
		changedStatus := "failed"
		switch candidate.TerminalStatus {
		case "failed":
			changedStatus = "succeeded"
		case "succeeded":
			changedStatus = "canceled"
		}
		mustExecCleanup(t, db, `UPDATE turn SET status = ? WHERE id = ?`, changedStatus, candidate.TurnID)
	}

	dispositions, err := store.CleanupTerminalArtifactCandidates(context.Background(), candidates, now)
	if err != nil {
		t.Fatalf("CleanupTerminalArtifactCandidates() error: %v", err)
	}
	for i, disposition := range dispositions {
		if disposition != TerminalArtifactNoLongerEligible {
			t.Fatalf("disposition[%d]=%v, want no_longer_eligible", i, disposition)
		}
		assertTerminalArtifactNotCleaned(t, db, candidates[i])
	}
}

func TestTerminalArtifactSnapshot_DeduplicatesByPrecedenceThenNewestTurn(t *testing.T) {
	service, db := newSeededServiceWithDB(t)
	now := time.Now().UTC()
	mustExecCleanup(t, db, `INSERT INTO conversation (id, created_at) VALUES (?, ?)`, "cleanup-conversation", now.Add(-3*time.Hour))
	insertCleanupTurn(t, db, "turn-direct", "failed", "run-direct", "direct failure", now.Add(-3*time.Hour))
	insertCleanupTurn(t, db, "turn-message", "succeeded", "", "", now.Add(-2*time.Hour))
	insertCleanupTurn(t, db, "turn-run-new", "canceled", "shared-run", "", now.Add(-time.Hour))
	insertCleanupTurn(t, db, "turn-run-old", "failed", "shared-run", "", now.Add(-90*time.Minute))
	insertCleanupTurn(t, db, "turn-old", "failed", "", "", now.Add(-61*24*time.Hour))

	insertCleanupMessage(t, db, "precedence", "turn-message", "assistant", "text", "running", now)
	insertCleanupModelCall(t, db, "precedence", "turn-direct", "shared-run", "running")
	insertCleanupMessage(t, db, "newest", "", "assistant", "text", "running", now)
	insertCleanupModelCall(t, db, "newest", "", "shared-run", "thinking")
	insertCleanupMessage(t, db, "old-artifact", "turn-old", "assistant", "text", "running", now)
	insertCleanupModelCall(t, db, "old-artifact", "turn-old", "", "streaming")
	insertCleanupMessage(t, db, "terminal-artifact", "turn-direct", "assistant", "text", "running", now)
	insertCleanupModelCall(t, db, "terminal-artifact", "turn-direct", "", "completed")
	insertCleanupMessage(t, db, "plain-message", "turn-direct", "assistant", "text", "running", now)

	store := service.(TerminalArtifactCleanupStore)
	candidates, err := store.SnapshotTerminalArtifactCandidates(context.Background(), now.Add(-60*24*time.Hour), 5000)
	if err != nil {
		t.Fatalf("snapshot error: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidate count=%d, want 2: %+v", len(candidates), candidates)
	}
	byID := map[string]TerminalArtifactCandidate{}
	for _, candidate := range candidates {
		byID[candidate.ID] = candidate
	}
	if got := byID["precedence"]; got.Linkage != TerminalArtifactDirectTurn || got.TurnID != "turn-direct" || got.Reason != "direct failure" {
		t.Fatalf("precedence candidate=%+v", got)
	}
	if got := byID["newest"]; got.Linkage != TerminalArtifactRun || got.TurnID != "turn-run-new" || got.Reason != "turn reached terminal status canceled" {
		t.Fatalf("newest run candidate=%+v", got)
	}
}

func TestTerminalArtifactCleanup_ConcurrentCompletionAndEligibilityChanges(t *testing.T) {
	service, db := newSeededServiceWithDB(t)
	now := time.Now().UTC()
	seedSingleDirectCleanupCandidate(t, db, now)
	store := service.(TerminalArtifactCleanupStore)
	candidates, err := store.SnapshotTerminalArtifactCandidates(context.Background(), now.Add(-time.Hour), 5000)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("snapshot candidates=%+v err=%v", candidates, err)
	}
	candidate := candidates[0]

	mustExecCleanup(t, db, `UPDATE model_call SET status = 'completed' WHERE message_id = ?`, candidate.ID)
	mustExecCleanup(t, db, `UPDATE turn SET status = 'succeeded' WHERE id = ?`, candidate.TurnID)
	dispositions, err := store.CleanupTerminalArtifactCandidates(context.Background(), []TerminalArtifactCandidate{candidate}, now)
	if err != nil || len(dispositions) != 1 || dispositions[0] != TerminalArtifactAlreadyResolved {
		t.Fatalf("concurrent completion dispositions=%v err=%v", dispositions, err)
	}

	mustExecCleanup(t, db, `UPDATE turn SET status = 'failed' WHERE id = ?`, candidate.TurnID)
	mustExecCleanup(t, db, `UPDATE model_call SET status = 'running', turn_id = ? WHERE message_id = ?`, "other-turn", candidate.ID)
	dispositions, err = store.CleanupTerminalArtifactCandidates(context.Background(), []TerminalArtifactCandidate{candidate}, now)
	if err != nil || dispositions[0] != TerminalArtifactNoLongerEligible {
		t.Fatalf("link change dispositions=%v err=%v", dispositions, err)
	}

	mustExecCleanup(t, db, `UPDATE model_call SET turn_id = ? WHERE message_id = ?`, candidate.TurnID, candidate.ID)
	mustExecCleanup(t, db, `UPDATE turn SET status = 'running' WHERE id = ?`, candidate.TurnID)
	dispositions, err = store.CleanupTerminalArtifactCandidates(context.Background(), []TerminalArtifactCandidate{candidate}, now)
	if err != nil || dispositions[0] != TerminalArtifactNoLongerEligible {
		t.Fatalf("turn status change dispositions=%v err=%v", dispositions, err)
	}

	mustExecCleanup(t, db, `UPDATE turn SET status = 'failed' WHERE id = ?`, candidate.TurnID)
	base := service.(*datlyService)
	secondStore := NewService(base.dao).(TerminalArtifactCleanupStore)
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []TerminalArtifactDisposition
		errs    []error
	)
	for _, concurrentStore := range []TerminalArtifactCleanupStore{store, secondStore} {
		wg.Add(1)
		go func(concurrentStore TerminalArtifactCleanupStore) {
			defer wg.Done()
			got, cleanupErr := concurrentStore.CleanupTerminalArtifactCandidates(context.Background(), []TerminalArtifactCandidate{candidate}, now)
			mu.Lock()
			defer mu.Unlock()
			if cleanupErr != nil {
				errs = append(errs, cleanupErr)
				return
			}
			results = append(results, got...)
		}(concurrentStore)
	}
	wg.Wait()
	if len(errs) != 0 {
		t.Fatalf("concurrent cleanup errors: %v", errs)
	}
	sort.Slice(results, func(i, j int) bool { return results[i] < results[j] })
	want := []TerminalArtifactDisposition{TerminalArtifactRepaired, TerminalArtifactAlreadyResolved}
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if !reflect.DeepEqual(results, want) {
		t.Fatalf("concurrent dispositions=%v, want %v", results, want)
	}
}

func TestTerminalArtifactCleanup_RollsBackEntireBatch(t *testing.T) {
	service, db := newSeededServiceWithDB(t)
	now := time.Now().UTC()
	mustExecCleanup(t, db, `INSERT INTO conversation (id, created_at) VALUES (?, ?)`, "cleanup-conversation", now.Add(-time.Hour))
	insertCleanupTurn(t, db, "rollback-turn", "failed", "", "batch failed", now.Add(-time.Minute))
	for _, id := range []string{"rollback-first", "rollback-second"} {
		insertCleanupMessage(t, db, id, "rollback-turn", "assistant", "text", "running", now)
		insertCleanupModelCall(t, db, id, "rollback-turn", "", "running")
	}
	store := service.(TerminalArtifactCleanupStore)
	candidates, err := store.SnapshotTerminalArtifactCandidates(context.Background(), now.Add(-time.Hour), 5000)
	if err != nil || len(candidates) != 2 {
		t.Fatalf("snapshot candidates=%+v err=%v", candidates, err)
	}
	mustExecCleanup(t, db, `CREATE TRIGGER fail_terminal_cleanup
        BEFORE UPDATE OF status ON model_call
        WHEN NEW.message_id = 'rollback-second'
        BEGIN
            SELECT RAISE(ABORT, 'forced cleanup failure');
        END`)
	if dispositions, err := store.CleanupTerminalArtifactCandidates(context.Background(), candidates, now); err == nil || dispositions != nil {
		t.Fatalf("rollback cleanup dispositions=%v err=%v", dispositions, err)
	}
	for _, id := range []string{"rollback-first", "rollback-second"} {
		var status string
		var completedAt sql.NullTime
		if err := db.QueryRow(`SELECT status, completed_at FROM model_call WHERE message_id = ?`, id).Scan(&status, &completedAt); err != nil {
			t.Fatalf("load rollback row: %v", err)
		}
		if status != "running" || completedAt.Valid {
			t.Fatalf("row %s was not rolled back: status=%q completed=%v", id, status, completedAt)
		}
	}
}

func seedTerminalArtifactMatrix(t *testing.T, db *sql.DB, now time.Time) {
	t.Helper()
	mustExecCleanup(t, db, `INSERT INTO conversation (id, created_at) VALUES (?, ?)`, "cleanup-conversation", now.Add(-time.Hour))
	types := []struct {
		kind    TerminalArtifactKind
		id      string
		linkage TerminalArtifactLinkage
		status  string
	}{
		{TerminalArtifactModelCall, "model-direct", TerminalArtifactDirectTurn, "thinking"},
		{TerminalArtifactModelCall, "model-message", TerminalArtifactMessageTurn, "streaming"},
		{TerminalArtifactModelCall, "model-run", TerminalArtifactRun, "running"},
		{TerminalArtifactModelCall, "model-legacy", TerminalArtifactLegacyRun, "thinking"},
		{TerminalArtifactToolCall, "tool-direct", TerminalArtifactDirectTurn, "queued"},
		{TerminalArtifactToolCall, "tool-message", TerminalArtifactMessageTurn, "running"},
		{TerminalArtifactToolCall, "tool-run", TerminalArtifactRun, "waiting_for_user"},
		{TerminalArtifactToolCall, "tool-legacy", TerminalArtifactLegacyRun, "queued"},
	}
	terminalStatuses := []string{"failed", "succeeded", "canceled"}
	for i, item := range types {
		turnID := item.id + "-turn"
		turnRun := ""
		artifactTurn := ""
		artifactRun := ""
		messageTurn := ""
		switch item.linkage {
		case TerminalArtifactDirectTurn:
			artifactTurn = turnID
			messageTurn = turnID
		case TerminalArtifactMessageTurn:
			messageTurn = turnID
		case TerminalArtifactRun:
			turnRun = item.id + "-run"
			artifactRun = turnRun
		case TerminalArtifactLegacyRun:
			artifactRun = turnID
		}
		reason := ""
		if i == 0 {
			reason = "direct turn failed"
		}
		insertCleanupTurn(t, db, turnID, terminalStatuses[i%len(terminalStatuses)], turnRun, reason, now.Add(time.Duration(i)*time.Second))
		insertCleanupMessage(t, db, item.id, messageTurn, "assistant", "text", "running", now)
		if item.kind == TerminalArtifactModelCall {
			insertCleanupModelCall(t, db, item.id, artifactTurn, artifactRun, item.status)
		} else {
			insertCleanupToolCall(t, db, item.id, artifactTurn, artifactRun, item.status)
		}
	}
	insertCleanupTurn(t, db, "message-only-turn", "failed", "", "message turn failed", now.Add(20*time.Second))
	insertCleanupMessage(t, db, "message-only", "message-only-turn", "tool", "text", "open", now)
	insertCleanupMessage(t, db, "plain-running-message", "message-only-turn", "assistant", "text", "running", now)
	insertCleanupMessage(t, db, "terminal-tool-message", "message-only-turn", "tool", "tool_op", "completed", now)
}

func seedSingleDirectCleanupCandidate(t *testing.T, db *sql.DB, now time.Time) {
	t.Helper()
	mustExecCleanup(t, db, `INSERT INTO conversation (id, created_at) VALUES (?, ?)`, "cleanup-conversation", now.Add(-time.Hour))
	insertCleanupTurn(t, db, "direct-turn", "failed", "", "direct failed", now.Add(-time.Minute))
	insertCleanupTurn(t, db, "other-turn", "running", "", "", now)
	insertCleanupMessage(t, db, "direct-model", "direct-turn", "assistant", "text", "running", now)
	insertCleanupModelCall(t, db, "direct-model", "direct-turn", "", "running")
}

func insertCleanupTurn(t *testing.T, db *sql.DB, id, status, runID, reason string, createdAt time.Time) {
	t.Helper()
	mustExecCleanup(t, db, `INSERT INTO turn (id, conversation_id, created_at, status, run_id, error_message) VALUES (?, ?, ?, ?, ?, ?)`, id, "cleanup-conversation", createdAt, status, nullableCleanupString(runID), nullableCleanupString(reason))
}

func insertCleanupMessage(t *testing.T, db *sql.DB, id, turnID, role, messageType, status string, createdAt time.Time) {
	t.Helper()
	mustExecCleanup(t, db, `INSERT INTO message (id, conversation_id, turn_id, role, type, status, content, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, "cleanup-conversation", nullableCleanupString(turnID), role, messageType, nullableCleanupString(status), "content must not be captured", createdAt)
}

func insertCleanupModelCall(t *testing.T, db *sql.DB, id, turnID, runID, status string) {
	t.Helper()
	mustExecCleanup(t, db, `INSERT INTO model_call (message_id, turn_id, provider, model, model_kind, status, run_id) VALUES (?, ?, ?, ?, ?, ?, ?)`, id, nullableCleanupString(turnID), "openai", "test-model", "chat", status, nullableCleanupString(runID))
}

func insertCleanupToolCall(t *testing.T, db *sql.DB, id, turnID, runID, status string) {
	t.Helper()
	mustExecCleanup(t, db, `INSERT INTO tool_call (message_id, turn_id, op_id, tool_name, tool_kind, status, run_id) VALUES (?, ?, ?, ?, ?, ?, ?)`, id, nullableCleanupString(turnID), id+"-op", "test-tool", "general", status, nullableCleanupString(runID))
}

func assertTerminalCallRow(t *testing.T, db *sql.DB, table, id string, wantMessagePathReason bool) {
	t.Helper()
	if table != "model_call" && table != "tool_call" {
		t.Fatalf("unsupported table %q", table)
	}
	var status, reason string
	var completedAt sql.NullTime
	query := "SELECT status, COALESCE(error_message, ''), completed_at FROM " + table + " WHERE message_id = ?"
	if err := db.QueryRow(query, id).Scan(&status, &reason, &completedAt); err != nil {
		t.Fatalf("load %s row: %v", table, err)
	}
	if status != "failed" || !completedAt.Valid {
		t.Fatalf("%s row %s status=%q completed=%v", table, id, status, completedAt)
	}
	if wantMessagePathReason && reason != "tool message terminalized after turn ended" {
		t.Fatalf("tool message-path reason=%q", reason)
	}
	if !wantMessagePathReason && strings.TrimSpace(reason) == "" {
		t.Fatalf("%s row %s has empty failure reason", table, id)
	}
}

func assertTerminalArtifactNotCleaned(t *testing.T, db *sql.DB, candidate TerminalArtifactCandidate) {
	t.Helper()
	var status string
	var terminalAt sql.NullTime
	switch candidate.Kind {
	case TerminalArtifactModelCall:
		if err := db.QueryRow(`SELECT status, completed_at FROM model_call WHERE message_id = ?`, candidate.ID).Scan(&status, &terminalAt); err != nil {
			t.Fatalf("load model call after status change: %v", err)
		}
	case TerminalArtifactToolCall:
		if err := db.QueryRow(`SELECT status, completed_at FROM tool_call WHERE message_id = ?`, candidate.ID).Scan(&status, &terminalAt); err != nil {
			t.Fatalf("load tool call after status change: %v", err)
		}
	case TerminalArtifactMessage:
		if err := db.QueryRow(`SELECT COALESCE(status, ''), updated_at FROM message WHERE id = ?`, candidate.ID).Scan(&status, &terminalAt); err != nil {
			t.Fatalf("load message after status change: %v", err)
		}
	default:
		t.Fatalf("unexpected candidate kind %q", candidate.Kind)
	}
	if status == "failed" || terminalAt.Valid {
		t.Fatalf("candidate was cleaned after terminal status changed: kind=%q status=%q terminal_at=%v", candidate.Kind, status, terminalAt)
	}
}

func mustExecCleanup(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("cleanup test SQL failed: %v", err)
	}
}

func nullableCleanupString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
