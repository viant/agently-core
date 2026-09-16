package data

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/viant/agently-core/internal/testutil/dbtest"
)

func TestListConversationMaintenanceCandidates_InteractiveKeysetAndFilters(t *testing.T) {
	old := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	cutoff := old.Add(24 * time.Hour)
	svc, _ := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"old-a", old, old, "running", "owner-1"}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"old-b", old, old, "succeeded", "owner-2"}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"recent", old, cutoff.Add(time.Second), "succeeded", "owner-1"}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id, conversation_parent_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"ordinary-child", old, old, "succeeded", "owner-1", "old-a"}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"linked-child", old, old, "succeeded", "owner-1"}},
			{SQL: `INSERT INTO message (id, conversation_id, role, type, linked_conversation_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"link-message", "old-a", "assistant", "text", "linked-child"}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id, schedule_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-id", old, old, "succeeded", "owner-1", "schedule-1"}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id, scheduled) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-flag", old, old, "succeeded", "owner-1", 1}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-run", old, old, "succeeded", "owner-1"}},
			{SQL: `INSERT INTO run (id, conversation_id, conversation_kind, status) VALUES (?, ?, ?, ?)`, Params: []interface{}{"scheduled-run-row", "scheduled-run", "scheduled", "completed"}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status) VALUES (?, ?, ?, ?)`, Params: []interface{}{"ownerless", old, old, "succeeded"}},
		})
	})

	request := ConversationMaintenanceCandidateRequest{
		Kind:           ConversationMaintenanceInteractive,
		InactiveBefore: cutoff,
		Limit:          1,
	}
	first, err := svc.ListConversationMaintenanceCandidates(context.Background(), request)
	if err != nil {
		t.Fatalf("ListConversationMaintenanceCandidates(first) error: %v", err)
	}
	assertMaintenanceCandidateIDs(t, first, "old-a")
	if first[0].ExpectedOwnerID != "owner-1" || !first[0].ActivityAt.Equal(old) {
		t.Fatalf("first candidate = %#v", first[0])
	}

	request.AfterActivity = first[0].ActivityAt
	request.AfterRootID = first[0].RootID
	second, err := svc.ListConversationMaintenanceCandidates(context.Background(), request)
	if err != nil {
		t.Fatalf("ListConversationMaintenanceCandidates(second) error: %v", err)
	}
	assertMaintenanceCandidateIDs(t, second, "old-b")

	request.AfterActivity = second[0].ActivityAt
	request.AfterRootID = second[0].RootID
	third, err := svc.ListConversationMaintenanceCandidates(context.Background(), request)
	if err != nil {
		t.Fatalf("ListConversationMaintenanceCandidates(third) error: %v", err)
	}
	assertMaintenanceCandidateIDs(t, third)
}

func TestListConversationMaintenanceCandidates_ScheduledMarkers(t *testing.T) {
	old := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	svc, _ := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, created_by_user_id) VALUES (?, ?, ?, ?)`, Params: []interface{}{"interactive", old, old, "owner-1"}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, created_by_user_id, schedule_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-a", old, old, "owner-1", "schedule-1"}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, created_by_user_id, schedule_kind) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-b", old, old, "owner-1", "cron"}},
		})
	})

	candidates, err := svc.ListConversationMaintenanceCandidates(context.Background(), ConversationMaintenanceCandidateRequest{
		Kind:           ConversationMaintenanceScheduled,
		InactiveBefore: old.Add(time.Hour),
		Limit:          10,
	})
	if err != nil {
		t.Fatalf("ListConversationMaintenanceCandidates(scheduled) error: %v", err)
	}
	assertMaintenanceCandidateIDs(t, candidates, "scheduled-a", "scheduled-b")
}

func TestListConversationMaintenanceCandidates_ScheduledFallbackOnlyReturnsOldShellsWithoutRuns(t *testing.T) {
	old := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	cutoff := old.Add(24 * time.Hour)
	svc, _ := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, schedule_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"fallback-ownerless", old, old, "succeeded", "schedule-1"}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id, schedule_kind) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"fallback-owned", old, old, "succeeded", "owner-1", "cron"}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id, schedule_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-with-run", old, old, "succeeded", "owner-1", "schedule-2"}},
			{SQL: `INSERT INTO run (id, conversation_id, conversation_kind, status) VALUES (?, ?, ?, ?)`, Params: []interface{}{"existing-run", "scheduled-with-run", "scheduled", "completed"}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id, schedule_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"recent-shell", old, cutoff.Add(time.Second), "succeeded", "owner-1", "schedule-3"}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"interactive", old, old, "succeeded", "owner-1"}},
		})
	})

	candidates, err := svc.ListConversationMaintenanceCandidates(context.Background(), ConversationMaintenanceCandidateRequest{
		Kind:           ConversationMaintenanceScheduledFallback,
		InactiveBefore: cutoff,
		Limit:          10,
	})
	if err != nil {
		t.Fatalf("ListConversationMaintenanceCandidates(scheduled fallback) error: %v", err)
	}
	assertMaintenanceCandidateIDs(t, candidates, "fallback-owned", "fallback-ownerless")
	if candidates[0].ExpectedOwnerID != "owner-1" || candidates[1].ExpectedOwnerID != "" {
		t.Fatalf("fallback candidate owners = %#v", candidates)
	}
}

func TestListConversationMaintenanceCandidates_ValidatesSafetyInputs(t *testing.T) {
	svc := newSeededService(t)
	_, err := svc.ListConversationMaintenanceCandidates(context.Background(), ConversationMaintenanceCandidateRequest{})
	if !errors.Is(err, ErrInvalidConversationMaintenanceRequest) {
		t.Fatalf("expected ErrInvalidConversationMaintenanceRequest, got %v", err)
	}

	_, err = svc.ListConversationMaintenanceCandidates(context.Background(), ConversationMaintenanceCandidateRequest{
		Kind:           ConversationMaintenanceInteractive,
		InactiveBefore: time.Now(),
		AfterRootID:    "cursor-without-time",
		Limit:          1,
	})
	if !errors.Is(err, ErrInvalidConversationMaintenanceRequest) {
		t.Fatalf("expected invalid cursor error, got %v", err)
	}
}

func assertMaintenanceCandidateIDs(t *testing.T, candidates []ConversationMaintenanceCandidate, want ...string) {
	t.Helper()
	if len(candidates) != len(want) {
		t.Fatalf("candidate count = %d, want %d; candidates=%#v", len(candidates), len(want), candidates)
	}
	for i, candidate := range candidates {
		if candidate.RootID != want[i] {
			t.Fatalf("candidate[%d].RootID = %q, want %q; candidates=%#v", i, candidate.RootID, want[i], candidates)
		}
	}
}
