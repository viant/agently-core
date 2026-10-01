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
	"time"

	read "github.com/viant/agently-core/internal/datly/turn/read"
	write "github.com/viant/agently-core/internal/datly/turn/write"
	"github.com/viant/bindly/locator"
	requestprovider "github.com/viant/bindly/provider/request"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	dexec "github.com/viant/datly/exec"
	druntime "github.com/viant/datly/runtime"
	writer "github.com/viant/datly/runtime/handler/writer"
	"github.com/viant/datly/runtime/registry"
	"github.com/viant/datly/spec"
	dsql "github.com/viant/datly/sql"
	"github.com/viant/datly/sql/dml"
	viewprovider "github.com/viant/datly/sql/reader/provider"
)

func TestTurnWriterLegacyParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type expect struct {
		failed bool
		count  int
	}
	type useCase struct {
		desc   string
		input  string
		expect expect
	}
	for _, tc := range []useCase{
		{"insert creation default", `{"data":[{"id":"new","conversationId":"c1","status":"queued"}]}`, expect{false, 2}},
		{"insert overwrites supplied creation time like legacy", `{"data":[{"id":"new","conversationId":"c1","status":"queued","createdAt":"2000-01-01T00:00:00Z"}]}`, expect{false, 2}},
		{"explicit zero and empty", `{"data":[{"id":"new","conversationId":"c1","status":"queued","queueSeq":0,"origin":""}]}`, expect{false, 2}},
		{"sparse status patch", `{"data":[{"id":"existing","status":"running"}]}`, expect{false, 1}},
		{"explicit null clears optional metadata", `{"data":[{"id":"existing","errorMessage":null}]}`, expect{false, 1}},
		{"explicit empty required status is rejected", `{"data":[{"id":"existing","status":""}]}`, expect{true, 1}},
		{"missing identity is rejected", `{"data":[{"conversationId":"c1","status":"queued"}]}`, expect{true, 1}},
		{"missing conversation is rejected", `{"data":[{"id":"new","status":"queued"}]}`, expect{true, 1}},
		{"missing status is rejected", `{"data":[{"id":"new","conversationId":"c1"}]}`, expect{true, 1}},
		{"unknown foreign conversation is rejected", `{"data":[{"id":"new","conversationId":"absent","status":"queued"}]}`, expect{true, 1}},
		{"mixed insert and sparse update", `{"data":[{"id":"new","conversationId":"c1","status":"queued"},{"id":"existing","status":"running"}]}`, expect{false, 2}},
		{"invalid batch rolls back", `{"data":[{"id":"existing","status":"running"},{"id":"new","conversationId":"c1"}]}`, expect{true, 1}},
		{"empty batch", `{"data":[]}`, expect{false, 1}},
		{"null batch", `{"data":null}`, expect{false, 1}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, oldPath := turnWriterFixture(t, project)
			db, _ := turnWriterFixture(t, project)
			payload, err := json.Marshal(map[string]any{"Component": "turn", "DBPath": oldPath, "Body": tc.input, "Filters": map[string]string{"conversationId": "c1"}})
			must(t, err)
			process := exec.Command(legacy)
			process.Stdin = bytes.NewReader(payload)
			raw, err := process.Output()
			must(t, err)
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			rt, key := turnWriterRuntime(t, db)
			request := httptest.NewRequest("PATCH", "/v1/api/agently/turn", strings.NewReader(tc.input))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			mutationOutput, err := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/turn", scope)
			if before.Failed != tc.expect.failed || (err != nil) != tc.expect.failed {
				t.Fatalf("failure legacy=%v native=%v expected=%v legacyError=%s", before.Failed, err, tc.expect.failed, before.Error)
			}
			input := &read.TurnRowsInput{ConversationID: "c1", Has: &read.TurnRowsInputHas{ConversationID: true}}
			value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/turn/list/list"}}, Input: input})
			must(t, err)
			raw, err = json.Marshal(value.(*read.TurnRowsOutput).Data)
			must(t, err)
			var rows []json.RawMessage
			must(t, json.Unmarshal(raw, &rows))
			oldRows, newRows := normalizeRows(t, before.Rows), normalizeRows(t, rows)
			if len(newRows) != tc.expect.count || !reflect.DeepEqual(oldRows, newRows) {
				t.Fatalf("stored state legacy=%s native=%s", pretty(oldRows), pretty(newRows))
			}
			oldDB, err := sql.Open("sqlite3", oldPath)
			must(t, err)
			defer oldDB.Close()
			if !reflect.DeepEqual(turnStoredRows(t, oldDB), turnStoredRows(t, db)) {
				t.Fatal("complete stored turn rows differ")
			}
			if !tc.expect.failed {
				var oldOutput []json.RawMessage
				must(t, json.Unmarshal(before.Output, &oldOutput))
				raw, err := json.Marshal(mutationOutput.(*write.Output).Data)
				must(t, err)
				var newOutput []json.RawMessage
				must(t, json.Unmarshal(raw, &newOutput))
				if !reflect.DeepEqual(normalizeRows(t, oldOutput), normalizeRows(t, newOutput)) {
					t.Fatalf("writer response differs: legacy=%s native=%s", before.Output, raw)
				}
			}
			if !tc.expect.failed && strings.Contains(tc.input, `"id":"new"`) {
				var created time.Time
				must(t, db.QueryRow("SELECT created_at FROM turn WHERE id='new'").Scan(&created))
				if created.Before(time.Now().Add(-time.Minute)) {
					t.Fatal("new turn did not receive a fresh creation timestamp")
				}
			}
		})
	}
}

func turnWriterFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := goalFixture(t, project)
	_, err := db.Exec(`INSERT INTO turn(id,conversation_id,status,queue_seq,error_message,origin,created_at) VALUES ('existing','c1','queued',7,'old error','user','2026-01-01 00:00:00')`)
	must(t, err)
	return db, path
}

func turnWriterRuntime(t *testing.T, db *sql.DB, supplied ...*sql.Tx) (*druntime.Runtime, spec.Key) {
	resources := resource.New()
	must(t, resources.Register(read.ReaderDatlyResourceNamespace, read.ReaderDatlyResources))
	must(t, resources.Register(write.WriterDatlyResourceNamespace, write.WriterDatlyResources))
	ra := payloadArtifact(t, resources, reflect.TypeFor[read.ReaderComponent](), reflect.TypeFor[read.TurnRowsInput](), reflect.TypeFor[read.TurnRowsOutput]())
	wa := payloadArtifact(t, resources, reflect.TypeFor[write.WriterComponent](), reflect.TypeFor[write.Input](), reflect.TypeFor[write.Output]())
	reader, err := ra.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	views, err := viewprovider.New(viewprovider.Config{Dependencies: wa.ViewDependencies, Input: wa.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	handler, err := writer.New(wa.Component, reflect.TypeFor[write.Input](), reflect.TypeFor[write.Output](), "patch")
	must(t, err)
	var tx *sql.Tx
	if len(supplied) > 0 {
		tx = supplied[0]
	}
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{
		{Component: ra.Component, Input: ra.Input, Output: ra.Output, OutputType: reflect.TypeFor[read.TurnRowsOutput](), Reader: reader, Providers: []locator.Provider{ordinaryAccess("turnaccess", func(context.Context, reflect.Type, string) (any, bool, error) { return "rows", true, nil })}},
		{Component: wa.Component, Input: wa.Input, Output: wa.Output, OutputType: reflect.TypeFor[write.Output](), Handler: handler, Providers: []locator.Provider{views}, DataSource: dml.Source{DB: db, Tx: tx}},
	}, druntime.WithResources(resources))
	must(t, err)
	return rt, ra.Component.Key
}

// Fixture observation covers every physical column, including those omitted by
// the legacy list projection. This helper is not product persistence.
func turnStoredRows(t *testing.T, db *sql.DB) []map[string]any {
	rows, err := db.Query("SELECT * FROM turn ORDER BY id")
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
			if text, ok := value.([]byte); ok {
				value = string(text)
			}
			row[strings.ReplaceAll(name, "_", "")] = value
		}
		raw, err := json.Marshal(row)
		must(t, err)
		result = append(result, raw)
	}
	must(t, rows.Err())
	return normalizeRows(t, result)
}

func TestTurnWriterCallerTransaction(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type useCase struct {
		desc   string
		input  bool
		expect string
	}
	for _, tc := range []useCase{{"caller rollback", false, "queued"}, {"caller commit", true, "running"}} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := turnWriterFixture(t, project)
			db.SetMaxOpenConns(1)
			tx, err := db.BeginTx(context.Background(), nil)
			must(t, err)
			defer tx.Rollback()
			rt, _ := turnWriterRuntime(t, db, tx)
			request := httptest.NewRequest("PATCH", "/v1/api/agently/turn", strings.NewReader(`{"data":[{"id":"existing","status":"running"}]}`))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			_, err = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/turn", scope)
			must(t, err)
			var pending string
			must(t, tx.QueryRow("SELECT status FROM turn WHERE id='existing'").Scan(&pending))
			if pending != "running" {
				t.Fatal("caller transaction did not retain pending mutation")
			}
			if tc.input {
				must(t, tx.Commit())
			} else {
				must(t, tx.Rollback())
			}
			var actual string
			must(t, db.QueryRow("SELECT status FROM turn WHERE id='existing'").Scan(&actual))
			if actual != tc.expect {
				t.Fatalf("stored status=%s expected=%s", actual, tc.expect)
			}
		})
	}
}
