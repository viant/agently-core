package tests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	requestprovider "github.com/viant/bindly/provider/request"
)

func TestModelCallDeletionLegacyParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
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
			db, _ := modelCallDeleteFixture(t, project)
			if tc.input.reject {
				for _, fixture := range []*sql.DB{db} {
					_, err := fixture.Exec(`CREATE TRIGGER reject_modelcall_delete BEFORE DELETE ON model_call WHEN OLD.message_id='second' BEGIN SELECT RAISE(ABORT,'fixture delete rejection'); END`)
					must(t, err)
				}
			}
			nativeRows := []map[string]any{}
			for _, id := range tc.input.ids {
				nativeRows = append(nativeRows, map[string]any{"messageId": id, "shouldDelete": true})
			}
			rt := modelCallWriterRuntime(t, db)
			body, err := json.Marshal(map[string]any{"data": nativeRows})
			must(t, err)
			request := httptest.NewRequest("PATCH", "/v1/api/agently/modelcall", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			_, err = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/modelcall", scope)
			if (err != nil) != tc.expect.failed {
				t.Fatalf("failure native=%v expected=%v", err, tc.expect.failed)
			}
			newRows := modelCallStoredRows(t, db)
			ids := []string{}
			for _, row := range newRows {
				ids = append(ids, row["messageid"].(string))
			}
			if !reflect.DeepEqual(ids, tc.expect.ids) {
				t.Fatalf("ids=%v expected=%v", ids, tc.expect.ids)
			}
			for _, fixture := range []*sql.DB{db} {
				var messages, turns, payloads int
				must(t, fixture.QueryRow("SELECT COUNT(*) FROM message").Scan(&messages))
				must(t, fixture.QueryRow("SELECT COUNT(*) FROM turn").Scan(&turns))
				must(t, fixture.QueryRow("SELECT COUNT(*) FROM call_payload").Scan(&payloads))
				if messages != 3 || turns != 1 || payloads != 1 {
					t.Fatal("model call deletion changed a parent table")
				}
			}

		})
	}
}

func modelCallDeleteFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := modelCallFixture(t, project)
	_, err := db.Exec(`INSERT INTO message(id,conversation_id,role) VALUES ('second','c1','assistant'); INSERT INTO model_call(message_id,provider,model,model_kind,status) VALUES ('second','p','m','chat','thinking')`)
	must(t, err)
	return db, path
}

func TestModelCallMixedMutationTransaction(t *testing.T) {
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
			db, _ := modelCallDeleteFixture(t, project)
			if tc.input.reject {
				_, err := db.Exec(`CREATE TRIGGER reject_modelcall_insert BEFORE INSERT ON model_call WHEN NEW.message_id='new' BEGIN SELECT RAISE(ABORT,'fixture insert rejection'); END`)
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
			rt := modelCallWriterRuntime(t, db, tx)
			request := httptest.NewRequest("PATCH", "/v1/api/agently/modelcall", bytes.NewBufferString(`{"data":[{"messageId":"existing","shouldDelete":true},{"messageId":"second","status":"completed"},{"messageId":"new","provider":"p","model":"m","modelKind":"chat","status":"thinking"}]}`))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			_, err = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/modelcall", scope)
			if (err != nil) != tc.expect.failed {
				t.Fatalf("failure=%v expected=%v", err, tc.expect.failed)
			}
			if tx != nil {
				var count int
				must(t, tx.QueryRow("SELECT COUNT(*) FROM model_call WHERE message_id IN ('new','second')").Scan(&count))
				if count != 2 {
					t.Fatal("caller transaction did not retain mixed mutation")
				}
				if tc.input.commit {
					must(t, tx.Commit())
				} else {
					must(t, tx.Rollback())
				}
			}
			rows := modelCallStoredRows(t, db)
			ids := []string{}
			for _, row := range rows {
				ids = append(ids, row["messageid"].(string))
			}
			expectedIDs := []string{"existing", "second"}
			expectedStatus := "thinking"
			if tc.expect.replaced {
				expectedIDs = []string{"new", "second"}
				expectedStatus = "completed"
			}
			if !reflect.DeepEqual(ids, expectedIDs) {
				t.Fatalf("ids=%v expected=%v", ids, expectedIDs)
			}
			var actual string
			must(t, db.QueryRow("SELECT status FROM model_call WHERE message_id='second'").Scan(&actual))
			if actual != expectedStatus {
				t.Fatalf("status=%s expected=%s", actual, expectedStatus)
			}
		})
	}
}
