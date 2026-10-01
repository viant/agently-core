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
	"strings"
	"testing"

	write "github.com/viant/agently-core/internal/datly/modelcall/write"
	"github.com/viant/bindly/locator"
	requestprovider "github.com/viant/bindly/provider/request"
	"github.com/viant/bindly/resource"
	druntime "github.com/viant/datly/runtime"
	writer "github.com/viant/datly/runtime/handler/writer"
	"github.com/viant/datly/runtime/registry"
	dsql "github.com/viant/datly/sql"
	"github.com/viant/datly/sql/dml"
	viewprovider "github.com/viant/datly/sql/reader/provider"
)

func TestModelCallWriterLegacyParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type expect struct{ failed, checkTimestamps bool }
	type useCase struct {
		desc, input string
		expect      expect
	}
	for _, tc := range []useCase{
		{"insert required contract", `{"data":[{"messageId":"new","provider":"openai","model":"gpt","modelKind":"chat","status":"thinking"}]}`, expect{failed: false}},
		{"sparse update keeps provider model usage and payload", `{"data":[{"messageId":"existing","status":"completed"}]}`, expect{failed: false}},
		{"identity-only patch is no-op", `{"data":[{"messageId":"existing"}]}`, expect{failed: false}},
		{"explicit zero usage and cost", `{"data":[{"messageId":"existing","promptTokens":0,"totalTokens":0,"cost":0,"iteration":0}]}`, expect{failed: false}},
		{"explicit empty optional strings", `{"data":[{"messageId":"existing","runId":"","traceId":"","errorMessage":""}]}`, expect{failed: false}},
		{"explicit null clears optional values", `{"data":[{"messageId":"existing","cost":null,"requestPayloadId":null,"startedAt":null,"traceId":null}]}`, expect{failed: false}},
		{"detailed usage fields retain markers", `{"data":[{"messageId":"existing","promptCachedTokens":2,"completionTokens":3,"promptAudioTokens":4,"completionReasoningTokens":5,"completionAudioTokens":6,"completionAcceptedPredictionTokens":7,"completionRejectedPredictionTokens":8,"latencyMs":9}]}`, expect{failed: false}},
		{"legacy timestamp storage remains readable by native driver", `{"data":[{"messageId":"existing","startedAt":"2026-01-02T00:00:00Z","completedAt":"2026-01-02T00:01:00Z"}]}`, expect{checkTimestamps: true}},
		{"payload references round trip", `{"data":[{"messageId":"existing","requestPayloadId":"p1","responsePayloadId":"p1","providerRequestPayloadId":"p1","providerResponsePayloadId":"p1","streamPayloadId":"p1"}]}`, expect{failed: false}},
		{"missing message identity is rejected", `{"data":[{"provider":"p","model":"m","modelKind":"chat","status":"thinking"}]}`, expect{failed: true}},
		{"missing provider is rejected", `{"data":[{"messageId":"new","model":"m","modelKind":"chat","status":"thinking"}]}`, expect{failed: true}},
		{"missing model is rejected", `{"data":[{"messageId":"new","provider":"p","modelKind":"chat","status":"thinking"}]}`, expect{failed: true}},
		{"missing model kind is rejected", `{"data":[{"messageId":"new","provider":"p","model":"m","status":"thinking"}]}`, expect{failed: true}},
		{"missing status is rejected", `{"data":[{"messageId":"new","provider":"p","model":"m","modelKind":"chat"}]}`, expect{failed: true}},
		{"explicit empty required update field is rejected", `{"data":[{"messageId":"existing","status":""}]}`, expect{failed: true}},
		{"unknown message foreign key is rejected", `{"data":[{"messageId":"missing","provider":"p","model":"m","modelKind":"chat","status":"thinking"}]}`, expect{failed: true}},
		{"unknown payload foreign key is rejected", `{"data":[{"messageId":"existing","requestPayloadId":"missing"}]}`, expect{failed: true}},
		{"mixed insert and sparse update", `{"data":[{"messageId":"new","provider":"p","model":"m","modelKind":"chat","status":"thinking"},{"messageId":"existing","status":"completed"}]}`, expect{failed: false}},
		{"empty batch", `{"data":[]}`, expect{failed: false}},
		{"null batch", `{"data":null}`, expect{failed: false}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			oldDB, oldPath := modelCallFixture(t, project)
			db, _ := modelCallFixture(t, project)
			payload, err := json.Marshal(map[string]any{"Component": "modelCall", "DBPath": oldPath, "Body": tc.input})
			must(t, err)
			process := exec.Command(legacy)
			process.Stdin = bytes.NewReader(payload)
			raw, err := process.Output()
			must(t, err)
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			rt := modelCallWriterRuntime(t, db)
			request := httptest.NewRequest("PATCH", "/v1/api/agently/modelcall", strings.NewReader(tc.input))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			value, err := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/modelcall", scope)
			if before.Failed != tc.expect.failed || (err != nil) != tc.expect.failed {
				t.Fatalf("failure legacy=%v (%s) native=%v expected=%v", before.Failed, before.Error, err, tc.expect.failed)
			}
			oldRows, newRows := modelCallStoredRows(t, oldDB), modelCallStoredRows(t, db)
			if tc.expect.checkTimestamps {
				assertModelCallTimestampParity(t, oldRows, newRows)
			}
			if !reflect.DeepEqual(oldRows, newRows) {
				t.Fatalf("complete stored rows legacy=%s native=%s", pretty(oldRows), pretty(newRows))
			}
			if !tc.expect.failed {
				var oldOutput []json.RawMessage
				must(t, json.Unmarshal(before.Output, &oldOutput))
				raw, err := json.Marshal(value.(*write.Output).Data)
				must(t, err)
				var newOutput []json.RawMessage
				must(t, json.Unmarshal(raw, &newOutput))
				oldResponse, newResponse := normalizeRowsInOrder(t, oldOutput), normalizeRowsInOrder(t, newOutput)
				if tc.expect.checkTimestamps {
					assertModelCallTimestampParity(t, oldResponse, newResponse)
				}
				if !reflect.DeepEqual(oldResponse, newResponse) {
					t.Fatalf("response legacy=%s native=%s", before.Output, raw)
				}
			}
		})
	}
}

func modelCallFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := goalFixture(t, project)
	_, err := db.Exec(`INSERT INTO turn(id,conversation_id,status) VALUES ('t1','c1','running');
 INSERT INTO message(id,conversation_id,turn_id,role) VALUES ('existing','c1','t1','assistant'),('new','c1','t1','assistant');
 INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage) VALUES ('p1','request','application/json',2,'inline');
 INSERT INTO model_call(message_id,turn_id,provider,model,model_kind,status,prompt_tokens,total_tokens,cost,error_message,request_payload_id,trace_id,started_at) VALUES ('existing','t1','p','m','chat','thinking',10,15,1.5,'old error','p1','old trace','2026-01-01 00:00:00');`)
	must(t, err)
	return db, path
}
func modelCallWriterRuntime(t *testing.T, db *sql.DB, supplied ...*sql.Tx) *druntime.Runtime {
	resources := resource.New()
	must(t, resources.Register(write.WriterDatlyResourceNamespace, write.WriterDatlyResources))
	artifact := payloadArtifact(t, resources, reflect.TypeFor[write.WriterComponent](), reflect.TypeFor[write.Input](), reflect.TypeFor[write.Output]())
	views, err := viewprovider.New(viewprovider.Config{Dependencies: artifact.ViewDependencies, Input: artifact.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	handler, err := writer.New(artifact.Component, reflect.TypeFor[write.Input](), reflect.TypeFor[write.Output](), "patch")
	must(t, err)
	var tx *sql.Tx
	if len(supplied) > 0 {
		tx = supplied[0]
	}
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{{Component: artifact.Component, Input: artifact.Input, Output: artifact.Output, OutputType: reflect.TypeFor[write.Output](), Handler: handler, Providers: []locator.Provider{views}, DataSource: dml.Source{DB: db, Tx: tx}}}, druntime.WithResources(resources))
	must(t, err)
	return rt
}

// Observation only: verify every physical persisted field without using a
// handcrafted product read path.
func modelCallStoredRows(t *testing.T, db *sql.DB) []map[string]any {
	rows, err := db.Query("SELECT * FROM model_call ORDER BY message_id")
	must(t, err)
	defer rows.Close()
	columns, err := rows.Columns()
	must(t, err)
	result := []json.RawMessage{}
	for rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		must(t, rows.Scan(targets...))
		row := map[string]any{}
		for i, name := range columns {
			value := values[i]
			if b, ok := value.([]byte); ok {
				value = string(b)
			}
			row[strings.ReplaceAll(name, "_", "")] = value
		}
		raw, err := json.Marshal(row)
		must(t, err)
		result = append(result, raw)
	}
	must(t, rows.Err())
	return normalizeRowsInOrder(t, result)
}

func TestModelCallWriterCallerTransaction(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type useCase struct {
		desc   string
		input  bool
		expect string
	}
	for _, tc := range []useCase{{"caller rollback", false, "thinking"}, {"caller commit", true, "completed"}} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := modelCallFixture(t, project)
			db.SetMaxOpenConns(1)
			tx, err := db.BeginTx(context.Background(), nil)
			must(t, err)
			defer tx.Rollback()
			rt := modelCallWriterRuntime(t, db, tx)
			request := httptest.NewRequest("PATCH", "/v1/api/agently/modelcall", strings.NewReader(`{"data":[{"messageId":"existing","status":"completed"}]}`))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			_, err = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/modelcall", scope)
			must(t, err)
			var pending string
			must(t, tx.QueryRow("SELECT status FROM model_call WHERE message_id='existing'").Scan(&pending))
			if pending != "completed" {
				t.Fatal("caller did not retain pending update")
			}
			if tc.input {
				must(t, tx.Commit())
			} else {
				must(t, tx.Rollback())
			}
			var actual string
			must(t, db.QueryRow("SELECT status FROM model_call WHERE message_id='existing'").Scan(&actual))
			if actual != tc.expect {
				t.Fatalf("status=%s expected=%s", actual, tc.expect)
			}
		})
	}
}

func TestModelCallBatchFailureLegacyAndNative(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type useCase struct {
		desc, input string
		expect      bool
	}
	for _, tc := range []useCase{{"first update rejection leaves both unchanged", "existing", false}, {"second update rejection demonstrates corrected native rollback", "second", true}} {
		t.Run(tc.desc, func(t *testing.T) {
			oldDB, oldPath := modelCallFixture(t, project)
			db, _ := modelCallFixture(t, project)
			for _, fixture := range []*sql.DB{oldDB, db} {
				_, err := fixture.Exec(`INSERT INTO message(id,conversation_id,role) VALUES ('second','c1','assistant');
     INSERT INTO model_call(message_id,provider,model,model_kind,status) VALUES ('second','p','m','chat','thinking');
     CREATE TRIGGER reject_modelcall_update BEFORE UPDATE ON model_call WHEN OLD.message_id='` + tc.input + `' BEGIN SELECT RAISE(ABORT,'fixture update rejection'); END;`)
				must(t, err)
			}
			original := modelCallStoredRows(t, db)
			body := `{"data":[{"messageId":"existing","status":"completed"},{"messageId":"second","status":"completed"}]}`
			payload, err := json.Marshal(map[string]any{"Component": "modelCall", "DBPath": oldPath, "Body": body})
			must(t, err)
			process := exec.Command(legacy)
			process.Stdin = bytes.NewReader(payload)
			raw, err := process.Output()
			must(t, err)
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			if !before.Failed {
				t.Fatal("legacy accepted rejected update")
			}
			rt := modelCallWriterRuntime(t, db)
			request := httptest.NewRequest("PATCH", "/v1/api/agently/modelcall", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			_, err = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/modelcall", scope)
			if err == nil {
				t.Fatal("native accepted rejected update")
			}
			if !reflect.DeepEqual(modelCallStoredRows(t, db), original) {
				t.Fatal("native batch failure did not restore complete original state")
			}
			oldChanged := !reflect.DeepEqual(modelCallStoredRows(t, oldDB), original)
			if oldChanged != tc.expect {
				t.Fatalf("legacy partial commit=%v expected=%v", oldChanged, tc.expect)
			}
		})
	}
}

// Assert exact time values across legacy and native storage formats in both
// persisted state and responses; retain these fields in the complete comparison.
func assertModelCallTimestampParity(t *testing.T, legacy, native []map[string]any) {
	t.Helper()
	if len(legacy) != 1 || len(native) != 1 {
		t.Fatal("timestamp correction needs one row")
	}
	for name, expected := range map[string]string{"startedat": "2026-01-02T00:00:00Z", "completedat": "2026-01-02T00:01:00Z"} {
		if legacy[0][name] != expected || native[0][name] != expected {
			t.Fatalf("timestamp %s legacy=%v native=%v expectedNative=%s", name, legacy[0][name], native[0][name], expected)
		}
	}
}
