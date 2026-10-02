package tests

import (
	"context"
	"database/sql"
	"encoding/json"
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
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestRunVariantsLegacyV1Parity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		mode    string
		filters map[string]any
	}
	type useCase struct {
		desc   string
		input  input
		expect []string
	}
	for _, tc := range []useCase{
		{"active selects newest eligible run", input{mode: "active"}, []string{"queued"}},
		{"active conversation predicate affects newest selection", input{mode: "active", filters: map[string]any{"conversationId": "c1"}}, []string{"running"}},
		{"unknown active conversation is empty", input{mode: "active", filters: map[string]any{"conversationId": "missing"}}, []string{}},
		{"stale keeps running only in activity order", input{mode: "stale"}, []string{"recent", "scheduled", "running", "child"}},
		{"heartbeat cutoff includes null", input{mode: "stale", filters: map[string]any{"heartbeatBefore": "2026-01-02T00:00:00Z"}}, []string{"scheduled", "running", "child"}},
		{"worker host includes null", input{mode: "stale", filters: map[string]any{"workerHost": "host-a"}}, []string{"scheduled", "running", "child"}},
		{"expired leases exclude null", input{mode: "stale", filters: map[string]any{"leaseExpiredBefore": "2026-01-02T00:00:00Z"}}, []string{"scheduled", "child"}},
		{"activity lower bound uses heartbeat start creation precedence", input{mode: "stale", filters: map[string]any{"activityAfter": "2026-01-02T00:00:00Z"}}, []string{"recent"}},
		{"conversation kind filter", input{mode: "stale", filters: map[string]any{"conversationKind": "scheduled"}}, []string{"scheduled"}},
		{"root interactive excludes scheduled and child conversations", input{mode: "stale", filters: map[string]any{"rootInteractive": true}}, []string{"recent", "running"}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := runVariantFixture(t, project)
			rt, key := runVariantRuntime(t, db, tc.input.mode, true)
			input := &read.RunRowsInput{Has: &read.RunRowsInputHas{}}
			fields := reflect.ValueOf(input).Elem()
			markers := reflect.ValueOf(input.Has).Elem()
			for name, value := range tc.input.filters {
				fieldName := map[string]string{"conversationId": "ConversationId", "turnId": "TurnId", "heartbeatBefore": "HeartbeatBefore", "workerHost": "WorkerHost", "leaseExpiredBefore": "LeaseExpiredBefore", "activityAfter": "ActivityAfter", "conversationKind": "ConversationKind", "rootInteractive": "RootInteractive"}[name]
				encoded, err := json.Marshal(value)
				must(t, err)
				must(t, json.Unmarshal(encoded, fields.FieldByName(fieldName).Addr().Interface()))
				markers.FieldByName(fieldName).SetBool(true)
			}
			value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/run/{id}"}}, Input: input})
			must(t, err)
			raw, err := json.Marshal(value.(*read.RunRowsOutput).Data)
			must(t, err)
			var rows []json.RawMessage
			must(t, json.Unmarshal(raw, &rows))
			normalize := func(rows []json.RawMessage) []map[string]any {
				result := []map[string]any{}
				for _, row := range rows {
					var fields map[string]any
					must(t, json.Unmarshal(row, &fields))
					lower := map[string]any{}
					for key, value := range fields {
						lower[strings.ToLower(key)] = value
					}
					result = append(result, lower)
				}
				return result
			}
			newRows := normalize(rows)
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

func TestRunMaintenanceModeRejectsPublicScope(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	db, _ := runVariantFixture(t, filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file)))))
	rt, key := runVariantRuntime(t, db, "stale", false)
	_, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/run/{id}"}}, Input: &read.RunRowsInput{}})
	if err == nil {
		t.Fatal("public scope entered maintenance mode")
	}
}

func runVariantFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := goalFixture(t, project)
	_, err := db.Exec(`INSERT INTO conversation(id,conversation_parent_id) VALUES('child-conversation','c1');
 INSERT INTO schedule(id,name,agent_ref,created_at) VALUES('schedule','schedule','fixture','2026-01-01 00:00:00');
 INSERT INTO run(id,status,conversation_id,conversation_kind,schedule_id,worker_host,last_heartbeat_at,lease_until,created_at) VALUES
 ('running','running','c1','interactive',NULL,'host-a',NULL,NULL,'2026-01-01 00:00:00'),
 ('recent','running','c2','interactive',NULL,'host-b','2026-01-04 00:00:00','2026-01-04 00:00:00','2026-01-01 00:00:00'),
 ('queued','queued','c2','interactive',NULL,NULL,NULL,NULL,'2026-01-05 00:00:00'),
 ('done','succeeded','c1','interactive',NULL,NULL,NULL,NULL,'2026-01-06 00:00:00'),
 ('child','running','child-conversation','interactive',NULL,NULL,NULL,'2026-01-01 00:00:00','2026-01-01 00:00:00'),
 ('scheduled','running',NULL,'scheduled','schedule',NULL,NULL,'2026-01-01 00:00:00','2026-01-01 00:00:00')`)
	must(t, err)
	return db, path
}

func runVariantRuntime(t *testing.T, db *sql.DB, mode string, internal bool) (*druntime.Runtime, spec.Key) {
	resources := resource.New()
	must(t, resources.Register(read.ReaderDatlyResourceNamespace, read.ReaderDatlyResources))
	artifact := payloadArtifact(t, resources, reflect.TypeFor[read.ReaderComponent](), reflect.TypeFor[read.RunRowsInput](), reflect.TypeFor[read.RunRowsOutput]())
	reader, err := artifact.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	subject := ""
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{{Component: artifact.Component, Input: artifact.Input, Output: artifact.Output, OutputType: reflect.TypeFor[read.RunRowsOutput](), Reader: reader, Providers: []locator.Provider{ordinaryAccess("runaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		if name == "mode" {
			return mode, true, nil
		}
		return internal, true, nil
	}), provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &subject, true, nil })}}}, druntime.WithResources(resources))
	must(t, err)
	return rt, artifact.Component.Key
}
