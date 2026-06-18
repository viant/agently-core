package scheduler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/viant/agently-core/internal/testutil/dbtest"
	schrun "github.com/viant/agently-core/pkg/agently/scheduler/run"
	schedwrite "github.com/viant/agently-core/pkg/agently/scheduler/schedule/write"
	agentsvc "github.com/viant/agently-core/service/agent"
	svcauth "github.com/viant/agently-core/service/auth"
	"github.com/viant/datly"
	"github.com/viant/datly/view"
	_ "modernc.org/sqlite"
)

func TestDatlyStore_PatchScheduleRoundTrip(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()

	row := &schedwrite.Schedule{}
	row.SetId("sched-1")
	row.SetName("Scheduler Store Test")
	row.SetVisibility("public")
	row.SetAgentRef("simple")
	row.SetEnabled(true)
	row.SetScheduleType("adhoc")
	row.SetTimezone("UTC")
	row.SetTaskPrompt("Say hello")
	if err := store.PatchSchedule(ctx, row); err != nil {
		t.Fatalf("PatchSchedule() error: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM schedule WHERE id = ?`, "sched-1").Scan(&count); err != nil {
		t.Fatalf("QueryRowContext() error: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected schedule row to be inserted, got count=%d", count)
	}

	got, err := store.Get(ctx, "sched-1")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got == nil {
		t.Fatalf("expected persisted schedule to be readable")
	}
	if got.Name != "Scheduler Store Test" {
		t.Fatalf("unexpected schedule name: %q", got.Name)
	}
}

func TestService_UpsertPersistsSchedule(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, nil)
	prompt := "Say hello"

	err := svc.Upsert(context.Background(), &Schedule{
		ID:           "sched-service-1",
		Name:         "Service Schedule",
		Visibility:   "public",
		AgentRef:     "simple",
		Enabled:      true,
		ScheduleType: "adhoc",
		Timezone:     "UTC",
		TaskPrompt:   &prompt,
	})
	if err != nil {
		t.Fatalf("Upsert() error: %v", err)
	}

	assertScheduleCount(t, db, "sched-service-1", 1)
}

func TestService_UpsertUpdatesDescriptionAndTaskPrompt(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, nil)
	initialPrompt := "Say hello"
	initialDescription := "initial description"

	err := svc.Upsert(context.Background(), &Schedule{
		ID:           "sched-service-update-1",
		Name:         "Service Schedule Update",
		Description:  &initialDescription,
		Visibility:   "public",
		AgentRef:     "simple",
		Enabled:      true,
		ScheduleType: "adhoc",
		Timezone:     "UTC",
		TaskPrompt:   &initialPrompt,
	})
	if err != nil {
		t.Fatalf("initial Upsert() error: %v", err)
	}

	nextPrompt := "Say goodbye"
	nextDescription := "updated description"
	err = svc.Upsert(context.Background(), &Schedule{
		ID:           "sched-service-update-1",
		Name:         "Service Schedule Update",
		Description:  &nextDescription,
		Visibility:   "public",
		AgentRef:     "simple",
		Enabled:      true,
		ScheduleType: "adhoc",
		Timezone:     "UTC",
		TaskPrompt:   &nextPrompt,
	})
	if err != nil {
		t.Fatalf("update Upsert() error: %v", err)
	}

	assertScheduleCount(t, db, "sched-service-update-1", 1)
	assertScheduleTextFields(t, db, "sched-service-update-1", nextDescription, nextPrompt)
}

func TestHandler_BatchUpdatePersistsSchedule(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, nil)
	h := NewHandler(svc)

	body, err := json.Marshal(map[string]interface{}{
		"schedules": []map[string]interface{}{
			{
				"id":           "sched-http-1",
				"name":         "HTTP Schedule",
				"visibility":   "public",
				"agentRef":     "simple",
				"enabled":      true,
				"scheduleType": "adhoc",
				"timezone":     "UTC",
				"taskPrompt":   "Say hello",
			},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/v1/api/agently/scheduler/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.handleBatchUpdate()(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}

	assertScheduleCount(t, db, "sched-http-1", 1)
}

func TestHandler_BatchUpdateAnonymousPrivateInsertBecomesPublic(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, nil)
	h := NewHandler(svc)

	body, err := json.Marshal(map[string]interface{}{
		"schedules": []map[string]interface{}{
			{
				"id":           "sched-http-anon-private",
				"name":         "HTTP Anonymous Private Schedule",
				"visibility":   "private",
				"agentRef":     "simple",
				"enabled":      true,
				"scheduleType": "adhoc",
				"timezone":     "UTC",
				"taskPrompt":   "Say hello",
			},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/v1/api/agently/scheduler/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.handleBatchUpdate()(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}

	assertScheduleCount(t, db, "sched-http-anon-private", 1)
	assertScheduleVisibility(t, db, "sched-http-anon-private", "public")

	list, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected anonymous list to include coerced-public schedule, got %d rows", len(list))
	}
}

func TestHandler_BatchUpdateUpdatesDescriptionAndTaskPrompt(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, nil)
	h := NewHandler(svc)

	initialBody, err := json.Marshal(map[string]interface{}{
		"schedules": []map[string]interface{}{
			{
				"id":           "sched-http-update-1",
				"name":         "HTTP Schedule Update",
				"description":  "before",
				"visibility":   "public",
				"agentRef":     "simple",
				"enabled":      true,
				"scheduleType": "adhoc",
				"timezone":     "UTC",
				"taskPrompt":   "hello",
			},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/v1/api/agently/scheduler/", bytes.NewReader(initialBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handleBatchUpdate()(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("initial update unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}

	updateBody, err := json.Marshal(map[string]interface{}{
		"schedules": []map[string]interface{}{
			{
				"id":           "sched-http-update-1",
				"name":         "HTTP Schedule Update",
				"description":  "after",
				"visibility":   "public",
				"agentRef":     "simple",
				"enabled":      true,
				"scheduleType": "adhoc",
				"timezone":     "UTC",
				"taskPrompt":   "goodbye",
			},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}

	req = httptest.NewRequest(http.MethodPatch, "/v1/api/agently/scheduler/", bytes.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.handleBatchUpdate()(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("update unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}

	assertScheduleTextFields(t, db, "sched-http-update-1", "after", "goodbye")
}

func TestHandler_BatchUpdateDescriptionPreservesNextRunAt(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, nil)
	h := NewHandler(svc)

	nextRunAt := time.Date(2026, 6, 18, 9, 0, 0, 0, time.UTC)
	createdAt := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO schedule (
			id, name, description, visibility, agent_ref, enabled, schedule_type, cron_expr, timezone,
			next_run_at, timeout_seconds, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "sched-description-preserve", "Description Preserve", "before", "public", "simple", 1, "cron", "0 9 * * *", "UTC", nextRunAt, 0, createdAt, createdAt); err != nil {
		t.Fatalf("insert schedule error: %v", err)
	}

	body, err := json.Marshal(map[string]interface{}{
		"schedules": []map[string]interface{}{
			{
				"id":           "sched-description-preserve",
				"name":         "Description Preserve",
				"description":  "after",
				"visibility":   "public",
				"agentRef":     "simple",
				"enabled":      true,
				"scheduleType": "cron",
				"cronExpr":     "0 9 * * *",
				"timezone":     "UTC",
			},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/v1/api/agently/scheduler/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handleBatchUpdate()(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("update unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}

	var description string
	var gotNextRunAt time.Time
	if err := db.QueryRowContext(context.Background(), `SELECT description, next_run_at FROM schedule WHERE id = ?`, "sched-description-preserve").Scan(&description, &gotNextRunAt); err != nil {
		t.Fatalf("query schedule error: %v", err)
	}
	if description != "after" {
		t.Fatalf("description = %q, want after", description)
	}
	if !gotNextRunAt.Equal(nextRunAt) {
		t.Fatalf("next_run_at = %s, want %s", gotNextRunAt, nextRunAt)
	}
}

func TestHandler_BatchUpdateEnableClearsNextRunAt(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, nil)
	h := NewHandler(svc)

	nextRunAt := time.Date(2026, 5, 9, 9, 0, 0, 0, time.UTC)
	createdAt := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO schedule (
			id, name, visibility, agent_ref, enabled, schedule_type, cron_expr, timezone,
			next_run_at, timeout_seconds, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "sched-enable-clear", "Enable Clear", "public", "simple", 0, "cron", "0 9 * * *", "UTC", nextRunAt, 0, createdAt, createdAt); err != nil {
		t.Fatalf("insert schedule error: %v", err)
	}

	body, err := json.Marshal(map[string]interface{}{
		"schedules": []map[string]interface{}{
			{
				"id":           "sched-enable-clear",
				"name":         "Enable Clear",
				"visibility":   "public",
				"agentRef":     "simple",
				"enabled":      true,
				"scheduleType": "cron",
				"cronExpr":     "0 9 * * *",
				"timezone":     "UTC",
			},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/v1/api/agently/scheduler/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handleBatchUpdate()(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("update unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}

	var enabled bool
	var gotNextRunAt sql.NullTime
	if err := db.QueryRowContext(context.Background(), `SELECT enabled, next_run_at FROM schedule WHERE id = ?`, "sched-enable-clear").Scan(&enabled, &gotNextRunAt); err != nil {
		t.Fatalf("query schedule error: %v", err)
	}
	if !enabled {
		t.Fatalf("expected schedule to be enabled")
	}
	if gotNextRunAt.Valid {
		t.Fatalf("expected next_run_at to be cleared, got %s", gotNextRunAt.Time)
	}
}

func TestHandler_DeleteScheduleRemovesOwnedRow(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, nil)
	h := NewHandler(svc)

	insertScheduleRowWithOwner(t, db, "sched-delete-1", "Nightly", "private", "devuser")
	insertConversationRowWithOwner(t, db, "conv-delete-1", "private", "devuser")
	if _, err := db.ExecContext(context.Background(), `UPDATE conversation SET schedule_id = ? WHERE id = ?`, "sched-delete-1", "conv-delete-1"); err != nil {
		t.Fatalf("update conversation schedule_id error: %v", err)
	}
	insertSchedulerRunRow(t, db, "run-delete-1", "sched-delete-1", "conv-delete-1", "succeeded", time.Date(2026, 3, 23, 10, 0, 0, 0, time.UTC))

	req := httptest.NewRequest(http.MethodDelete, "/v1/api/agently/scheduler/schedule/sched-delete-1", nil)
	req.SetPathValue("id", "sched-delete-1")
	req = req.WithContext(svcauth.InjectUser(req.Context(), "devuser"))
	rec := httptest.NewRecorder()

	h.handleDeleteSchedule()(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}

	assertScheduleCount(t, db, "sched-delete-1", 0)
	assertTableIDCount(t, db, "conversation", "conv-delete-1", 0)
	assertTableIDCount(t, db, "run", "run-delete-1", 0)
}

func TestHandler_DeleteScheduleRejectsOtherUser(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, nil)
	h := NewHandler(svc)

	insertScheduleRowWithOwner(t, db, "sched-delete-2", "Nightly", "private", "devuser")

	req := httptest.NewRequest(http.MethodDelete, "/v1/api/agently/scheduler/schedule/sched-delete-2", nil)
	req.SetPathValue("id", "sched-delete-2")
	req = req.WithContext(svcauth.InjectUser(req.Context(), "otheruser"))
	rec := httptest.NewRecorder()

	h.handleDeleteSchedule()(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}

	assertScheduleCount(t, db, "sched-delete-2", 1)
}

func TestHandler_GetInternalScheduleReturnsNotFound(t *testing.T) {
	store, _ := newTestStore(t)
	svc := New(store, &agentsvc.Service{})
	h := NewHandler(svc)

	scheduled, err := svc.ScheduleGoalWakeup(context.Background(), agentsvc.GoalWakeupRequest{
		ConversationID: "conv-goal",
		GoalID:         "goal-conv-goal",
		UserID:         "devuser",
		AgentID:        "coder",
		WakeAt:         time.Now().UTC().Add(2 * time.Minute),
		Preview:        "Continue goal later",
		Payload:        "Continue working toward the active goal.",
	})
	if err != nil {
		t.Fatalf("ScheduleGoalWakeup() error: %v", err)
	}
	if !scheduled {
		t.Fatalf("expected wakeup to schedule")
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/api/agently/scheduler/schedule/goal-wakeup-goal-conv-goal", nil)
	req.SetPathValue("id", "goal-wakeup-goal-conv-goal")
	rec := httptest.NewRecorder()

	h.handleGetSchedule()(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandler_RunNowInternalScheduleReturnsNotFound(t *testing.T) {
	store, _ := newTestStore(t)
	svc := New(store, &agentsvc.Service{})
	h := NewHandler(svc)

	scheduled, err := svc.ScheduleGoalWakeup(context.Background(), agentsvc.GoalWakeupRequest{
		ConversationID: "conv-goal",
		GoalID:         "goal-conv-goal",
		UserID:         "devuser",
		AgentID:        "coder",
		WakeAt:         time.Now().UTC().Add(2 * time.Minute),
		Preview:        "Continue goal later",
		Payload:        "Continue working toward the active goal.",
	})
	if err != nil {
		t.Fatalf("ScheduleGoalWakeup() error: %v", err)
	}
	if !scheduled {
		t.Fatalf("expected wakeup to schedule")
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/api/agently/scheduler/run-now/goal-wakeup-goal-conv-goal", nil)
	req.SetPathValue("id", "goal-wakeup-goal-conv-goal")
	rec := httptest.NewRecorder()

	h.handleRunNow()(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandler_RunNowRateLimitReturnsTooManyRequests(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, &agentsvc.Service{})
	h := NewHandler(svc)

	insertScheduleRow(t, db, "sched-rate-limit", "Run Now Rate Limit")
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO run (id, schedule_id, conversation_kind, status, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, "run-recent-manual", "sched-rate-limit", "scheduled", "pending", time.Now().UTC()); err != nil {
		t.Fatalf("insert recent manual run error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/api/agently/scheduler/run-now/sched-rate-limit", nil)
	req.SetPathValue("id", "sched-rate-limit")
	rec := httptest.NewRecorder()

	h.handleRunNow()(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), ErrRunNowRateLimited.Error()) {
		t.Fatalf("expected rate-limit message in body, got %s", rec.Body.String())
	}
}

func TestHandler_BatchUpdateRejectsReservedInternalScheduleFields(t *testing.T) {
	store, _ := newTestStore(t)
	svc := New(store, &agentsvc.Service{})
	h := NewHandler(svc)

	body := `{"schedules":[{"id":"sched-public","name":"Public","visibility":"private","internal":true,"conversationId":"conv-goal","goalId":"goal-conv-goal","agentRef":"coder","enabled":true,"scheduleType":"adhoc","timezone":"UTC"}]}`
	req := httptest.NewRequest(http.MethodPatch, "/v1/api/agently/scheduler/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.handleBatchUpdate()(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandler_ListSchedulesSupportsPagination(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, nil)
	h := NewHandler(svc)

	insertScheduleRow(t, db, "sched-1", "Nightly")
	insertScheduleRow(t, db, "sched-2", "Hourly")
	insertScheduleRow(t, db, "sched-3", "Weekly")

	req := httptest.NewRequest(http.MethodGet, "/v1/api/agently/scheduler/?page=2&size=2", nil)
	rec := httptest.NewRecorder()

	h.handleListSchedules()(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Schedules []map[string]interface{} `json:"schedules"`
		} `json:"data"`
		Info struct {
			PageCount  int `json:"pageCount"`
			TotalCount int `json:"totalCount"`
		} `json:"info"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	if payload.Status != "ok" {
		t.Fatalf("expected status ok, got %q", payload.Status)
	}
	if payload.Info.PageCount != 2 || payload.Info.TotalCount != 3 {
		t.Fatalf("unexpected paging info: %+v", payload.Info)
	}
	if len(payload.Data.Schedules) != 1 {
		t.Fatalf("expected one schedule on page 2, got %d", len(payload.Data.Schedules))
	}
}

func TestHandler_ListSchedulesFiltersByNameBeforePagination(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, nil)
	h := NewHandler(svc)

	insertScheduleRow(t, db, "sched-1", "test5101")
	insertScheduleRow(t, db, "sched-2", "nightly")
	insertScheduleRow(t, db, "sched-3", "TEST5102")
	insertScheduleRow(t, db, "sched-4", "test34")

	req := httptest.NewRequest(http.MethodGet, "/v1/api/agently/scheduler/?name=51&page=1&size=1", nil)
	rec := httptest.NewRecorder()

	h.handleListSchedules()(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}

	payload := decodeScheduleListPayload(t, rec)
	if payload.Info.PageCount != 2 || payload.Info.TotalCount != 2 {
		t.Fatalf("unexpected filtered paging info: %+v", payload.Info)
	}
	if len(payload.Data.Schedules) != 1 {
		t.Fatalf("expected one filtered schedule on page 1, got %d", len(payload.Data.Schedules))
	}
	if got := payload.Data.Schedules[0]["name"]; got != "test5101" {
		t.Fatalf("unexpected first filtered schedule name: %v", got)
	}
}

func TestHandler_ListSchedulesSupportsQueryAliasAndBlankName(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, nil)
	h := NewHandler(svc)

	insertScheduleRow(t, db, "sched-1", "Alpha")
	insertScheduleRow(t, db, "sched-2", "Beta")
	insertScheduleRow(t, db, "sched-3", "alphabet")

	req := httptest.NewRequest(http.MethodGet, "/v1/api/agently/scheduler/?name=%20%20&query=alp&page=1&size=10", nil)
	rec := httptest.NewRecorder()

	h.handleListSchedules()(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}

	payload := decodeScheduleListPayload(t, rec)
	if payload.Info.PageCount != 1 || payload.Info.TotalCount != 2 {
		t.Fatalf("unexpected query filtered paging info: %+v", payload.Info)
	}
	if len(payload.Data.Schedules) != 2 {
		t.Fatalf("expected two query-filtered schedules, got %d", len(payload.Data.Schedules))
	}
}

func TestHandler_ListSchedulesReturnsEmptyForNoFilterMatches(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, nil)
	h := NewHandler(svc)

	insertScheduleRow(t, db, "sched-1", "Alpha")
	insertScheduleRow(t, db, "sched-2", "Beta")

	req := httptest.NewRequest(http.MethodGet, "/v1/api/agently/scheduler/?name=missing", nil)
	rec := httptest.NewRecorder()

	h.handleListSchedules()(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}

	payload := decodeScheduleListPayload(t, rec)
	if payload.Info.PageCount != 1 || payload.Info.TotalCount != 0 {
		t.Fatalf("unexpected empty filtered paging info: %+v", payload.Info)
	}
	if len(payload.Data.Schedules) != 0 {
		t.Fatalf("expected no filtered schedules, got %d", len(payload.Data.Schedules))
	}
}

func decodeScheduleListPayload(t *testing.T, rec *httptest.ResponseRecorder) struct {
	Status string `json:"status"`
	Data   struct {
		Schedules []map[string]interface{} `json:"schedules"`
	} `json:"data"`
	Info struct {
		PageCount  int `json:"pageCount"`
		TotalCount int `json:"totalCount"`
	} `json:"info"`
} {
	t.Helper()
	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Schedules []map[string]interface{} `json:"schedules"`
		} `json:"data"`
		Info struct {
			PageCount  int `json:"pageCount"`
			TotalCount int `json:"totalCount"`
		} `json:"info"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	if payload.Status != "ok" {
		t.Fatalf("expected status ok, got %q", payload.Status)
	}
	return payload
}

func newTestStore(t *testing.T) (Store, *sql.DB) {
	t.Helper()
	db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "agently-core-scheduler-store")
	t.Cleanup(cleanup)
	dbtest.LoadSQLiteSchema(t, db)

	ctx := context.Background()
	dao, err := datly.New(ctx)
	if err != nil {
		t.Fatalf("datly.New() error: %v", err)
	}
	if err = dao.AddConnectors(ctx, view.NewConnector("agently", "sqlite", dbPath)); err != nil {
		t.Fatalf("AddConnectors() error: %v", err)
	}

	store, err := NewDatlyStore(ctx, dao, nil)
	if err != nil {
		t.Fatalf("NewDatlyStore() error: %v", err)
	}
	return store, db
}

func assertScheduleCount(t *testing.T, db *sql.DB, id string, expected int) {
	t.Helper()
	assertTableIDCount(t, db, "schedule", id, expected)
}

func assertTableIDCount(t *testing.T, db *sql.DB, table, id string, expected int) {
	t.Helper()
	var count int
	if err := db.QueryRowContext(context.Background(), fmt.Sprintf(`SELECT COUNT(1) FROM %s WHERE id = ?`, table), id).Scan(&count); err != nil {
		t.Fatalf("QueryRowContext() error: %v", err)
	}
	if count != expected {
		t.Fatalf("expected %s row count %d for %s, got %d", table, expected, id, count)
	}
}

func assertScheduleVisibility(t *testing.T, db *sql.DB, id string, expected string) {
	t.Helper()
	var visibility string
	if err := db.QueryRowContext(context.Background(), `SELECT visibility FROM schedule WHERE id = ?`, id).Scan(&visibility); err != nil {
		t.Fatalf("QueryRowContext() error: %v", err)
	}
	if visibility != expected {
		t.Fatalf("expected schedule visibility %q for %s, got %q", expected, id, visibility)
	}
}

func assertScheduleTextFields(t *testing.T, db *sql.DB, id, expectedDescription, expectedTaskPrompt string) {
	t.Helper()
	var description sql.NullString
	var taskPrompt sql.NullString
	if err := db.QueryRowContext(context.Background(), `SELECT description, task_prompt FROM schedule WHERE id = ?`, id).Scan(&description, &taskPrompt); err != nil {
		t.Fatalf("QueryRowContext() error: %v", err)
	}
	if description.String != expectedDescription {
		t.Fatalf("expected description %q for %s, got %q", expectedDescription, id, description.String)
	}
	if taskPrompt.String != expectedTaskPrompt {
		t.Fatalf("expected task_prompt %q for %s, got %q", expectedTaskPrompt, id, taskPrompt.String)
	}
}

func TestHandler_ListRunsBySchedule(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, nil)
	h := NewHandler(svc)

	insertScheduleRow(t, db, "sched-1", "Nightly")
	insertConversationRow(t, db, "conv-1", "public")
	insertSchedulerRunRow(t, db, "run-1", "sched-1", "conv-1", "succeeded", time.Date(2026, 3, 23, 10, 0, 0, 0, time.UTC))

	req := httptest.NewRequest(http.MethodGet, "/v1/api/agently/scheduler/run?scheduleId=sched-1", nil)
	rec := httptest.NewRecorder()

	h.handleListRuns()(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Status string            `json:"status"`
		Data   []*schrun.RunView `json:"data"`
		Info   struct {
			PageCount  int `json:"pageCount"`
			TotalCount int `json:"totalCount"`
		} `json:"info"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	if payload.Status != "ok" {
		t.Fatalf("expected status ok, got %q", payload.Status)
	}
	if payload.Info.TotalCount != 1 || len(payload.Data) != 1 {
		t.Fatalf("expected 1 run, got count=%d rows=%d", payload.Info.TotalCount, len(payload.Data))
	}
	if payload.Data[0].Id != "run-1" {
		t.Fatalf("unexpected run id: %q", payload.Data[0].Id)
	}
}

func TestHandler_ListRunsRequireScheduleIDReturnsEmpty(t *testing.T) {
	store, _ := newTestStore(t)
	svc := New(store, nil)
	h := NewHandler(svc)

	req := httptest.NewRequest(http.MethodGet, "/v1/api/agently/scheduler/run?requireScheduleId=true", nil)
	rec := httptest.NewRecorder()

	h.handleListRuns()(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Data []*schrun.RunView `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	if len(payload.Data) != 0 {
		t.Fatalf("expected empty runs, got %d", len(payload.Data))
	}
}

func TestHandler_ListRunsWithoutScheduleIDReturnsAllVisibleRuns(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, nil)
	h := NewHandler(svc)

	insertScheduleRow(t, db, "sched-1", "Nightly")
	insertScheduleRow(t, db, "sched-2", "Hourly")
	insertConversationRow(t, db, "conv-1", "public")
	insertConversationRow(t, db, "conv-2", "public")
	insertSchedulerRunRow(t, db, "run-1", "sched-1", "conv-1", "succeeded", time.Date(2026, 3, 23, 10, 0, 0, 0, time.UTC))
	insertSchedulerRunRow(t, db, "run-2", "sched-2", "conv-2", "running", time.Date(2026, 3, 23, 11, 0, 0, 0, time.UTC))

	req := httptest.NewRequest(http.MethodGet, "/v1/api/agently/scheduler/run", nil)
	rec := httptest.NewRecorder()

	h.handleListRuns()(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Status string            `json:"status"`
		Data   []*schrun.RunView `json:"data"`
		Info   struct {
			PageCount  int `json:"pageCount"`
			TotalCount int `json:"totalCount"`
		} `json:"info"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	if payload.Status != "ok" {
		t.Fatalf("expected status ok, got %q", payload.Status)
	}
	if payload.Info.TotalCount != 2 || len(payload.Data) != 2 {
		t.Fatalf("expected 2 runs, got count=%d rows=%d", payload.Info.TotalCount, len(payload.Data))
	}
	if payload.Data[0].Id != "run-2" || payload.Data[1].Id != "run-1" {
		t.Fatalf("unexpected run order: %#v", payload.Data)
	}
}

func TestHandler_ListRunsIncludesPrivateRunsForOwner(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, nil)
	h := NewHandler(svc)

	insertScheduleRowWithOwner(t, db, "sched-1", "Nightly", "private", "devuser")
	insertConversationRow(t, db, "conv-1", "public")
	insertSchedulerRunRow(t, db, "run-1", "sched-1", "conv-1", "succeeded", time.Date(2026, 3, 23, 10, 0, 0, 0, time.UTC))

	req := httptest.NewRequest(http.MethodGet, "/v1/api/agently/scheduler/run", nil)
	req = req.WithContext(svcauth.InjectUser(req.Context(), "devuser"))
	rec := httptest.NewRecorder()

	h.handleListRuns()(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Status string            `json:"status"`
		Data   []*schrun.RunView `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	if payload.Status != "ok" {
		t.Fatalf("expected status ok, got %q", payload.Status)
	}
	if len(payload.Data) != 1 || payload.Data[0].Id != "run-1" {
		t.Fatalf("expected owner to see private run, got %#v", payload.Data)
	}
}

func TestHandler_ListRunsExcludesPrivateRunsFromOtherUsers(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, nil)
	h := NewHandler(svc)

	insertScheduleRowWithOwner(t, db, "sched-1", "Nightly", "private", "devuser")
	insertConversationRow(t, db, "conv-1", "public")
	insertSchedulerRunRow(t, db, "run-1", "sched-1", "conv-1", "succeeded", time.Date(2026, 3, 23, 10, 0, 0, 0, time.UTC))

	req := httptest.NewRequest(http.MethodGet, "/v1/api/agently/scheduler/run", nil)
	req = req.WithContext(svcauth.InjectUser(req.Context(), "ppoudyal"))
	rec := httptest.NewRecorder()

	h.handleListRuns()(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Status string            `json:"status"`
		Data   []*schrun.RunView `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	if payload.Status != "ok" {
		t.Fatalf("expected status ok, got %q", payload.Status)
	}
	if len(payload.Data) != 0 {
		t.Fatalf("expected other users to be denied private runs, got %#v", payload.Data)
	}
}

func TestHandler_ListRunsByPrivateScheduleReturnsEmptyForOtherUsers(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, nil)
	h := NewHandler(svc)

	insertScheduleRowWithOwner(t, db, "sched-1", "Nightly", "private", "devuser")
	insertConversationRow(t, db, "conv-1", "public")
	insertSchedulerRunRow(t, db, "run-1", "sched-1", "conv-1", "succeeded", time.Date(2026, 3, 23, 10, 0, 0, 0, time.UTC))

	req := httptest.NewRequest(http.MethodGet, "/v1/api/agently/scheduler/run/sched-1", nil)
	req = req.WithContext(svcauth.InjectUser(req.Context(), "ppoudyal"))
	req.SetPathValue("id", "sched-1")
	rec := httptest.NewRecorder()

	h.handleListRuns()(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Status string            `json:"status"`
		Data   []*schrun.RunView `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	if payload.Status != "ok" {
		t.Fatalf("expected status ok, got %q", payload.Status)
	}
	if len(payload.Data) != 0 {
		t.Fatalf("expected schedule-scoped private runs to be denied, got %#v", payload.Data)
	}
}

func TestHandler_ListRunsByPrivateScheduleIncludesOwner(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, nil)
	h := NewHandler(svc)

	insertScheduleRowWithOwner(t, db, "sched-1", "Nightly", "private", "devuser")
	insertConversationRow(t, db, "conv-1", "public")
	insertSchedulerRunRow(t, db, "run-1", "sched-1", "conv-1", "succeeded", time.Date(2026, 3, 23, 10, 0, 0, 0, time.UTC))

	req := httptest.NewRequest(http.MethodGet, "/v1/api/agently/scheduler/run/sched-1", nil)
	req = req.WithContext(svcauth.InjectUser(req.Context(), "devuser"))
	req.SetPathValue("id", "sched-1")
	rec := httptest.NewRecorder()

	h.handleListRuns()(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Status string            `json:"status"`
		Data   []*schrun.RunView `json:"data"`
		Info   struct {
			PageCount  int `json:"pageCount"`
			TotalCount int `json:"totalCount"`
		} `json:"info"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	if payload.Status != "ok" {
		t.Fatalf("expected status ok, got %q", payload.Status)
	}
	if payload.Info.PageCount != 1 || payload.Info.TotalCount != 1 {
		t.Fatalf("unexpected paging info: %+v", payload.Info)
	}
	if len(payload.Data) != 1 || payload.Data[0].Id != "run-1" {
		t.Fatalf("expected owner to see private schedule run, got %#v", payload.Data)
	}
}

func TestHandler_ListRunsIgnoresInteractiveRunsWithoutScheduleID(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, nil)
	h := NewHandler(svc)

	insertScheduleRow(t, db, "sched-1", "Nightly")
	insertConversationRow(t, db, "conv-scheduled", "public")
	insertConversationRow(t, db, "conv-interactive", "public")
	insertSchedulerRunRow(t, db, "run-scheduled", "sched-1", "conv-scheduled", "succeeded", time.Date(2026, 3, 23, 10, 0, 0, 0, time.UTC))
	insertInteractiveRunRow(t, db, "run-interactive", "conv-interactive", "running", time.Date(2026, 3, 23, 11, 0, 0, 0, time.UTC))

	req := httptest.NewRequest(http.MethodGet, "/v1/api/agently/scheduler/run", nil)
	rec := httptest.NewRecorder()

	h.handleListRuns()(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Status string            `json:"status"`
		Data   []*schrun.RunView `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	if payload.Status != "ok" {
		t.Fatalf("expected status ok, got %q", payload.Status)
	}
	if len(payload.Data) != 1 || payload.Data[0].Id != "run-scheduled" {
		t.Fatalf("expected only scheduled run, got %#v", payload.Data)
	}
}

func TestHandler_ListRunsIncludesScheduledRunsWhileKindIsStillInteractive(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, nil)
	h := NewHandler(svc)

	insertScheduleRow(t, db, "sched-1", "Nightly")
	insertConversationRow(t, db, "conv-scheduled", "public")
	insertConversationRow(t, db, "conv-interactive", "public")
	startedAt := time.Date(2026, 3, 23, 11, 0, 0, 0, time.UTC)
	if _, err := db.ExecContext(context.Background(), `INSERT INTO run (id, schedule_id, conversation_id, conversation_kind, status, created_at, started_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, "run-scheduled-live", "sched-1", "conv-scheduled", "interactive", "running", startedAt.Add(-1*time.Minute), startedAt); err != nil {
		t.Fatalf("insert run error: %v", err)
	}
	insertInteractiveRunRow(t, db, "run-interactive", "conv-interactive", "running", startedAt.Add(2*time.Minute))

	req := httptest.NewRequest(http.MethodGet, "/v1/api/agently/scheduler/run", nil)
	rec := httptest.NewRecorder()

	h.handleListRuns()(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Status string            `json:"status"`
		Data   []*schrun.RunView `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	if payload.Status != "ok" {
		t.Fatalf("expected status ok, got %q", payload.Status)
	}
	if len(payload.Data) != 1 || payload.Data[0].Id != "run-scheduled-live" {
		t.Fatalf("expected only in-flight scheduled run, got %#v", payload.Data)
	}
}

func TestHandler_ListRunsSupportsTrailingSlashAndPagination(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, nil)
	h := NewHandler(svc)

	insertScheduleRow(t, db, "sched-1", "Nightly")
	insertConversationRow(t, db, "conv-1", "public")
	insertConversationRow(t, db, "conv-2", "public")
	insertConversationRow(t, db, "conv-3", "public")
	insertSchedulerRunRow(t, db, "run-1", "sched-1", "conv-1", "succeeded", time.Date(2026, 3, 23, 10, 0, 0, 0, time.UTC))
	insertSchedulerRunRow(t, db, "run-2", "sched-1", "conv-2", "failed", time.Date(2026, 3, 23, 11, 0, 0, 0, time.UTC))
	insertSchedulerRunRow(t, db, "run-3", "sched-1", "conv-3", "running", time.Date(2026, 3, 23, 12, 0, 0, 0, time.UTC))

	req := httptest.NewRequest(http.MethodGet, "/v1/api/agently/scheduler/run/?page=2&size=2", nil)
	rec := httptest.NewRecorder()

	h.handleListRuns()(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Data []*schrun.RunView `json:"data"`
		Info struct {
			PageCount  int `json:"pageCount"`
			TotalCount int `json:"totalCount"`
		} `json:"info"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	if payload.Info.PageCount != 2 || payload.Info.TotalCount != 3 {
		t.Fatalf("unexpected paging info: %+v", payload.Info)
	}
	if len(payload.Data) != 1 || payload.Data[0].Id != "run-1" {
		t.Fatalf("unexpected page slice: %#v", payload.Data)
	}
}

func TestHandler_ListRunsSupportsPathScheduleID(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, nil)
	h := NewHandler(svc)

	insertScheduleRow(t, db, "sched-1", "Nightly")
	insertScheduleRow(t, db, "sched-2", "Hourly")
	insertConversationRow(t, db, "conv-1", "public")
	insertConversationRow(t, db, "conv-2", "public")
	insertSchedulerRunRow(t, db, "run-1", "sched-1", "conv-1", "succeeded", time.Date(2026, 3, 23, 10, 0, 0, 0, time.UTC))
	insertSchedulerRunRow(t, db, "run-2", "sched-2", "conv-2", "running", time.Date(2026, 3, 23, 11, 0, 0, 0, time.UTC))

	req := httptest.NewRequest(http.MethodGet, "/v1/api/agently/scheduler/run/sched-1", nil)
	req.SetPathValue("id", "sched-1")
	rec := httptest.NewRecorder()

	h.handleListRuns()(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Data []*schrun.RunView `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	if len(payload.Data) != 1 || payload.Data[0].Id != "run-1" {
		t.Fatalf("expected schedule-scoped run list, got %#v", payload.Data)
	}
}

func TestHandler_ListRunsFiltersByStatusConversationAndError(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, nil)
	h := NewHandler(svc)

	insertScheduleRow(t, db, "sched-1", "Nightly")
	insertConversationRow(t, db, "conv-alpha-001", "public")
	insertConversationRow(t, db, "conv-alpha-002", "public")
	insertConversationRow(t, db, "conv-beta-001", "public")
	insertConversationRow(t, db, "conv-alpha-003", "public")
	startedAt := time.Date(2026, 3, 23, 10, 0, 0, 0, time.UTC)
	insertSchedulerRunRowWithError(t, db, "run-match", "sched-1", "conv-alpha-001", "succeeded", "worker died, resumed as abc", startedAt)
	insertSchedulerRunRowWithError(t, db, "run-status-miss", "sched-1", "conv-alpha-002", "failed", "worker died, resumed as def", startedAt.Add(time.Hour))
	insertSchedulerRunRowWithError(t, db, "run-conversation-miss", "sched-1", "conv-beta-001", "succeeded", "worker died, resumed as ghi", startedAt.Add(2*time.Hour))
	insertSchedulerRunRowWithError(t, db, "run-error-miss", "sched-1", "conv-alpha-003", "succeeded", "network timeout", startedAt.Add(3*time.Hour))

	req := httptest.NewRequest(http.MethodGet, "/v1/api/agently/scheduler/run?status=SUCC&conversationId=ALPHA&errorMessage=RESUMED&page=1&size=1", nil)
	rec := httptest.NewRecorder()

	h.handleListRuns()(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}
	payload := decodeRunListPayload(t, rec)
	if payload.Status != "ok" {
		t.Fatalf("expected status ok, got %q", payload.Status)
	}
	if payload.Info.PageCount != 1 || payload.Info.TotalCount != 1 {
		t.Fatalf("unexpected paging info: %+v", payload.Info)
	}
	if len(payload.Data) != 1 || payload.Data[0].Id != "run-match" {
		t.Fatalf("expected matching run only, got %#v", payload.Data)
	}
}

func TestHandler_ListRunsFiltersKeepScheduleScopeAndIgnoreBlankValues(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, nil)
	h := NewHandler(svc)

	insertScheduleRow(t, db, "sched-1", "Nightly")
	insertScheduleRow(t, db, "sched-2", "Hourly")
	insertConversationRow(t, db, "conv-alpha-001", "public")
	insertConversationRow(t, db, "conv-beta-001", "public")
	startedAt := time.Date(2026, 3, 23, 10, 0, 0, 0, time.UTC)
	insertSchedulerRunRowWithError(t, db, "run-sched-1-alpha", "sched-1", "conv-alpha-001", "succeeded", "worker died, resumed as abc", startedAt)
	insertSchedulerRunRowWithError(t, db, "run-sched-2-alpha", "sched-2", "conv-alpha-001", "succeeded", "worker died, resumed as def", startedAt.Add(time.Hour))
	insertSchedulerRunRowWithError(t, db, "run-sched-1-beta", "sched-1", "conv-beta-001", "failed", "network timeout", startedAt.Add(2*time.Hour))

	req := httptest.NewRequest(http.MethodGet, "/v1/api/agently/scheduler/run/sched-1?status=%20&conversationId=alpha&errorMessage=%20&page=1&size=10", nil)
	req.SetPathValue("id", "sched-1")
	rec := httptest.NewRecorder()

	h.handleListRuns()(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d body=%s", rec.Code, rec.Body.String())
	}
	payload := decodeRunListPayload(t, rec)
	if payload.Status != "ok" {
		t.Fatalf("expected status ok, got %q", payload.Status)
	}
	if payload.Info.PageCount != 1 || payload.Info.TotalCount != 1 {
		t.Fatalf("unexpected paging info: %+v", payload.Info)
	}
	if len(payload.Data) != 1 || payload.Data[0].Id != "run-sched-1-alpha" {
		t.Fatalf("expected schedule-scoped filtered run, got %#v", payload.Data)
	}
}

func decodeRunListPayload(t *testing.T, rec *httptest.ResponseRecorder) struct {
	Status string            `json:"status"`
	Data   []*schrun.RunView `json:"data"`
	Info   struct {
		PageCount  int `json:"pageCount"`
		TotalCount int `json:"totalCount"`
	} `json:"info"`
} {
	t.Helper()
	var payload struct {
		Status string            `json:"status"`
		Data   []*schrun.RunView `json:"data"`
		Info   struct {
			PageCount  int `json:"pageCount"`
			TotalCount int `json:"totalCount"`
		} `json:"info"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	return payload
}

func insertScheduleRow(t *testing.T, db *sql.DB, id, name string) {
	t.Helper()
	insertScheduleRowWithOwner(t, db, id, name, "public", "")
}

func insertScheduleRowWithOwner(t *testing.T, db *sql.DB, id, name, visibility, owner string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), `INSERT INTO schedule (id, name, visibility, created_by_user_id, agent_ref, enabled, schedule_type, timezone) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, name, visibility, nullableString(owner), "simple", 1, "adhoc", "UTC"); err != nil {
		t.Fatalf("insert schedule error: %v", err)
	}
}

func insertConversationRow(t *testing.T, db *sql.DB, id, visibility string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), `INSERT INTO conversation (id, created_at, visibility) VALUES (?, ?, ?)`, id, time.Now().UTC(), visibility); err != nil {
		t.Fatalf("insert conversation error: %v", err)
	}
}

func insertConversationRowWithOwner(t *testing.T, db *sql.DB, id, visibility, owner string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), `INSERT INTO conversation (id, created_at, visibility, created_by_user_id) VALUES (?, ?, ?, ?)`, id, time.Now().UTC(), visibility, owner); err != nil {
		t.Fatalf("insert conversation error: %v", err)
	}
}

func insertSchedulerRunRow(t *testing.T, db *sql.DB, id, scheduleID, conversationID, status string, startedAt time.Time) {
	t.Helper()
	insertSchedulerRunRowWithError(t, db, id, scheduleID, conversationID, status, "", startedAt)
}

func insertSchedulerRunRowWithError(t *testing.T, db *sql.DB, id, scheduleID, conversationID, status, errorMessage string, startedAt time.Time) {
	t.Helper()
	createdAt := startedAt.Add(-1 * time.Minute)
	completedAt := startedAt.Add(30 * time.Second)
	if _, err := db.ExecContext(context.Background(), `INSERT INTO run (id, schedule_id, conversation_id, conversation_kind, status, error_message, created_at, started_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, scheduleID, conversationID, "scheduled", status, nullableString(errorMessage), createdAt, startedAt, completedAt); err != nil {
		t.Fatalf("insert run error: %v", err)
	}
}

func insertInteractiveRunRow(t *testing.T, db *sql.DB, id, conversationID, status string, startedAt time.Time) {
	t.Helper()
	createdAt := startedAt.Add(-1 * time.Minute)
	if _, err := db.ExecContext(context.Background(), `INSERT INTO run (id, conversation_id, conversation_kind, status, created_at, started_at) VALUES (?, ?, ?, ?, ?, ?)`, id, conversationID, "interactive", status, createdAt, startedAt); err != nil {
		t.Fatalf("insert run error: %v", err)
	}
}

func nullableString(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}
