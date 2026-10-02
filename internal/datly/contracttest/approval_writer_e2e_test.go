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

	write "github.com/viant/agently-core/internal/datly/toolapprovalqueue/write"
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

func TestApprovalWriterLegacyParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type useCase struct {
		desc, input string
		expect      bool
	}
	for _, tc := range []useCase{
		{"insert defaults pending and UTC timestamps", `{"data":[{"id":"new","userId":"u1","toolName":"sql/query","arguments":[123,125]}]}`, false},
		{"empty insert status receives pending", `{"data":[{"id":"new","userId":"u1","toolName":"sql/query","arguments":[123,125],"status":""}]}`, false},
		{"sparse status update keeps required context", `{"data":[{"id":"existing","status":"approved"}]}`, false},
		{"identity-only patch", `{"data":[{"id":"existing"}]}`, false},
		{"provided status decision approver", `{"data":[{"id":"existing","status":"rejected","decision":"deny","approvedByUserId":"u2","approvedAt":"2026-01-01T00:00:00Z"}]}`, false},
		{"nullable link fields clear", `{"data":[{"id":"existing","conversationId":null,"turnId":null,"messageId":null}]}`, false},
		{"nullable text fields clear", `{"data":[{"id":"existing","title":null,"errorMessage":null,"decision":null}]}`, false},
		{"nullable metadata clears", `{"data":[{"id":"existing","metadata":null}]}`, false},
		{"provided empty optional text", `{"data":[{"id":"existing","title":"","errorMessage":"","decision":""}]}`, false},
		{"arguments and metadata retain binary values", `{"data":[{"id":"existing","arguments":[123,34,120,34,58,49,125],"metadata":[123,125]}]}`, false},
		{"explicit null creation retains previous", `{"data":[{"id":"existing","createdAt":null}]}`, false},
		{"explicit null update timestamp receives UTC now", `{"data":[{"id":"existing","updatedAt":null}]}`, false},
		{"supplied creation and update timestamps preserved", `{"data":[{"id":"existing","createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}]}`, false},
		{"timeout and execution dates", `{"data":[{"id":"existing","status":"timed_out","expiresAt":"2026-01-01T00:00:00Z","timedOutAt":"2026-01-01T00:00:00Z","executedAt":null}]}`, false},
		{"missing identity rejected", `{"data":[{"userId":"u1","toolName":"sql/query","arguments":[123,125]}]}`, true},
		{"missing owner rejected", `{"data":[{"id":"new","toolName":"sql/query","arguments":[123,125]}]}`, true},
		{"missing tool rejected", `{"data":[{"id":"new","userId":"u1","arguments":[123,125]}]}`, true},
		{"missing arguments rejected", `{"data":[{"id":"new","userId":"u1","toolName":"sql/query"}]}`, true},
		{"explicit null required arguments rejected", `{"data":[{"id":"existing","arguments":null}]}`, true},
		{"explicit empty existing status rejected", `{"data":[{"id":"existing","status":""}]}`, true},
		{"unknown conversation rejected", `{"data":[{"id":"new","userId":"u1","toolName":"sql/query","arguments":[123,125],"conversationId":"missing"}]}`, true},
		{"mixed insert and sparse update", `{"data":[{"id":"new","userId":"u1","toolName":"sql/query","arguments":[123,125]},{"id":"existing","status":"approved"}]}`, false},
		{"empty batch", `{"data":[]}`, false}, {"null batch", `{"data":null}`, false},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			oldDB, path := approvalFixture(t, project)
			db, _ := approvalFixture(t, project)
			payload, err := json.Marshal(map[string]any{"Component": "approval", "DBPath": path, "Body": tc.input})
			must(t, err)
			process := exec.Command(legacy)
			process.Stdin = bytes.NewReader(payload)
			raw, err := process.Output()
			must(t, err)
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			request := httptest.NewRequest("PATCH", "/v1/api/agently/toolapprovalqueue", strings.NewReader(tc.input))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			out, err := approvalWriterRuntime(t, db).ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/toolapprovalqueue", scope)
			if before.Failed != tc.expect || (err != nil) != tc.expect {
				t.Fatalf("failure legacy=%v (%s) native=%v expected=%v", before.Failed, before.Error, err, tc.expect)
			}
			oldRows, newRows := approvalStoredRows(t, oldDB), approvalStoredRows(t, db)
			if !reflect.DeepEqual(oldRows, newRows) {
				t.Fatalf("stored legacy=%s native=%s", pretty(oldRows), pretty(newRows))
			}
			if !tc.expect {
				var oldOutput []json.RawMessage
				must(t, json.Unmarshal(before.Output, &oldOutput))
				raw, err = json.Marshal(out.(*write.Output).Data)
				must(t, err)
				var newOutput []json.RawMessage
				must(t, json.Unmarshal(raw, &newOutput))
				if !reflect.DeepEqual(normalizeRowsInOrder(t, oldOutput), normalizeRowsInOrder(t, newOutput)) {
					t.Fatalf("response legacy=%s native=%s", before.Output, raw)
				}
			}
		})
	}
}
func approvalFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := goalFixture(t, project)
	_, err := db.Exec(`INSERT INTO turn(id,conversation_id,status) VALUES('t1','c1','running');INSERT INTO message(id,conversation_id,turn_id,role,type) VALUES('m1','c1','t1','assistant','text');
 INSERT INTO tool_approval_queue(id,user_id,conversation_id,turn_id,message_id,tool_name,title,arguments,metadata,status,decision,error_message,created_at) VALUES('existing','u1','c1','t1','m1','sql/query','original',X'7B7D',X'7B7D','pending','ask','old','2026-01-01 00:00:00');`)
	must(t, err)
	return db, path
}
func approvalWriterRuntime(t *testing.T, db *sql.DB, supplied ...*sql.Tx) *druntime.Runtime {
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

func approvalStoredRows(t *testing.T, db *sql.DB) []map[string]any {
	rows, err := db.Query("SELECT * FROM tool_approval_queue ORDER BY id")
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

func TestApprovalWriterMixedTransactions(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct{ caller, commit, reject bool }
	type expect struct{ failed, changed bool }
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{{"owned mixed mutation commits", input{}, expect{changed: true}}, {"late insertion rejection rolls back", input{reject: true}, expect{failed: true}}, {"caller mixed mutation rollback", input{caller: true}, expect{}}, {"caller mixed mutation commit", input{caller: true, commit: true}, expect{changed: true}}} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := approvalFixture(t, project)
			_, err := db.Exec(`INSERT INTO tool_approval_queue(id,user_id,tool_name,arguments,status) VALUES('delete','u1','sql/query',X'7B7D','pending')`)
			must(t, err)
			if tc.input.reject {
				_, err = db.Exec(`CREATE TRIGGER reject_approval_insert BEFORE INSERT ON tool_approval_queue WHEN NEW.id='new' BEGIN SELECT RAISE(ABORT,'fixture rejection'); END`)
				must(t, err)
			}
			var tx *sql.Tx
			if tc.input.caller {
				tx, err = db.BeginTx(context.Background(), nil)
				must(t, err)
				defer tx.Rollback()
			}
			body := `{"data":[{"id":"delete","shouldDelete":true},{"id":"existing","status":"approved"},{"id":"new","userId":"u1","toolName":"sql/query","arguments":[123,125]}]}`
			request := httptest.NewRequest("PATCH", "/v1/api/agently/toolapprovalqueue", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			_, err = approvalWriterRuntime(t, db, tx).ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/toolapprovalqueue", scope)
			if (err != nil) != tc.expect.failed {
				t.Fatalf("failure=%v expected=%v", err, tc.expect.failed)
			}
			if tx != nil {
				var pending string
				must(t, tx.QueryRow("SELECT status FROM tool_approval_queue WHERE id='existing'").Scan(&pending))
				if pending != "approved" {
					t.Fatal("caller did not retain pending change")
				}
				if tc.input.commit {
					must(t, tx.Commit())
				} else {
					must(t, tx.Rollback())
				}
			}
			var status string
			must(t, db.QueryRow("SELECT status FROM tool_approval_queue WHERE id='existing'").Scan(&status))
			want := "pending"
			if tc.expect.changed {
				want = "approved"
			}
			if status != want {
				t.Fatalf("status=%s expected=%s", status, want)
			}
			var inserted, deleted int
			must(t, db.QueryRow("SELECT COUNT(*) FROM tool_approval_queue WHERE id='new'").Scan(&inserted))
			must(t, db.QueryRow("SELECT COUNT(*) FROM tool_approval_queue WHERE id='delete'").Scan(&deleted))
			if tc.expect.changed {
				if inserted != 1 || deleted != 0 {
					t.Fatal("mixed commit missing")
				}
			} else if inserted != 0 || deleted != 1 {
				t.Fatal("mixed rollback incomplete")
			}
		})
	}
}
func TestApprovalWriterIdempotentDelete(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type useCase struct {
		desc, input string
		expect      int
	}
	for _, tc := range []useCase{{"unknown delete is ignored", `{"data":[{"id":"missing","shouldDelete":true}]}`, 1}, {"existing and repeated delete", `{"data":[{"id":"existing","shouldDelete":true},{"id":"existing","shouldDelete":true}]}`, 0}} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := approvalFixture(t, project)
			request := httptest.NewRequest("PATCH", "/v1/api/agently/toolapprovalqueue", strings.NewReader(tc.input))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			_, err = approvalWriterRuntime(t, db).ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/toolapprovalqueue", scope)
			must(t, err)
			var count int
			must(t, db.QueryRow("SELECT COUNT(*) FROM tool_approval_queue").Scan(&count))
			if count != tc.expect {
				t.Fatalf("count=%d expected=%d", count, tc.expect)
			}
		})
	}
}
