package tests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	schedule "github.com/viant/agently-core/internal/datly/schedule/read"
	write "github.com/viant/agently-core/internal/datly/schedule/write"
	requestprovider "github.com/viant/bindly/provider/request"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestScheduleDeletionLegacyV1Parity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type input struct {
		ids    []string
		reject bool
	}
	type expect struct {
		failed    bool
		ids, runs []string
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	all := []string{"internal", "other", "owned", "public"}
	allRuns := []string{"run-other", "run-owned"}
	for _, tc := range []useCase{
		{"empty deletion request", input{ids: []string{}}, expect{ids: all, runs: allRuns}},
		{"blank identity is ignored by legacy contract", input{ids: []string{""}}, expect{ids: all, runs: allRuns}},
		{"unknown identity remains idempotent", input{ids: []string{"missing"}}, expect{ids: all, runs: allRuns}},
		{"known schedule cascades its runs", input{ids: []string{"owned"}}, expect{ids: []string{"internal", "other", "public"}, runs: []string{"run-other"}}},
		{"repeated identity is idempotent", input{ids: []string{"owned", "owned"}}, expect{ids: []string{"internal", "other", "public"}, runs: []string{"run-other"}}},
		{"known missing and blank identities share one writer", input{ids: []string{"owned", "missing", ""}}, expect{ids: []string{"internal", "other", "public"}, runs: []string{"run-other"}}},
		{"late rejection restores parent and cascading runs", input{ids: []string{"owned", "missing", "other"}, reject: true}, expect{failed: true, ids: all, runs: allRuns}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			oldDB, oldPath := scheduleDeleteFixture(t, project)
			db, _ := scheduleDeleteFixture(t, project)
			if tc.input.reject {
				for _, fixture := range []*sql.DB{oldDB, db} {
					_, err := fixture.Exec(`CREATE TRIGGER reject_schedule_delete BEFORE DELETE ON schedule WHEN OLD.id='other' BEGIN SELECT RAISE(ABORT,'fixture delete rejection'); END;`)
					must(t, err)
				}
			}
			encoded, err := json.Marshal(map[string]any{"Component": "scheduleDelete", "DBPath": oldPath, "Filters": map[string]any{"mode": "due", "ids": tc.input.ids}})
			must(t, err)
			process := exec.Command(legacy)
			process.Stdin = bytes.NewReader(encoded)
			var stderr bytes.Buffer
			process.Stderr = &stderr
			raw, err := process.Output()
			if err != nil {
				t.Fatalf("legacy: %v\n%s", err, stderr.String())
			}
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			rt, key := scheduleWriterRuntime(t, db, "")
			rows := []map[string]any{}
			for _, id := range tc.input.ids {
				rows = append(rows, map[string]any{"id": id, "shouldDelete": true})
			}
			body, err := json.Marshal(map[string]any{"data": rows})
			must(t, err)
			req := httptest.NewRequest("PATCH", "/v1/api/agently/scheduler/", strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(req)
			must(t, err)
			defer scope.Close()
			result, mutationErr := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/scheduler/", scope)
			if before.Failed != tc.expect.failed || (mutationErr != nil) != tc.expect.failed {
				t.Fatalf("legacy failed=%v (%s), new=%v, expected=%v", before.Failed, before.Error, mutationErr, tc.expect.failed)
			}
			if !tc.expect.failed {
				var oldAck []string
				must(t, json.Unmarshal(before.Output, &oldAck))
				if oldAck == nil {
					oldAck = []string{}
				}
				newAck := []string{}
				for _, row := range result.(*write.Output).Data {
					if !row.ShouldDelete {
						t.Fatal("deleted request marker was lost")
					}
					newAck = append(newAck, row.Id)
				}
				if !reflect.DeepEqual(oldAck, newAck) {
					t.Fatalf("acknowledgments legacy=%v new=%v", oldAck, newAck)
				}
			}
			result, err = rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/scheduler/schedule/{id}"}}, Input: &schedule.ScheduleInput{}})
			must(t, err)
			raw, err = json.Marshal(result.(*schedule.ScheduleOutput).Data)
			must(t, err)
			var after []json.RawMessage
			must(t, json.Unmarshal(raw, &after))
			oldRows, newRows := normalizeRows(t, before.Rows), normalizeRows(t, after)
			if !reflect.DeepEqual(oldRows, newRows) {
				t.Fatalf("stored parity\nlegacy=%s\nnew=%s", pretty(oldRows), pretty(newRows))
			}
			ids := []string{}
			for _, row := range newRows {
				ids = append(ids, row["id"].(string))
			}
			if !reflect.DeepEqual(ids, tc.expect.ids) {
				t.Fatalf("identities=%v expected=%v", ids, tc.expect.ids)
			}
			for _, fixture := range []*sql.DB{oldDB, db} {
				rows, err := fixture.Query("SELECT id FROM run ORDER BY id")
				must(t, err)
				ids := []string{}
				for rows.Next() {
					var id string
					must(t, rows.Scan(&id))
					ids = append(ids, id)
				}
				must(t, rows.Err())
				must(t, rows.Close())
				if !reflect.DeepEqual(ids, tc.expect.runs) {
					t.Fatalf("cascade rows=%v expected=%v", ids, tc.expect.runs)
				}
			}
		})
	}
}

func scheduleDeleteFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := scheduleReaderFixture(t, project)
	_, err := db.Exec(`INSERT INTO run(id,schedule_id,created_at) VALUES('run-owned','owned','2026-01-01 00:00:00'),('run-other','other','2026-01-01 00:00:00')`)
	must(t, err)
	return db, path
}

func TestScheduleDeletionCallerTransactionOwnership(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type useCase struct {
		desc   string
		input  bool
		expect int
	}
	for _, tc := range []useCase{{"caller rollback restores cascading run", false, 1}, {"caller commit publishes cascading delete", true, 0}} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := scheduleDeleteFixture(t, project)
			db.SetMaxOpenConns(1)
			tx, err := db.BeginTx(context.Background(), nil)
			must(t, err)
			defer tx.Rollback()
			rt, _ := scheduleWriterRuntime(t, db, "", tx)
			req := httptest.NewRequest("PATCH", "/v1/api/agently/scheduler/", strings.NewReader(`{"data":[{"id":"owned","shouldDelete":true}]}`))
			req.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(req)
			must(t, err)
			defer scope.Close()
			_, err = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/scheduler/", scope)
			must(t, err)
			var pending int
			must(t, tx.QueryRow("SELECT COUNT(*) FROM run WHERE id='run-owned'").Scan(&pending))
			if pending != 0 {
				t.Fatalf("pending cascade count=%d", pending)
			}
			if tc.input {
				must(t, tx.Commit())
			} else {
				must(t, tx.Rollback())
			}
			for _, query := range []string{"SELECT COUNT(*) FROM schedule WHERE id='owned'", "SELECT COUNT(*) FROM run WHERE id='run-owned'"} {
				var stored int
				must(t, db.QueryRow(query).Scan(&stored))
				if stored != tc.expect {
					t.Fatalf("count=%d expected=%d", stored, tc.expect)
				}
			}
		})
	}
}
