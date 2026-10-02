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

func TestMessageDeletionLegacyParity(t *testing.T) {
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
			oldDB, oldPath := messageDeleteFixture(t, project)
			db, _ := messageDeleteFixture(t, project)
			if tc.input.reject {
				for _, fixture := range []*sql.DB{oldDB, db} {
					_, err := fixture.Exec(`CREATE TRIGGER reject_message_delete BEFORE DELETE ON message WHEN OLD.id='second' BEGIN SELECT RAISE(ABORT,'fixture delete rejection'); END`)
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
			payload, err := json.Marshal(map[string]any{"Component": "message", "Method": "DELETE", "DBPath": oldPath, "Body": string(body)})
			must(t, err)
			process := exec.Command(legacy)
			process.Stdin = bytes.NewReader(payload)
			raw, err := process.Output()
			must(t, err)
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			rt := messageWriterRuntime(t, db)
			body, err = json.Marshal(map[string]any{"data": nativeRows})
			must(t, err)
			request := httptest.NewRequest("PATCH", "/v1/api/agently/message", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			_, err = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/message", scope)
			if before.Failed != tc.expect.failed || (err != nil) != tc.expect.failed {
				t.Fatalf("failure legacy=%v native=%v expected=%v", before.Failed, err, tc.expect.failed)
			}
			oldRows, newRows := messageStoredRows(t, oldDB), messageStoredRows(t, db)
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
			for _, fixture := range []*sql.DB{oldDB, db} {
				var messages, turns, payloads int
				must(t, fixture.QueryRow("SELECT COUNT(*) FROM model_call").Scan(&messages))
				must(t, fixture.QueryRow("SELECT COUNT(*) FROM turn").Scan(&turns))
				must(t, fixture.QueryRow("SELECT COUNT(*) FROM call_payload").Scan(&payloads))
				if messages != 0 || turns != 2 || payloads != 0 {
					t.Fatal("message deletion changed a parent table")
				}
			}

		})
	}
}

func messageDeleteFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := messageFixture(t, project)
	_, err := db.Exec(`INSERT INTO message(id,conversation_id,role,type) VALUES('second','c1','assistant','text')`)
	must(t, err)
	return db, path
}

func TestMessageDeleteCascadeLegacyParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type useCase struct {
		desc   string
		input  bool
		expect int
	}
	for _, tc := range []useCase{{"delete cascades fact rows and queue", false, 0}, {"late failure restores cascaded rows", true, 1}} {
		t.Run(tc.desc, func(t *testing.T) {
			oldDB, path := messageDeleteFixture(t, project)
			db, _ := messageDeleteFixture(t, project)
			for _, fixture := range []*sql.DB{oldDB, db} {
				_, err := fixture.Exec(`INSERT INTO model_call(message_id,provider,model,model_kind,status) VALUES('existing','p','m','chat','thinking');
   INSERT INTO tool_call(message_id,op_id,tool_name,tool_kind,status) VALUES('existing','op','tool','function','running');
   INSERT INTO turn_queue(id,conversation_id,turn_id,message_id,queue_seq) VALUES('q1','c1','t1','existing',1);`)
				must(t, err)
				if tc.input {
					_, err = fixture.Exec(`CREATE TRIGGER reject_message_cascade BEFORE DELETE ON message WHEN OLD.id='second' BEGIN SELECT RAISE(ABORT,'fixture rejection'); END`)
					must(t, err)
				}
			}
			body := `{"data":[{"id":"existing"},{"id":"second"}]}`
			payload, err := json.Marshal(map[string]any{"Component": "message", "Method": "DELETE", "DBPath": path, "Body": body})
			must(t, err)
			process := exec.Command(legacy)
			process.Stdin = bytes.NewReader(payload)
			raw, err := process.Output()
			must(t, err)
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			if before.Failed != tc.input {
				t.Fatalf("legacy failure=%v expected=%v", before.Failed, tc.input)
			}
			body = `{"data":[{"id":"existing","shouldDelete":true},{"id":"second","shouldDelete":true}]}`
			request := httptest.NewRequest("PATCH", "/v1/api/agently/message", bytes.NewBufferString(body))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			_, err = messageWriterRuntime(t, db).ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/message", scope)
			if (err != nil) != tc.input {
				t.Fatalf("native failure=%v expected=%v", err, tc.input)
			}
			for _, fixture := range []*sql.DB{oldDB, db} {
				for _, query := range []string{"SELECT COUNT(*) FROM model_call", "SELECT COUNT(*) FROM tool_call", "SELECT COUNT(*) FROM turn_queue"} {
					var count int
					must(t, fixture.QueryRow(query).Scan(&count))
					if count != tc.expect {
						t.Fatalf("%s count=%d expected=%d", query, count, tc.expect)
					}
				}
			}
		})
	}
}
