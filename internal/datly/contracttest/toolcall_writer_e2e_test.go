package tests

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	write "github.com/viant/agently-core/internal/datly/toolcall/write"
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

func TestToolCallWriterLegacyParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type expect struct{ failed bool }
	type useCase struct {
		desc, input string
		expect      expect
	}
	body := func(fields map[string]any) string {
		raw, err := json.Marshal(map[string]any{"data": []any{fields}})
		must(t, err)
		return string(raw)
	}
	for _, tc := range []useCase{
		{"insert defaults attempt to one", `{"data":[{"messageId":"new","opId":"new-op","toolName":"query","toolKind":"mcp","status":"running"}]}`, expect{}},
		{"explicit zero attempt remains supplied", `{"data":[{"messageId":"new","opId":"new-op","attempt":0,"toolName":"query","toolKind":"mcp","status":"running"}]}`, expect{}},
		{"sparse status update", `{"data":[{"messageId":"existing","status":"completed"}]}`, expect{}},
		{"identity-only update", `{"data":[{"messageId":"existing"}]}`, expect{}},
		{"zero fields remain supplied", `{"data":[{"messageId":"existing","attempt":0,"retriable":0,"latencyMs":0,"cost":0,"iteration":0}]}`, expect{}},
		{"empty optional strings remain supplied", `{"data":[{"messageId":"existing","runId":"","requestHash":"","errorMessage":""}]}`, expect{}},
		{"null clears optional values", `{"data":[{"messageId":"existing","errorMessage":null,"startedAt":null,"cost":null,"requestPayloadId":null}]}`, expect{}},
		{"timestamps retain exact values", `{"data":[{"messageId":"existing","startedAt":"2026-01-02T00:00:00Z","completedAt":"2026-01-02T00:01:00Z"}]}`, expect{}},
		{"valid payload references", `{"data":[{"messageId":"existing","requestPayloadId":"p1","responsePayloadId":"p1"}]}`, expect{}},
		{"transient response overflow remains in response", `{"data":[{"messageId":"existing","responseOverflow":true}]}`, expect{}},
		{"long ASCII error message is sanitized", body(map[string]any{"messageId": "existing", "errorMessage": strings.Repeat("abcdefghij", 7000)}), expect{}},
		{"long UTF8 error message is sanitized", body(map[string]any{"messageId": "existing", "errorMessage": strings.Repeat("界", 24000)}), expect{}},
		{"missing message identity rejected", `{"data":[{"opId":"op","toolName":"query","toolKind":"mcp","status":"running"}]}`, expect{failed: true}},
		{"missing op identity rejected", `{"data":[{"messageId":"new","toolName":"query","toolKind":"mcp","status":"running"}]}`, expect{failed: true}},
		{"missing tool name rejected", `{"data":[{"messageId":"new","opId":"op","toolKind":"mcp","status":"running"}]}`, expect{failed: true}},
		{"missing tool kind rejected", `{"data":[{"messageId":"new","opId":"op","toolName":"query","status":"running"}]}`, expect{failed: true}},
		{"missing status rejected", `{"data":[{"messageId":"new","opId":"op","toolName":"query","toolKind":"mcp"}]}`, expect{failed: true}},
		{"empty required update field rejected", `{"data":[{"messageId":"existing","status":""}]}`, expect{failed: true}},
		{"unknown message reference rejected", `{"data":[{"messageId":"absent","opId":"op","toolName":"query","toolKind":"mcp","status":"running"}]}`, expect{failed: true}},
		{"unknown payload reference rejected", `{"data":[{"messageId":"existing","requestPayloadId":"absent"}]}`, expect{failed: true}},
		{"composite op attempt uniqueness rejected", `{"data":[{"messageId":"new","turnId":"t1","opId":"existing-op","attempt":2,"toolName":"query","toolKind":"mcp","status":"running"}]}`, expect{failed: true}},
		{"mixed insert and sparse update", `{"data":[{"messageId":"new","opId":"new-op","toolName":"query","toolKind":"mcp","status":"running"},{"messageId":"existing","status":"completed"}]}`, expect{}},
		{"empty batch", `{"data":[]}`, expect{}},
		{"null batch", `{"data":null}`, expect{}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := toolCallFixture(t, project)
			initial := toolCallStoredRows(t, db)
			rt := toolCallWriterRuntime(t, db)
			request := httptest.NewRequest("PATCH", "/v1/api/agently/toolcall", strings.NewReader(tc.input))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			value, err := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/toolcall", scope)
			if (err != nil) != tc.expect.failed {
				t.Fatalf("native error=%v expected failure=%v", err, tc.expect.failed)
			}
			newRows := toolCallStoredRows(t, db)
			if tc.expect.failed && !reflect.DeepEqual(initial, newRows) {
				t.Fatalf("failed mutation changed stored rows: before=%s after=%s", pretty(initial), pretty(newRows))
			}
			if !tc.expect.failed {
				var submitted struct {
					Data []map[string]any `json:"data"`
				}
				must(t, json.Unmarshal([]byte(tc.input), &submitted))
				if len(value.(*write.Output).Data) != len(submitted.Data) {
					t.Fatalf("response rows=%d submitted=%d", len(value.(*write.Output).Data), len(submitted.Data))
				}
				indexed := map[string]map[string]any{}
				for _, row := range newRows {
					indexed[row["messageid"].(string)] = row
				}
				for _, submittedRow := range submitted.Data {
					stored := indexed[submittedRow["messageId"].(string)]
					if stored == nil {
						t.Fatalf("missing persisted row for %v", submittedRow["messageId"])
					}
					for field, want := range submittedRow {
						if field == "responseOverflow" || field == "startedAt" || field == "completedAt" {
							continue
						}
						if field == "errorMessage" && len(fmt.Sprint(want)) > 65535 {
							if len(fmt.Sprint(stored["errormessage"])) >= len(fmt.Sprint(want)) {
								t.Fatalf("error message was not truncated")
							}
							continue
						}
						if got := stored[strings.ToLower(field)]; !reflect.DeepEqual(got, want) {
							t.Fatalf("stored %s=%v, want %v", field, got, want)
						}
					}
					if submittedRow["messageId"] == "new" {
						if _, supplied := submittedRow["attempt"]; !supplied && stored["attempt"] != float64(1) {
							t.Fatalf("default attempt=%v, want 1", stored["attempt"])
						}
					}
				}
			}
		})
	}
}

func toolCallFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := goalFixture(t, project)
	_, err := db.Exec(`INSERT INTO turn(id,conversation_id,status) VALUES ('t1','c1','running');
 INSERT INTO message(id,conversation_id,turn_id,role) VALUES ('existing','c1','t1','assistant'),('new','c1','t1','assistant');
 INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage) VALUES ('p1','request','application/json',2,'inline');
 INSERT INTO tool_call(message_id,turn_id,op_id,attempt,tool_name,tool_kind,status,error_message,request_hash,request_payload_id,started_at,cost) VALUES ('existing','t1','existing-op',2,'query','mcp','running','old error','old hash','p1','2026-01-01 00:00:00',1.5);`)
	must(t, err)
	return db, path
}

func toolCallWriterRuntime(t *testing.T, db *sql.DB, supplied ...*sql.Tx) *druntime.Runtime {
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
func toolCallStoredRows(t *testing.T, db *sql.DB) []map[string]any {
	rows, err := db.Query("SELECT * FROM tool_call ORDER BY message_id")
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

func TestToolCallWriterCallerTransaction(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type useCase struct {
		desc   string
		input  bool
		expect string
	}
	for _, tc := range []useCase{{"caller rollback", false, "running"}, {"caller commit", true, "completed"}} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := toolCallFixture(t, project)
			db.SetMaxOpenConns(1)
			tx, err := db.BeginTx(context.Background(), nil)
			must(t, err)
			defer tx.Rollback()
			rt := toolCallWriterRuntime(t, db, tx)
			request := httptest.NewRequest("PATCH", "/v1/api/agently/toolcall", strings.NewReader(`{"data":[{"messageId":"existing","status":"completed"}]}`))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			_, err = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/toolcall", scope)
			must(t, err)
			var pending string
			must(t, tx.QueryRow("SELECT status FROM tool_call WHERE message_id='existing'").Scan(&pending))
			if pending != "completed" {
				t.Fatal("caller did not retain pending update")
			}
			if tc.input {
				must(t, tx.Commit())
			} else {
				must(t, tx.Rollback())
			}
			var actual string
			must(t, db.QueryRow("SELECT status FROM tool_call WHERE message_id='existing'").Scan(&actual))
			if actual != tc.expect {
				t.Fatalf("status=%s expected=%s", actual, tc.expect)
			}
		})
	}
}
