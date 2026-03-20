package scheduler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/viant/agently-core/internal/testutil/dbtest"
	schedwrite "github.com/viant/agently-core/pkg/agently/scheduler/schedule/write"
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
	var count int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(1) FROM schedule WHERE id = ?`, id).Scan(&count); err != nil {
		t.Fatalf("QueryRowContext() error: %v", err)
	}
	if count != expected {
		t.Fatalf("expected schedule row count %d for %s, got %d", expected, id, count)
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
