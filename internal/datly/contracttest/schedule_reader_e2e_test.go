package tests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	schedule "github.com/viant/agently-core/internal/datly/schedule/read"
	"github.com/viant/bindly/locator"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	dexec "github.com/viant/datly/exec"
	druntime "github.com/viant/datly/runtime"
	provider "github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/runtime/registry"
	"github.com/viant/datly/spec"
	dsql "github.com/viant/datly/sql"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestScheduleReaderLegacyV1Parity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type input struct{ subject, mode, id string }
	type useCase struct {
		desc   string
		input  input
		expect []string
	}
	for _, tc := range []useCase{
		{"anonymous lists public only", input{mode: "list"}, []string{"public"}},
		{"owner lists own private and public", input{subject: "u1", mode: "list"}, []string{"owned", "public"}},
		{"other owner has separate private scope", input{subject: "u2", mode: "list"}, []string{"other", "public"}},
		{"trusted scheduler sees private and internal schedules", input{mode: "due"}, []string{"internal", "other", "owned", "public"}},
		{"get cannot cross private owner", input{subject: "u2", id: "owned"}, []string{}},
		{"owner can get their private schedule", input{subject: "u1", id: "owned"}, []string{"owned"}},
		{"internal stays hidden even to owner", input{subject: "u1", id: "internal"}, []string{}},
		{"unknown schedule returns empty", input{subject: "u1", id: "missing"}, []string{}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, oldPath := scheduleReaderFixture(t, project)
			db, _ := scheduleReaderFixture(t, project)
			filters := map[string]any{"mode": tc.input.mode}
			if tc.input.id != "" {
				filters["id"] = tc.input.id
			}
			encoded, err := json.Marshal(map[string]any{"Component": "schedule", "DBPath": oldPath, "Principal": tc.input.subject, "Filters": filters})
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
			if before.Failed {
				t.Fatalf("legacy read failed: %s", before.Error)
			}
			rt, key := scheduleReaderRuntime(t, db, tc.input.subject, tc.input.mode == "due", true, true)
			in := &schedule.ScheduleInput{Has: &schedule.ScheduleInputHas{}}
			if tc.input.id != "" {
				in.Id = tc.input.id
				in.Has.Id = true
			}
			result, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/scheduler/schedule/{id}"}}, Input: in})
			must(t, err)
			raw, err = json.Marshal(result.(*schedule.ScheduleOutput).Data)
			must(t, err)
			var rows []json.RawMessage
			must(t, json.Unmarshal(raw, &rows))
			oldRows, newRows := normalizeRows(t, before.Rows), normalizeRows(t, rows)
			if !reflect.DeepEqual(oldRows, newRows) {
				t.Fatalf("full schedule parity\nlegacy=%s\nnew=%s", pretty(oldRows), pretty(newRows))
			}
			ids := []string{}
			for _, row := range newRows {
				ids = append(ids, row["id"].(string))
			}
			if !reflect.DeepEqual(ids, tc.expect) {
				t.Fatalf("identities=%v expected=%v", ids, tc.expect)
			}
		})
	}
}

func TestScheduleReaderRequiresTrustedScope(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type useCase struct {
		desc   string
		input  [2]bool
		expect bool
	}
	for _, tc := range []useCase{{"missing visibility subject", [2]bool{false, true}, true}, {"missing host scheduler mode", [2]bool{true, false}, true}} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := scheduleReaderFixture(t, project)
			rt, key := scheduleReaderRuntime(t, db, "", false, tc.input[0], tc.input[1])
			_, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/scheduler/schedule/{id}"}}, Input: &schedule.ScheduleInput{}})
			if (err != nil) != tc.expect {
				t.Fatalf("error=%v expected failure=%v", err, tc.expect)
			}
		})
	}
}

func scheduleReaderFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := goalFixture(t, project)
	_, err := db.Exec(`INSERT INTO schedule(id,name,agent_ref,visibility,created_by_user_id,internal,created_at) VALUES
 ('public','public','fixture','public','other',0,'2026-01-01 00:00:00'),
 ('owned','owned','fixture','private','u1',0,'2026-01-01 00:00:00'),
 ('other','other','fixture','private','u2',0,'2026-01-01 00:00:00'),
 ('internal','internal','fixture','public','u1',1,'2026-01-01 00:00:00')`)
	must(t, err)
	return db, path
}

func scheduleReaderRuntime(t *testing.T, db *sql.DB, subject string, internal, visibilityProvided, modeProvided bool) (*druntime.Runtime, spec.Key) {
	resources := resource.New()
	must(t, resources.Register(schedule.ReaderDatlyResourceNamespace, schedule.ReaderDatlyResources))
	artifact := payloadArtifact(t, resources, reflect.TypeFor[schedule.ReaderComponent](), reflect.TypeFor[schedule.ScheduleInput](), reflect.TypeFor[schedule.ScheduleOutput]())
	reader, err := artifact.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	var providers []locator.Provider
	if visibilityProvided {
		providers = append(providers, provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &subject, true, nil }))
	}
	if modeProvided {
		providers = append(providers, ordinaryAccess("scheduleaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "internal" {
				return internal, true, nil
			}
			return nil, false, nil
		}))
	}
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{{Component: artifact.Component, Input: artifact.Input, Output: artifact.Output, OutputType: reflect.TypeFor[schedule.ScheduleOutput](), Reader: reader, Providers: providers}}, druntime.WithResources(resources))
	must(t, err)
	return rt, artifact.Component.Key
}
