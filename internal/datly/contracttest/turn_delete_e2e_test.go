package tests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	requestprovider "github.com/viant/bindly/provider/request"
)

func TestTurnDeletionLegacyParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type input struct {
		ids    []string
		reject bool
	}
	type expect struct {
		failed bool
		ids    []string
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"empty delete", input{ids: []string{}}, expect{ids: []string{"existing", "second"}}},
		{"blank identity is ignored", input{ids: []string{""}}, expect{ids: []string{"existing", "second"}}},
		{"missing identity is idempotent", input{ids: []string{"absent"}}, expect{ids: []string{"existing", "second"}}},
		{"known identity deletes", input{ids: []string{"existing"}}, expect{ids: []string{"second"}}},
		{"repeated identity is idempotent", input{ids: []string{"existing", "existing"}}, expect{ids: []string{"second"}}},
		{"known missing and blank share one batch", input{ids: []string{"existing", "absent", ""}}, expect{ids: []string{"second"}}},
		{"late failure restores earlier deleted row", input{ids: []string{"existing", "second"}, reject: true}, expect{failed: true, ids: []string{"existing", "second"}}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			oldDB, oldPath := turnDeleteFixture(t, project)
			db, _ := turnDeleteFixture(t, project)
			if tc.input.reject {
				for _, fixture := range []*sql.DB{oldDB, db} {
					_, err := fixture.Exec(`CREATE TRIGGER reject_turn_delete BEFORE DELETE ON turn WHEN OLD.id='second' BEGIN SELECT RAISE(ABORT,'fixture delete rejection'); END`)
					must(t, err)
				}
			}
			legacyRows := []map[string]any{}
			nativeRows := []map[string]any{}
			for _, id := range tc.input.ids {
				legacyRows = append(legacyRows, map[string]any{"id": id})
				nativeRows = append(nativeRows, map[string]any{"id": id, "shouldDelete": true})
			}
			body, err := json.Marshal(map[string]any{"data": legacyRows})
			must(t, err)
			payload, err := json.Marshal(map[string]any{"Component": "turn", "Method": "DELETE", "DBPath": oldPath, "Body": string(body), "Filters": map[string]string{"conversationId": "c1"}})
			must(t, err)
			process := exec.Command(legacy)
			process.Stdin = bytes.NewReader(payload)
			raw, err := process.Output()
			must(t, err)
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			rt, _ := turnWriterRuntime(t, db)
			body, err = json.Marshal(map[string]any{"data": nativeRows})
			must(t, err)
			request := httptest.NewRequest("PATCH", "/v1/api/agently/turn", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			_, err = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/turn", scope)
			if before.Failed != tc.expect.failed || (err != nil) != tc.expect.failed {
				t.Fatalf("failure legacy=%v native=%v expected=%v", before.Failed, err, tc.expect.failed)
			}
			oldRows, newRows := turnStoredRows(t, oldDB), turnStoredRows(t, db)
			if !reflect.DeepEqual(oldRows, newRows) {
				t.Fatalf("stored rows legacy=%s native=%s", pretty(oldRows), pretty(newRows))
			}
			ids := []string{}
			for _, row := range newRows {
				ids = append(ids, row["id"].(string))
			}
			if !reflect.DeepEqual(ids, tc.expect.ids) {
				t.Fatalf("ids=%v expected=%v", ids, tc.expect.ids)
			}
		})
	}
}

func turnDeleteFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := turnWriterFixture(t, project)
	_, err := db.Exec(`INSERT INTO turn(id,conversation_id,status,created_at) VALUES ('second','c1','queued','2026-01-01 00:00:00')`)
	must(t, err)
	return db, path
}

func TestTurnMixedMutationTransaction(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct{ caller, commit, reject bool }
	type expect struct {
		failed   bool
		replaced bool
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"owned mixed mutation commits", input{}, expect{replaced: true}},
		{"late insert failure rolls back deletion and update", input{reject: true}, expect{failed: true}},
		{"caller rolls back mixed mutation", input{caller: true}, expect{}},
		{"caller commits mixed mutation", input{caller: true, commit: true}, expect{replaced: true}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := turnDeleteFixture(t, project)
			if tc.input.reject {
				_, err := db.Exec(`CREATE TRIGGER reject_turn_insert BEFORE INSERT ON turn WHEN NEW.id='new' BEGIN SELECT RAISE(ABORT,'fixture insert rejection'); END`)
				must(t, err)
			}
			var tx *sql.Tx
			if tc.input.caller {
				db.SetMaxOpenConns(1)
				var err error
				tx, err = db.BeginTx(context.Background(), nil)
				must(t, err)
				defer tx.Rollback()
			}
			rt, _ := turnWriterRuntime(t, db, tx)
			request := httptest.NewRequest("PATCH", "/v1/api/agently/turn", bytes.NewBufferString(`{"data":[{"id":"existing","shouldDelete":true},{"id":"second","status":"running"},{"id":"new","conversationId":"c1","status":"queued"}]}`))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			_, err = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/turn", scope)
			if (err != nil) != tc.expect.failed {
				t.Fatalf("failure=%v expected=%v", err, tc.expect.failed)
			}
			if tx != nil {
				var count int
				must(t, tx.QueryRow("SELECT COUNT(*) FROM turn WHERE id IN ('new','second')").Scan(&count))
				if count != 2 {
					t.Fatal("caller transaction did not retain mixed mutation")
				}
				if tc.input.commit {
					must(t, tx.Commit())
				} else {
					must(t, tx.Rollback())
				}
			}
			rows := turnStoredRows(t, db)
			ids := []string{}
			for _, row := range rows {
				ids = append(ids, row["id"].(string))
			}
			expectedIDs := []string{"existing", "second"}
			expectedStatus := "queued"
			if tc.expect.replaced {
				expectedIDs = []string{"new", "second"}
				expectedStatus = "running"
			}
			if !reflect.DeepEqual(ids, expectedIDs) {
				t.Fatalf("ids=%v expected=%v", ids, expectedIDs)
			}
			var actual string
			must(t, db.QueryRow("SELECT status FROM turn WHERE id='second'").Scan(&actual))
			if actual != expectedStatus {
				t.Fatalf("status=%s expected=%s", actual, expectedStatus)
			}
		})
	}
}
