package tests

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	read "github.com/viant/agently-core/internal/datly/goal/read"
	write "github.com/viant/agently-core/internal/datly/goal/write"
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
	dtag "github.com/viant/datly/tag"
	_ "github.com/viant/sqlx/metadata/product/sqlite"
)

type expectation struct {
	failed bool
	rows   map[string]map[string]any
}
type useCase struct {
	desc   string
	input  mutationInput
	expect expectation
}
type mutationInput struct{ method, body string }
type probeResult struct {
	Failed bool
	Error  string
	Rows   []json.RawMessage
	Output json.RawMessage
}

func TestGoalNativeContract(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	base := map[string]any{"objective": "original", "status": "active", "tokenbudget": float64(100), "tokensused": float64(17), "statusreason": "why"}
	cases := []useCase{
		{desc: "reader returns existing row and excludes empty and wrong conversation scopes", expect: expectation{rows: map[string]map[string]any{"g1": base}}},
		{desc: "insert applies legacy defaults through generated lifecycle", input: mutationInput{body: `{"data":[{"id":"g2","conversationId":"c2","objective":"new","status":"active"}]}`}, expect: expectation{rows: map[string]map[string]any{"g1": base, "g2": {"objective": "new", "tokensused": float64(0), "timeusedseconds": float64(0), "autonomousturnsused": float64(0), "consecutivenoprogress": float64(0), "lastcontinuationfingerprint": ""}}}},
		{desc: "sparse update preserves every omitted physical field", input: mutationInput{body: `{"data":[{"id":"g1","status":"complete"}]}`}, expect: expectation{rows: map[string]map[string]any{"g1": {"objective": "original", "status": "complete", "tokenbudget": float64(100), "tokensused": float64(17), "statusreason": "why"}}}},
		{desc: "explicit zero empty and null remain distinct from omission", input: mutationInput{body: `{"data":[{"id":"g1","tokensUsed":0,"tokenBudget":0,"statusReason":null,"pauseReason":""}]}`}, expect: expectation{rows: map[string]map[string]any{"g1": {"objective": "original", "status": "active", "tokensused": float64(0), "tokenbudget": float64(0), "statusreason": nil, "pausereason": ""}}}},
		{desc: "mixed insert and sparse update retain request output order", input: mutationInput{body: `{"data":[{"id":"g2","conversationId":"c2","objective":"new","status":"active"},{"id":"g1","tokensUsed":21}]}`}, expect: expectation{rows: map[string]map[string]any{"g1": {"objective": "original", "tokensused": float64(21)}, "g2": {"objective": "new", "tokensused": float64(0)}}}},
		{desc: "missing insert objective fails before mutation", input: mutationInput{body: `{"data":[{"id":"g2","conversationId":"c2","status":"active"}]}`}, expect: expectation{failed: true, rows: map[string]map[string]any{"g1": base}}},
		{desc: "empty insert objective fails business validation", input: mutationInput{body: `{"data":[{"id":"g2","conversationId":"c2","objective":"  ","status":"active"}]}`}, expect: expectation{failed: true, rows: map[string]map[string]any{"g1": base}}},
		{desc: "missing identity fails before mutation", input: mutationInput{body: `{"data":[{"conversationId":"c2","objective":"new","status":"active"}]}`}, expect: expectation{failed: true, rows: map[string]map[string]any{"g1": base}}},
		{desc: "foreign-key failure leaves existing data intact", input: mutationInput{body: `{"data":[{"id":"g2","conversationId":"absent","objective":"new","status":"active"}]}`}, expect: expectation{failed: true, rows: map[string]map[string]any{"g1": base}}},
		{desc: "late database failure rolls back earlier sparse update", input: mutationInput{body: `{"data":[{"id":"g1","status":"complete"},{"id":"g2","conversationId":"c2","objective":"database-reject","status":"active"}]}`}, expect: expectation{failed: true, rows: map[string]map[string]any{"g1": base}}},
	}
	cases = append(cases, useCase{desc: "explicit generated deletion removes only the selected goal", input: mutationInput{method: "DELETE", body: `{"data":[{"id":"g1","shouldDelete":true}]}`}, expect: expectation{rows: map[string]map[string]any{}}})
	cases = append(cases,
		useCase{desc: "canonical writer ignores missing deletion identity", input: mutationInput{method: "DELETE", body: `{"data":[{"id":"absent","shouldDelete":true}]}`}, expect: expectation{rows: map[string]map[string]any{"g1": base}}},
		useCase{desc: "canonical writer deletes existing and ignores absent identity", input: mutationInput{method: "DELETE", body: `{"data":[{"id":"g1","shouldDelete":true},{"id":"absent","shouldDelete":true}]}`}, expect: expectation{rows: map[string]map[string]any{}}},
	)
	for _, test := range cases {
		t.Run(test.desc, func(t *testing.T) {
			newDB, _ := goalFixture(t, project)
			rt, readerKey := goalRuntime(t, newDB, false)
			after := probeResult{Rows: []json.RawMessage{}}
			if test.input.body != "" {
				path := "/v1/api/agently/goal"
				// The canonical writer handles both sparse writes and explicit deletion.
				req := httptest.NewRequest("PATCH", path, strings.NewReader(test.input.body))
				req.Header.Set("Content-Type", "application/json")
				scope, err := requestprovider.New(req)
				must(t, err)
				value, runErr := rt.ExecuteRoute(context.Background(), "PATCH", path, scope)
				must(t, scope.Close())
				after.Failed = runErr != nil
				if runErr != nil {
					after.Error = runErr.Error()
				} else if test.input.method != "DELETE" {
					after.Output, _ = json.Marshal(value.(*write.Output).Data)
				}
			}
			for _, id := range []string{"c1", "c2", "c3", "wrong"} {
				input := &read.GoalInput{}
				input.SetConversationID(id)
				value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: readerKey, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/goal/{conversationId}"}}, Input: input})
				must(t, err)
				for _, row := range value.(*read.GoalOutput).Data {
					data, err := json.Marshal(row)
					must(t, err)
					after.Rows = append(after.Rows, data)
				}
			}
			if after.Failed != test.expect.failed {
				t.Fatalf("native failure=%t (%s), expected=%t", after.Failed, after.Error, test.expect.failed)
			}
			newRows := normalizeRows(t, after.Rows)
			if len(newRows) != len(test.expect.rows) {
				t.Fatalf("row count: %d, expect %d", len(newRows), len(test.expect.rows))
			}
			for _, row := range newRows {
				id := row["id"].(string)
				wanted, ok := test.expect.rows[id]
				if !ok {
					t.Fatalf("unexpected row %s", id)
				}
				for field, expected := range wanted {
					if !reflect.DeepEqual(row[field], expected) {
						t.Errorf("%s.%s=%v, expect %v", id, field, row[field], expected)
					}
				}
			}
			if test.input.body != "" && !test.expect.failed && test.input.method != "DELETE" {
				var requested struct {
					Data []struct {
						ID string `json:"id"`
					} `json:"data"`
				}
				must(t, json.Unmarshal([]byte(test.input.body), &requested))
				var returned []struct {
					ID string `json:"id"`
				}
				must(t, json.Unmarshal(after.Output, &returned))
				if len(returned) != len(requested.Data) {
					t.Fatalf("writer returned %d rows for %d inputs", len(returned), len(requested.Data))
				}
				for i, row := range returned {
					if row.ID != requested.Data[i].ID {
						t.Fatalf("response order at %d: got %s, want %s", i, row.ID, requested.Data[i].ID)
					}
				}

			}
		})
	}
}

func TestGoalRequiredBoundPresenceAndPrivateExposure(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	db, _ := goalFixture(t, filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file)))))
	rt, key := goalRuntime(t, db, true)
	if rt.ExposesComponent(key) || len(rt.Routes()) != 0 {
		t.Fatal("private persistence component became public")
	}
	target := dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/goal/{conversationId}"}}
	_, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: target, Input: &read.GoalInput{ConversationID: "c1"}})
	if err == nil {
		t.Fatal("unmarked required bound input was accepted")
	}
	input := &read.GoalInput{}
	input.SetConversationID("c1")
	value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: target, Input: input})
	must(t, err)
	if len(value.(*read.GoalOutput).Data) != 1 {
		t.Fatal("marked required bound input failed")
	}
}

func goalFixture(t *testing.T, project string) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agently.db")
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on")
	must(t, err)
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	ddl, err := os.ReadFile(filepath.Join(project, "tools/schema/schema.sql"))
	must(t, err)
	_, err = db.Exec(string(ddl))
	must(t, err)
	_, err = db.Exec(`INSERT INTO conversation(id) VALUES ('c1'),('c2'),('c3');
INSERT INTO goal(id,conversation_id,objective,status,status_reason,pause_reason,controller_spec,token_budget,tokens_used,time_used_seconds,autonomous_turns_used,consecutive_no_progress,last_continuation_fingerprint,created_at,updated_at)
VALUES ('g1','c1','original','active','why','pause','controller',100,17,9,3,2,'fingerprint','2026-01-01 00:00:00','2026-01-01 00:00:00');
CREATE TRIGGER reject_goal BEFORE INSERT ON goal WHEN NEW.objective='database-reject' BEGIN SELECT RAISE(ABORT,'fixture rejection'); END;`)
	must(t, err)
	return db, path
}

// Test-only bootstrap resolves the transcribed holder and contracts verbatim.
// Production selection uses internal/datlylink and the stock Datly bootstrap.
func goalRuntime(t *testing.T, db *sql.DB, private bool) (*druntime.Runtime, spec.Key) {
	t.Helper()
	resources := resource.New()
	must(t, resources.Register(read.ReaderDatlyResourceNamespace, read.ReaderDatlyResources))
	must(t, resources.Register(write.WriterDatlyResourceNamespace, write.WriterDatlyResources))
	build := func(holder, input, output reflect.Type) *bootstrap.Artifact {
		field, _ := holder.FieldByName("Contract")
		tag, present, err := dtag.ParseComponent(field.Tag)
		must(t, err)
		if !present {
			t.Fatal("missing transcribed component tag")
		}
		source := &bootstrap.RouteSource{HolderType: holder.Name(), FieldName: field.Name, PackageName: filepath.Base(holder.PkgPath()), PackagePath: holder.PkgPath(), Tag: tag, InputType: input.Name(), OutputType: output.Name()}
		component, err := source.Resolve(input, output)
		must(t, err)
		component.Routes[0].Internal = private // Public only inside this HTTP-binding fixture.
		artifact, err := bootstrap.BuildArtifact(bootstrap.ArtifactInput{Component: component, InputType: input, OutputType: output, Resources: resources})
		must(t, err)
		return artifact
	}
	ra := build(reflect.TypeFor[read.ReaderComponent](), reflect.TypeFor[read.GoalInput](), reflect.TypeFor[read.GoalOutput]())
	wa := build(reflect.TypeFor[write.WriterComponent](), reflect.TypeFor[write.Input](), reflect.TypeFor[write.Output]())
	reader, err := ra.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	views, err := viewprovider.New(viewprovider.Config{Dependencies: wa.ViewDependencies, Input: wa.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	handler, err := writer.New(wa.Component, reflect.TypeFor[write.Input](), reflect.TypeFor[write.Output](), "patch")
	must(t, err)
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{
		{Component: ra.Component, Input: ra.Input, Output: ra.Output, OutputType: reflect.TypeFor[read.GoalOutput](), Reader: reader},
		{Component: wa.Component, Input: wa.Input, Output: wa.Output, OutputType: reflect.TypeFor[write.Output](), Handler: handler, Providers: []locator.Provider{views}, DataSource: dml.Source{DB: db}},
	}, druntime.WithResources(resources))
	must(t, err)
	return rt, ra.Component.Key
}

func normalizeRows(t *testing.T, rows []json.RawMessage) []map[string]any {
	result := normalizeRowsInOrder(t, rows)
	sort.Slice(result, func(i, j int) bool { return result[i]["id"].(string) < result[j]["id"].(string) })
	return result
}
func normalizeRowsInOrder(t *testing.T, rows []json.RawMessage) []map[string]any {
	result := make([]map[string]any, 0, len(rows))
	for _, raw := range rows {
		var value map[string]any
		must(t, json.Unmarshal(raw, &value))
		row := map[string]any{}
		for field, value := range value {
			key := strings.ToLower(field)
			if key == "createdat" || key == "updatedat" {
				if text, ok := value.(string); ok {
					parsed, err := time.Parse(time.RFC3339Nano, text)
					must(t, err)
					if parsed.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
						value = "2026-01-01T00:00:00Z"
					} else {
						value = "<generated timestamp>"
					}
				}
			}
			row[key] = value
		}
		result = append(result, row)
	}
	return result
}
func pretty(value any) string { result, _ := json.Marshal(value); return string(result) }
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(fmt.Errorf("fixture/runtime: %w", err))
	}
}
