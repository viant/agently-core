package tests

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	queueread "github.com/viant/agently-core/internal/datly/turnqueue/read"
	queuewrite "github.com/viant/agently-core/internal/datly/turnqueue/write"
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

func TestTurnQueueLegacyV1Parity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		body    string
		filters map[string]any
	}
	type expect struct {
		failed bool
		ids    []string
		fields map[string]map[string]any
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	insertion := `{"id":"q3","conversationId":"c1","turnId":"t3","messageId":"m3","queueSeq":0}`
	for _, tc := range []useCase{
		{desc: "reader preserves sequence ordering", expect: expect{ids: []string{"q2", "q1"}}},
		{desc: "status filter selects queued rows", input: input{filters: map[string]any{"status": "queued"}}, expect: expect{ids: []string{"q1"}}},
		{desc: "provided empty status remains an active predicate", input: input{filters: map[string]any{"status": ""}}, expect: expect{ids: []string{}}},
		{desc: "unknown ID returns no rows", input: input{filters: map[string]any{"id": "absent"}}, expect: expect{ids: []string{}}},
		{desc: "insert defaults status and timestamps and preserves zero sequence", input: input{body: `{"data":[` + insertion + `]}`}, expect: expect{ids: []string{"q3", "q2", "q1"}, fields: map[string]map[string]any{"q3": {"status": "queued", "queueseq": float64(0)}}}},
		{desc: "sparse status patch preserves required links", input: input{body: `{"data":[{"id":"q1","status":"completed"}]}`}, expect: expect{fields: map[string]map[string]any{"q1": {"status": "completed", "conversationid": "c1", "turnid": "t1", "messageid": "m1", "queueseq": float64(2)}}}},
		{desc: "omitted status retains legacy forced queued default", input: input{body: `{"data":[{"id":"q2","queueSeq":0}]}`}, expect: expect{fields: map[string]map[string]any{"q2": {"status": "queued", "queueseq": float64(0)}}}},
		{desc: "explicit empty status receives queued default", input: input{body: `{"data":[{"id":"q1","status":""}]}`}, expect: expect{fields: map[string]map[string]any{"q1": {"status": "queued"}}}},
		{desc: "provided timestamps remain authoritative", input: input{body: `{"data":[{"id":"q1","createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}]}`}, expect: expect{fields: map[string]map[string]any{"q1": {"createdat": "2026-01-01T00:00:00Z", "updatedat": "2026-01-01T00:00:00Z"}}}},
		{desc: "nil collection is a no-op", input: input{body: `{"data":null}`}},
		{desc: "empty collection is a no-op", input: input{body: `{"data":[]}`}},
		{desc: "missing identity is rejected", input: input{body: `{"data":[{"conversationId":"c1","turnId":"t3","messageId":"m3","queueSeq":3}]}`}, expect: expect{failed: true}},
		{desc: "explicit null nonnullable sequence is rejected", input: input{body: `{"data":[{"id":"q1","queueSeq":null}]}`}, expect: expect{failed: true}},
		{desc: "mixed batch rolls back earlier update when insert fails", input: input{body: `{"data":[{"id":"q1","status":"completed"},{"id":"q-reject","conversationId":"c1","turnId":"t3","messageId":"m3","queueSeq":3}]}`}, expect: expect{failed: true, fields: map[string]map[string]any{"q1": {"status": "queued", "createdat": "2026-01-01T00:00:00Z", "updatedat": nil}}}},
		{desc: "database insert rejection preserves existing rows", input: input{body: `{"data":[{"id":"q-reject","conversationId":"c1","turnId":"t3","messageId":"m3","queueSeq":3}]}`}, expect: expect{failed: true}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := queueParityFixture(t, project)
			filters := tc.input.filters
			if filters == nil {
				filters = map[string]any{}
			}
			rt, key := queueParityRuntime(t, db)
			var outputRows int
			var mutationError error
			if tc.input.body != "" {
				req := httptest.NewRequest("PATCH", "/v1/api/agently/turnqueue", strings.NewReader(tc.input.body))
				req.Header.Set("Content-Type", "application/json")
				scope, err := requestprovider.New(req)
				must(t, err)
				defer scope.Close()
				result, err := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/turnqueue", scope)
				mutationError = err
				if err == nil {
					outputRows = len(result.(*queuewrite.Output).Data)
				}
			}
			if (mutationError != nil) != tc.expect.failed {
				t.Fatalf("native error=%v expected failure=%v", mutationError, tc.expect.failed)
			}
			readerInput := &queueread.QueueRowsInput{Has: &queueread.QueueRowsInputHas{}}
			for _, field := range []struct {
				name    string
				value   *string
				present *bool
			}{
				{"id", &readerInput.Id, &readerInput.Has.Id}, {"conversationId", &readerInput.ConversationId, &readerInput.Has.ConversationId}, {"status", &readerInput.QueueStatus, &readerInput.Has.QueueStatus},
			} {
				if value, present := filters[field.name]; present {
					encoded, err := json.Marshal(value)
					must(t, err)
					must(t, json.Unmarshal(encoded, field.value))
					*field.present = true
				}
			}
			output, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/turnqueue/list"}}, Input: readerInput})
			must(t, err)
			data, err := json.Marshal(output.(*queueread.QueueRowsOutput).Data)
			must(t, err)
			var rows []json.RawMessage
			must(t, json.Unmarshal(data, &rows))
			newRows := normalizeRowsInOrder(t, rows)
			ids := []string{}
			indexed := map[string]map[string]any{}
			for _, row := range newRows {
				id := row["id"].(string)
				ids = append(ids, id)
				indexed[id] = row
			}
			expectedIDs := tc.expect.ids
			if expectedIDs == nil {
				expectedIDs = []string{"q2", "q1"}
			}
			if !reflect.DeepEqual(ids, expectedIDs) {
				t.Fatalf("IDs=%v expected=%v", ids, expectedIDs)
			}
			for id, fields := range tc.expect.fields {
				for field, value := range fields {
					if !reflect.DeepEqual(indexed[id][field], value) {
						t.Errorf("%s.%s=%v expected=%v", id, field, indexed[id][field], value)
					}
				}
			}
			if tc.input.body != "" && !tc.expect.failed {
				var submitted struct {
					Data []map[string]any `json:"data"`
				}
				must(t, json.Unmarshal([]byte(tc.input.body), &submitted))
				if outputRows != len(submitted.Data) {
					t.Fatalf("write response rows=%d submitted=%d", outputRows, len(submitted.Data))
				}
			}
		})
	}
}

func queueParityFixture(t *testing.T, project string) (*sql.DB, string) {
	t.Helper()
	db, path := goalFixture(t, project)
	_, err := db.Exec(`
 INSERT INTO turn(id,conversation_id,status,created_at) VALUES('t1','c1','queued','2026-01-01 00:00:00'),('t2','c1','queued','2026-01-01 00:00:00'),('t3','c1','queued','2026-01-01 00:00:00');
 INSERT INTO message(id,conversation_id,turn_id,role,created_at) VALUES('m1','c1','t1','user','2026-01-01 00:00:00'),('m2','c1','t2','user','2026-01-01 00:00:00'),('m3','c1','t3','user','2026-01-01 00:00:00');
 INSERT INTO turn_queue(id,conversation_id,turn_id,message_id,queue_seq,status,created_at,updated_at) VALUES('q1','c1','t1','m1',2,'queued','2026-01-01 00:00:00',NULL),('q2','c1','t2','m2',1,'canceled','2026-01-01 00:00:00',NULL);
 CREATE TRIGGER reject_queue BEFORE INSERT ON turn_queue WHEN NEW.id='q-reject' BEGIN SELECT RAISE(ABORT,'fixture queue rejection'); END;`)
	must(t, err)
	return db, path
}

func queueParityRuntime(t *testing.T, db *sql.DB, supplied ...*sql.Tx) (*druntime.Runtime, spec.Key) {
	t.Helper()
	resources := resource.New()
	must(t, resources.Register(queueread.ReaderDatlyResourceNamespace, queueread.ReaderDatlyResources))
	must(t, resources.Register(queuewrite.WriterDatlyResourceNamespace, queuewrite.WriterDatlyResources))
	ra := payloadArtifact(t, resources, reflect.TypeFor[queueread.ReaderComponent](), reflect.TypeFor[queueread.QueueRowsInput](), reflect.TypeFor[queueread.QueueRowsOutput]())
	wa := payloadArtifact(t, resources, reflect.TypeFor[queuewrite.WriterComponent](), reflect.TypeFor[queuewrite.Input](), reflect.TypeFor[queuewrite.Output]())
	reader, err := ra.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	views, err := viewprovider.New(viewprovider.Config{Dependencies: wa.ViewDependencies, Input: wa.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	handler, err := writer.New(wa.Component, reflect.TypeFor[queuewrite.Input](), reflect.TypeFor[queuewrite.Output](), "patch")
	must(t, err)
	var tx *sql.Tx
	if len(supplied) > 0 {
		tx = supplied[0]
	}
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{
		{Component: ra.Component, Input: ra.Input, Output: ra.Output, OutputType: reflect.TypeFor[queueread.QueueRowsOutput](), Reader: reader},
		{Component: wa.Component, Input: wa.Input, Output: wa.Output, OutputType: reflect.TypeFor[queuewrite.Output](), Handler: handler, Providers: []locator.Provider{views}, DataSource: dml.Source{DB: db, Tx: tx}},
	}, druntime.WithResources(resources))
	must(t, err)
	return rt, ra.Component.Key
}

func TestTurnQueueCallerTransactionOwnership(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "caller rollback", true: "caller commit"}[commit], func(t *testing.T) {
			db, _ := queueParityFixture(t, project)
			db.SetMaxOpenConns(1)
			tx, err := db.BeginTx(context.Background(), nil)
			must(t, err)
			defer tx.Rollback()
			rt, _ := queueParityRuntime(t, db, tx)
			request := httptest.NewRequest("PATCH", "/v1/api/agently/turnqueue", strings.NewReader(`{"data":[{"id":"q1","status":"completed"}]}`))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			_, err = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/turnqueue", scope)
			must(t, err)
			var status string
			must(t, tx.QueryRow("SELECT status FROM turn_queue WHERE id='q1'").Scan(&status))
			if status != "completed" {
				t.Fatalf("pending transaction status=%s", status)
			}
			if commit {
				must(t, tx.Commit())
			} else {
				must(t, tx.Rollback())
			}
			must(t, db.QueryRow("SELECT status FROM turn_queue WHERE id='q1'").Scan(&status))
			expected := "queued"
			if commit {
				expected = "completed"
			}
			if status != expected {
				t.Fatalf("stored status=%s expected=%s", status, expected)
			}
		})
	}
}
