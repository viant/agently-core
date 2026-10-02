package tests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	read "github.com/viant/agently-core/internal/datly/run/read"
	"github.com/viant/bindly/locator"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	dexec "github.com/viant/datly/exec"
	druntime "github.com/viant/datly/runtime"
	provider "github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/runtime/registry"
	"github.com/viant/datly/spec"
	dsql "github.com/viant/datly/sql"
	"github.com/viant/xdatly/state"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestSchedulerRunListLegacyV1Parity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type input struct {
		subject string
		filters map[string]any
	}
	type useCase struct {
		desc   string
		input  input
		expect []string
	}
	for _, tc := range []useCase{
		{"anonymous sees public schedule runs", input{}, []string{"pub-new", "pub-old"}},
		{"owner sees public and own private schedule", input{subject: "u1"}, []string{"own", "pub-new", "pub-old"}},
		{"other owner has separate schedule visibility", input{subject: "u2"}, []string{"other", "pub-new", "pub-old"}},
		{"schedule predicate keeps visibility", input{subject: "u1", filters: map[string]any{"scheduleId": "own"}}, []string{"own"}},
		{"status pattern is case insensitive", input{subject: "u1", filters: map[string]any{"status": "RUN%"}}, []string{"own", "pub-new"}},
		{"conversation pattern", input{subject: "u1", filters: map[string]any{"conversationId": "c%"}}, []string{"own", "pub-new", "pub-old"}},
		{"error wildcard filter", input{subject: "u1", filters: map[string]any{"errorMessage": "%timeout%"}}, []string{"pub-old"}},
		{"limit and offset preserve order", input{subject: "u1", filters: map[string]any{"limit": 1, "offset": 1}}, []string{"pub-new"}},
		{"internal schedules stay excluded", input{subject: "u1", filters: map[string]any{"scheduleId": "internal"}}, []string{}},
		{"missing schedule is empty", input{subject: "u1", filters: map[string]any{"scheduleId": "missing"}}, []string{}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, oldPath := schedulerRunListFixture(t, project)
			db, _ := schedulerRunListFixture(t, project)
			payload, err := json.Marshal(map[string]any{"Component": "schedulerRunList", "DBPath": oldPath, "Principal": tc.input.subject, "Filters": tc.input.filters})
			must(t, err)
			process := exec.Command(legacy)
			process.Stdin = bytes.NewReader(payload)
			var stderr bytes.Buffer
			process.Stderr = &stderr
			raw, err := process.Output()
			if err != nil {
				t.Fatalf("legacy: %v\n%s", err, stderr.String())
			}
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			if before.Failed {
				t.Fatalf("legacy failed: %s", before.Error)
			}
			rt, key := schedulerRunListRuntime(t, db, tc.input.subject, tc.input.filters)
			input := &read.RunRowsInput{Has: &read.RunRowsInputHas{}}
			for _, field := range []struct {
				name    string
				value   *string
				present *bool
			}{{"scheduleId", &input.ScheduleId, &input.Has.ScheduleId}, {"status", &input.StatusPattern, &input.Has.StatusPattern}, {"conversationId", &input.ConversationPattern, &input.Has.ConversationPattern}, {"errorMessage", &input.ErrorPattern, &input.Has.ErrorPattern}} {
				if value, ok := tc.input.filters[field.name]; ok {
					encoded, err := json.Marshal(value)
					must(t, err)
					must(t, json.Unmarshal(encoded, field.value))
					*field.present = true
				}
			}
			value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/run/{id}"}}, Input: input})
			must(t, err)
			raw, err = json.Marshal(value.(*read.RunRowsOutput).Data)
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
			oldRows, newRows := lower(before.Rows), lower(rows)
			// Compare the selected scheduler projection and ensure unselected fields are zero.
			for _, row := range value.(*read.RunRowsOutput).Data {
				if row.WorkerId != nil || row.AuthAuthority != nil || row.CheckpointData != nil {
					t.Fatal("scheduler list fetched an unselected runtime field")
				}
			}
			for i, row := range newRows {
				if i < len(oldRows) {
					for key := range row {
						if _, ok := oldRows[i][key]; !ok {
							delete(row, key)
						}
					}
				}
			}
			if !reflect.DeepEqual(oldRows, newRows) {
				t.Fatalf("full selected projection/order\nlegacy=%s\nnew=%s", pretty(oldRows), pretty(newRows))
			}
			ids := []string{}
			for _, row := range newRows {
				ids = append(ids, row["id"].(string))
			}
			if !reflect.DeepEqual(ids, tc.expect) {
				t.Fatalf("IDs=%v expected=%v", ids, tc.expect)
			}
		})
	}
}

func schedulerRunListFixture(t *testing.T, project string) (*sql.DB, string) {
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
	return db, path
}

func schedulerRunListRuntime(t *testing.T, db *sql.DB, subject string, filters map[string]any) (*druntime.Runtime, spec.Key) {
	resources := resource.New()
	must(t, resources.Register(read.ReaderDatlyResourceNamespace, read.ReaderDatlyResources))
	artifact := payloadArtifact(t, resources, reflect.TypeFor[read.ReaderComponent](), reflect.TypeFor[read.RunRowsInput](), reflect.TypeFor[read.RunRowsOutput]())
	reader, err := artifact.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	limit, offset := 100, 0
	if value, ok := filters["limit"]; ok {
		limit = value.(int)
	}
	if value, ok := filters["offset"]; ok {
		offset = value.(int)
	}
	fields := []string{"completed_at", "conversation_id", "conversation_kind", "created_at", "error_message", "id", "lease_owner", "lease_until", "precondition_passed", "precondition_ran_at", "precondition_result", "schedule_id", "scheduled_for", "started_at", "status", "updated_at"}
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{{Component: artifact.Component, Input: artifact.Input, Output: artifact.Output, OutputType: reflect.TypeFor[read.RunRowsOutput](), Reader: reader, Providers: []locator.Provider{ordinaryAccess("runaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		if name == "mode" {
			return "schedulerList", true, nil
		}
		return true, true, nil
	}), provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &subject, true, nil }), queryselectors.Provider(state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Fields: fields, Limit: limit, Offset: offset}}})}}}, druntime.WithResources(resources))
	must(t, err)
	return rt, artifact.Component.Key
}
