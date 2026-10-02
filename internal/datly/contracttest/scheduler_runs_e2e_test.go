package tests

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	read "github.com/viant/agently-core/internal/datly/run/read"
	"github.com/viant/bindly/locator"
	requestprovider "github.com/viant/bindly/provider/request"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	dexec "github.com/viant/datly/exec"
	druntime "github.com/viant/datly/runtime"
	provider "github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/runtime/registry"
	"github.com/viant/datly/spec"
	dsql "github.com/viant/datly/sql"
	"github.com/viant/xdatly/state"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestSchedulerRunsLegacyV1Parity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		Subject, Mode string
		Filters       map[string]any
	}
	type useCase struct {
		Desc   string
		Input  input
		Expect []string
	}
	var cases []useCase
	must(t, json.Unmarshal([]byte(`[
 {"desc":"public schedule is visible anonymously","input":{"subject":"","mode":"schedulerRuns","filters":{"scheduleId":"pub"}},"expect":["pub-new","pub-old"]},
 {"desc":"owner sees private schedule runs","input":{"subject":"u1","mode":"schedulerRuns","filters":{"scheduleId":"own"}},"expect":["own"]},
 {"desc":"other owner cannot see private schedule runs","input":{"subject":"u2","mode":"schedulerRuns","filters":{"scheduleId":"own"}},"expect":[]},
 {"desc":"trusted due lookup includes private schedule","input":{"subject":"","mode":"schedulerDue","filters":{"scheduleId":"own"}},"expect":["own"]},
 {"desc":"trusted due lookup includes internal schedule","input":{"subject":"","mode":"schedulerDue","filters":{"scheduleId":"internal"}},"expect":["internal"]},
 {"desc":"excluded status removes terminal run","input":{"subject":"u1","mode":"schedulerRuns","filters":{"scheduleId":"pub","excludeStatuses":["failed"]}},"expect":["pub-new"]},
 {"desc":"scheduled slot selects exact run","input":{"subject":"u1","mode":"schedulerRuns","filters":{"scheduleId":"pub","scheduledFor":"2026-01-02T00:00:00Z"}},"expect":["pub-new"]},
 {"desc":"unknown schedule returns empty","input":{"subject":"u1","mode":"schedulerRuns","filters":{"scheduleId":"missing"}},"expect":[]}
]
`), &cases))
	for _, tc := range cases {
		t.Run(tc.Desc, func(t *testing.T) {
			db, _ := schedulerRunsFixture(t, project)
			if slot, ok := tc.Input.Filters["scheduledFor"]; ok {
				// Use each writer path's real timestamp representation for a matching slot.
				seedBody, err := json.Marshal(map[string]any{"data": []map[string]any{{"id": "pub-new", "scheduledFor": slot}}})
				must(t, err)
				rt, _ := runParityRuntime(t, db, "", true)
				request := httptest.NewRequest("PATCH", "/v1/api/agently/run", strings.NewReader(string(seedBody)))
				request.Header.Set("Content-Type", "application/json")
				scope, err := requestprovider.New(request)
				must(t, err)
				_, err = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/run", scope)
				must(t, err)
				must(t, scope.Close())
			}
			rt, key := schedulerRunsRuntime(t, db, tc.Input.Subject, tc.Input.Mode)
			input := &read.RunRowsInput{Has: &read.RunRowsInputHas{}}
			for _, field := range []struct {
				name    string
				value   *string
				present *bool
			}{{"scheduleId", &input.ScheduleId, &input.Has.ScheduleId}} {
				if value, ok := tc.Input.Filters[field.name]; ok {
					encoded, err := json.Marshal(value)
					must(t, err)
					must(t, json.Unmarshal(encoded, field.value))
					*field.present = true
				}
			}
			if value, ok := tc.Input.Filters["excludeStatuses"]; ok {
				encoded, err := json.Marshal(value)
				must(t, err)
				must(t, json.Unmarshal(encoded, &input.ExcludeStatuses))
				input.Has.ExcludeStatuses = true
			}
			if value, ok := tc.Input.Filters["scheduledFor"]; ok {
				encoded, err := json.Marshal(value)
				must(t, err)
				must(t, json.Unmarshal(encoded, &input.ScheduledFor))
				input.Has.ScheduledFor = true
			}
			value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/run/{id}"}}, Input: input})
			must(t, err)
			raw, err := json.Marshal(value.(*read.RunRowsOutput).Data)
			must(t, err)
			var rows []json.RawMessage
			must(t, json.Unmarshal(raw, &rows))
			lower := func(rows []json.RawMessage) []map[string]any {
				result := []map[string]any{}
				for _, row := range rows {
					var fields map[string]any
					must(t, json.Unmarshal(row, &fields))
					mapped := map[string]any{}
					for key, value := range fields {
						mapped[strings.ToLower(key)] = value
					}
					result = append(result, mapped)
				}
				return result
			}
			newRows := lower(rows)
			// Compare the selected scheduler projection and ensure unselected fields are zero.
			for _, row := range value.(*read.RunRowsOutput).Data {
				if row.WorkerId != nil || row.AuthAuthority != nil || row.CheckpointData != nil {
					t.Fatal("scheduler list fetched an unselected runtime field")
				}
			}
			ids := []string{}
			for _, row := range newRows {
				ids = append(ids, row["id"].(string))
			}
			if !reflect.DeepEqual(ids, tc.Expect) {
				t.Fatalf("IDs=%v expected=%v", ids, tc.Expect)
			}
		})
	}
}

func schedulerRunsFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := goalFixture(t, project)
	_, err := db.Exec(`INSERT INTO schedule(id,name,agent_ref,visibility,created_by_user_id,internal,created_at) VALUES
 ('pub','pub','fixture','public',NULL,0,'2026-01-01 00:00:00'),('own','own','fixture','private','u1',0,'2026-01-01 00:00:00'),('other','other','fixture','private','u2',0,'2026-01-01 00:00:00'),('internal','internal','fixture','public','u1',1,'2026-01-01 00:00:00');
 INSERT INTO run(id,schedule_id,conversation_id,status,error_message,worker_id,auth_authority,checkpoint_data,started_at,created_at) VALUES
 ('pub-old','pub','c1','failed','timeout retry','secret-worker','fixture-auth','fixture-checkpoint','2026-01-01 00:00:00','2026-01-01 00:00:00'),
 ('pub-new','pub','c2','running',NULL,NULL,NULL,NULL,'2026-01-02 00:00:00','2026-01-01 00:00:00'),
 ('own','own','c1','running',NULL,NULL,NULL,NULL,'2026-01-03 00:00:00','2026-01-01 00:00:00'),
 ('other','other',NULL,'succeeded',NULL,NULL,NULL,NULL,'2026-01-04 00:00:00','2026-01-01 00:00:00'),
 ('internal','internal',NULL,'running',NULL,NULL,NULL,NULL,'2026-01-05 00:00:00','2026-01-01 00:00:00'),
 ('interactive',NULL,NULL,'running',NULL,NULL,NULL,NULL,'2026-01-06 00:00:00','2026-01-01 00:00:00')`)
	must(t, err)
	_, err = db.Exec("UPDATE run SET scheduled_for='2026-01-02 00:00:00' WHERE id='pub-new'")
	must(t, err)
	return db, path
}

func schedulerRunsRuntime(t *testing.T, db *sql.DB, subject, mode string) (*druntime.Runtime, spec.Key) {
	resources := resource.New()
	must(t, resources.Register(read.ReaderDatlyResourceNamespace, read.ReaderDatlyResources))
	artifact := payloadArtifact(t, resources, reflect.TypeFor[read.ReaderComponent](), reflect.TypeFor[read.RunRowsInput](), reflect.TypeFor[read.RunRowsOutput]())
	reader, err := artifact.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	limit, offset := 10000, 0
	fields := []string{"completed_at", "conversation_id", "conversation_kind", "created_at", "error_message", "id", "lease_owner", "lease_until", "precondition_passed", "precondition_ran_at", "precondition_result", "schedule_id", "scheduled_for", "started_at", "status", "updated_at"}
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{{Component: artifact.Component, Input: artifact.Input, Output: artifact.Output, OutputType: reflect.TypeFor[read.RunRowsOutput](), Reader: reader, Providers: []locator.Provider{ordinaryAccess("runaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		if name == "mode" {
			return mode, true, nil
		}
		return true, true, nil
	}), provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &subject, true, nil }), queryselectors.Provider(state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Fields: fields, Limit: limit, Offset: offset}}})}}}, druntime.WithResources(resources))
	must(t, err)
	return rt, artifact.Component.Key
}

func TestSchedulerRunsAnonymousPrivateScope(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	db, _ := schedulerRunsFixture(t, project)
	rt, key := schedulerRunsRuntime(t, db, "", "schedulerRuns")
	input := &read.RunRowsInput{}
	input.SetScheduleId("own")
	value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/run/{id}"}}, Input: input})
	must(t, err)
	if len(value.(*read.RunRowsOutput).Data) != 0 {
		t.Fatal("anonymous public lookup exposed a private schedule")
	}
}

func TestSchedulerRunsSinceTurnCorrection(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	db, _ := schedulerRunsFixture(t, project)
	_, err := db.Exec(`INSERT INTO turn(id,conversation_id,status,created_at) VALUES('anchor','c1','succeeded','2026-01-03 00:00:00');UPDATE run SET created_at='2026-01-04 00:00:00' WHERE id='pub-new'`)
	must(t, err)
	rt, key := schedulerRunsRuntime(t, db, "u1", "schedulerRuns")
	input := &read.RunRowsInput{}
	input.SetScheduleId("pub")
	input.SetSinceTurn("anchor")
	value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/run/{id}"}}, Input: input})
	must(t, err)
	rows := value.(*read.RunRowsOutput).Data
	if len(rows) != 1 || rows[0].Id != "pub-new" {
		t.Fatal("canonical since filter did not exclude the earlier run")
	}
}
