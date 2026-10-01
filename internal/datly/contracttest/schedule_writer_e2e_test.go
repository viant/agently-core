package tests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"github.com/google/uuid"
	schedule "github.com/viant/agently-core/internal/datly/schedule/read"
	write "github.com/viant/agently-core/internal/datly/schedule/write"
	"github.com/viant/bindly/locator"
	requestprovider "github.com/viant/bindly/provider/request"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	dexec "github.com/viant/datly/exec"
	druntime "github.com/viant/datly/runtime"
	provider "github.com/viant/datly/runtime/handler/provider"
	writer "github.com/viant/datly/runtime/handler/writer"
	"github.com/viant/datly/runtime/registry"
	"github.com/viant/datly/spec"
	dsql "github.com/viant/datly/sql"
	"github.com/viant/datly/sql/dml"
	viewprovider "github.com/viant/datly/sql/reader/provider"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestScheduleWriterLegacyV1Parity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type input struct{ subject, body string }
	type expect struct {
		failed bool
		field  string
		value  any
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"nil internal flag retains insertion rejection", input{body: `{"data":[{"id":"new","name":"new","agentRef":"fixture"}]}`}, expect{failed: true}},
		{"anonymous insertion defaults public adhoc UTC", input{body: `{"data":[{"id":"new","name":"new","agentRef":"fixture","internal":false}]}`}, expect{field: "visibility", value: "public"}},
		{"authenticated insertion defaults private ownership", input{subject: "u1", body: `{"data":[{"id":"new","name":"new","agentRef":"fixture","internal":false}]}`}, expect{field: "createdbyuserid", value: "u1"}},
		{"anonymous private request becomes public", input{body: `{"data":[{"id":"new","name":"new","agentRef":"fixture","internal":false,"visibility":"private"}]}`}, expect{field: "visibility", value: "public"}},
		{"explicit public remains public", input{subject: "u1", body: `{"data":[{"id":"new","name":"new","agentRef":"fixture","internal":false,"visibility":"public"}]}`}, expect{field: "visibility", value: "public"}},
		{"server allocates UUID before validation", input{subject: "u1", body: `{"data":[{"name":"new","agentRef":"fixture","internal":false}]}`}, expect{field: "id", value: "<generated id>"}},
		{"supplied creation timestamp remains authoritative", input{body: `{"data":[{"id":"new","name":"new","agentRef":"fixture","internal":false,"createdAt":"2026-01-01T00:00:00Z"}]}`}, expect{field: "createdat", value: "2026-01-01T00:00:00Z"}},
		{"missing insertion name fails without mutation", input{body: `{"data":[{"id":"new","agentRef":"fixture","internal":false}]}`}, expect{failed: true}},
		{"sparse description update preserves next run", input{body: `{"data":[{"id":"owned","description":"changed"}]}`}, expect{field: "nextrunat", value: "2027-01-01T00:00:00Z"}},
		{"schedule timezone change clears next run", input{body: `{"data":[{"id":"owned","timezone":"America/Los_Angeles"}]}`}, expect{field: "nextrunat", value: nil}},
		{"equivalent timezone retains next run", input{body: `{"data":[{"id":"owned","timezone":"UTC"}]}`}, expect{field: "nextrunat", value: "2027-01-01T00:00:00Z"}},
		{"changed interval clears next run", input{body: `{"data":[{"id":"owned","intervalSeconds":30}]}`}, expect{field: "nextrunat", value: nil}},
		{"explicit zero interval counts as a change", input{body: `{"data":[{"id":"owned","intervalSeconds":0}]}`}, expect{field: "nextrunat", value: nil}},
		{"explicit null interval counts as a change", input{body: `{"data":[{"id":"owned","intervalSeconds":null}]}`}, expect{field: "nextrunat", value: nil}},
		{"reenabling clears next run", input{body: `{"data":[{"id":"owned","enabled":true}]}`}, expect{field: "nextrunat", value: nil}},
		{"remaining disabled retains next run", input{body: `{"data":[{"id":"owned","enabled":false}]}`}, expect{field: "nextrunat", value: "2027-01-01T00:00:00Z"}},
		{"caller supplied update time stays authoritative", input{body: `{"data":[{"id":"owned","updatedAt":"2026-01-01T00:00:00Z"}]}`}, expect{field: "updatedat", value: "2026-01-01T00:00:00Z"}},
		{"changed start date clears next run", input{body: `{"data":[{"id":"owned","startAt":"2026-01-01T00:00:00Z"}]}`}, expect{field: "nextrunat", value: nil}},
		{"changed end date clears next run", input{body: `{"data":[{"id":"owned","endAt":"2028-01-01T00:00:00Z"}]}`}, expect{field: "nextrunat", value: nil}},
		{"changed cron clears next run", input{body: `{"data":[{"id":"owned","cronExpr":"* * * * *"}]}`}, expect{field: "nextrunat", value: nil}},
		{"changed schedule type clears next run", input{body: `{"data":[{"id":"owned","scheduleType":"interval"}]}`}, expect{field: "nextrunat", value: nil}},
		{"equivalent interval preserves next run", input{body: `{"data":[{"id":"owned","intervalSeconds":60}]}`}, expect{field: "nextrunat", value: "2027-01-01T00:00:00Z"}},
		{"authenticated update backfills absent legacy owner", input{subject: "u1", body: `{"data":[{"id":"unowned","description":"changed"}]}`}, expect{field: "createdbyuserid", value: "u1"}},
		{"existing owner remains authoritative", input{subject: "u2", body: `{"data":[{"id":"owned","description":"changed"}]}`}, expect{field: "createdbyuserid", value: "u1"}},
		{"empty batch is no-op", input{body: `{"data":[]}`}, expect{}},
		{"null body is no-op", input{body: `{"data":null}`}, expect{}},
		{"unique name rejects whole batch", input{body: `{"data":[{"id":"owned","description":"changed"},{"id":"new","name":"public","agentRef":"fixture","internal":false}]}`}, expect{failed: true}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, oldPath := scheduleWriterFixture(t, project)
			db, _ := scheduleWriterFixture(t, project)
			encoded, err := json.Marshal(map[string]any{"Component": "schedule", "DBPath": oldPath, "Principal": tc.input.subject, "Body": tc.input.body, "Filters": map[string]any{"mode": "due"}})
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
			rt, key := scheduleWriterRuntime(t, db, tc.input.subject)
			req := httptest.NewRequest("PATCH", "/v1/api/agently/scheduler/", strings.NewReader(tc.input.body))
			req.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(req)
			must(t, err)
			defer scope.Close()
			result, mutationErr := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/scheduler/", scope)
			if before.Failed != tc.expect.failed || (mutationErr != nil) != tc.expect.failed {
				t.Fatalf("legacy failure=%v (%s), new=%v, expected=%v", before.Failed, before.Error, mutationErr, tc.expect.failed)
			}
			resultRead, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/scheduler/schedule/{id}"}}, Input: &schedule.ScheduleInput{}})
			must(t, err)
			raw, err = json.Marshal(resultRead.(*schedule.ScheduleOutput).Data)
			must(t, err)
			var rows []json.RawMessage
			must(t, json.Unmarshal(raw, &rows))
			normalize := func(rows []json.RawMessage) []map[string]any {
				values := normalizeRows(t, rows)
				for _, row := range values {
					if row["name"] == "new" && strings.Contains(tc.desc, "UUID") {
						if _, err := uuid.Parse(row["id"].(string)); err != nil {
							t.Fatalf("invalid allocated id: %v", err)
						}
						row["id"] = "<generated id>"
					}
				}
				return values
			}
			oldRows, newRows := normalize(before.Rows), normalize(rows)
			if !reflect.DeepEqual(oldRows, newRows) {
				t.Fatalf("stored parity\nlegacy=%s\nnew=%s", pretty(oldRows), pretty(newRows))
			}
			if tc.expect.field != "" {
				name := "owned"
				if strings.Contains(tc.input.body, `"id":"unowned"`) {
					name = "unowned"
				}
				if strings.Contains(tc.input.body, `"name":"new"`) {
					name = "new"
				}
				found := false
				for _, row := range newRows {
					if row["name"] == name {
						found = true
						if !reflect.DeepEqual(row[tc.expect.field], tc.expect.value) {
							t.Fatalf("%s.%s=%v expected=%v", name, tc.expect.field, row[tc.expect.field], tc.expect.value)
						}
					}
				}
				if !found {
					t.Fatalf("expected row %s missing", name)
				}
			}
			if !tc.expect.failed {
				var oldOutput []json.RawMessage
				must(t, json.Unmarshal(before.Output, &oldOutput))
				raw, err = json.Marshal(result.(*write.Output).Data)
				must(t, err)
				var newOutput []json.RawMessage
				must(t, json.Unmarshal(raw, &newOutput))
				if !reflect.DeepEqual(normalize(oldOutput), normalize(newOutput)) {
					t.Fatalf("response parity\nlegacy=%s\nnew=%s", before.Output, raw)
				}
			}
		})
	}
}

func scheduleWriterFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := scheduleReaderFixture(t, project)
	_, err := db.Exec(`UPDATE schedule SET next_run_at='2027-01-01 00:00:00',interval_seconds=60,enabled=0 WHERE id='owned'; INSERT INTO schedule(id,name,agent_ref,visibility,created_at) VALUES('unowned','unowned','fixture','private','2026-01-01 00:00:00')`)
	must(t, err)
	return db, path
}

func scheduleWriterRuntime(t *testing.T, db *sql.DB, subject string, supplied ...*sql.Tx) (*druntime.Runtime, spec.Key) {
	return scheduleWriterRuntimeWithViewHook(t, db, subject, nil, supplied...)
}
func scheduleWriterRuntimeWithViewHook(t *testing.T, db *sql.DB, subject string, wrap func(locator.Provider) locator.Provider, supplied ...*sql.Tx) (*druntime.Runtime, spec.Key) {
	resources := resource.New()
	must(t, resources.Register(schedule.ReaderDatlyResourceNamespace, schedule.ReaderDatlyResources))
	must(t, resources.Register(write.WriterDatlyResourceNamespace, write.WriterDatlyResources))
	ra := payloadArtifact(t, resources, reflect.TypeFor[schedule.ReaderComponent](), reflect.TypeFor[schedule.ScheduleInput](), reflect.TypeFor[schedule.ScheduleOutput]())
	wa := payloadArtifact(t, resources, reflect.TypeFor[write.WriterComponent](), reflect.TypeFor[write.Input](), reflect.TypeFor[write.Output]())
	reader, err := ra.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	views, err := viewprovider.New(viewprovider.Config{Dependencies: wa.ViewDependencies, Input: wa.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	if wrap != nil {
		views = wrap(views)
	}
	h, err := writer.New(wa.Component, reflect.TypeFor[write.Input](), reflect.TypeFor[write.Output](), "patch")
	must(t, err)
	visibility := provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &subject, true, nil })
	var tx *sql.Tx
	if len(supplied) > 0 {
		tx = supplied[0]
	}
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{
		{Component: ra.Component, Input: ra.Input, Output: ra.Output, OutputType: reflect.TypeFor[schedule.ScheduleOutput](), Reader: reader, Providers: []locator.Provider{visibility, ordinaryAccess("scheduleaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "internal" {
				return true, true, nil
			}
			return nil, false, nil
		})}},
		{Component: wa.Component, Input: wa.Input, Output: wa.Output, OutputType: reflect.TypeFor[write.Output](), Handler: h, Providers: []locator.Provider{views, visibility}, DataSource: dml.Source{DB: db, Tx: tx}},
	}, druntime.WithResources(resources))
	must(t, err)
	return rt, ra.Component.Key
}

func TestScheduleWriterCallerTransactionOwnership(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type useCase struct {
		desc   string
		input  bool
		expect string
	}
	for _, tc := range []useCase{{"caller rollback", false, ""}, {"caller commit", true, "changed"}} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := scheduleWriterFixture(t, project)
			db.SetMaxOpenConns(1)
			tx, err := db.BeginTx(context.Background(), nil)
			must(t, err)
			defer tx.Rollback()
			rt, _ := scheduleWriterRuntime(t, db, "u1", tx)
			req := httptest.NewRequest("PATCH", "/v1/api/agently/scheduler/", strings.NewReader(`{"data":[{"id":"owned","description":"changed"}]}`))
			req.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(req)
			must(t, err)
			defer scope.Close()
			_, err = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/scheduler/", scope)
			must(t, err)
			var pending string
			must(t, tx.QueryRow("SELECT description FROM schedule WHERE id='owned'").Scan(&pending))
			if pending != "changed" {
				t.Fatalf("pending description=%q", pending)
			}
			if tc.input {
				must(t, tx.Commit())
			} else {
				must(t, tx.Rollback())
			}
			var stored sql.NullString
			must(t, db.QueryRow("SELECT description FROM schedule WHERE id='owned'").Scan(&stored))
			if stored.String != tc.expect {
				t.Fatalf("stored description=%q expected=%q", stored.String, tc.expect)
			}
		})
	}
}
