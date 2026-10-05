package tests

import (
	"context"
	cube "github.com/viant/agently-core/internal/datly/run/cube"
	read "github.com/viant/agently-core/internal/datly/run/read"
	steps "github.com/viant/agently-core/internal/datly/runsteps/read"
	conversation "github.com/viant/agently-core/internal/store/conversation"
	requestprovider "github.com/viant/bindly/provider/request"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExecutionRunKindReadIsolation(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	for _, mode := range []string{"rows", "active", "stale", "schedulerList", "schedulerRuns", "schedulerDue"} {
		t.Run(mode, func(t *testing.T) {
			db, _ := schedulerRunsFixture(t, project)
			_, err := db.Exec(`INSERT INTO run(id,run_kind,schedule_id,conversation_id,status,created_at,started_at) VALUES('protocol','agui','pub','c1','running','2099-01-01 00:00:00','2099-01-01 00:00:00')`)
			must(t, err)
			rt, key := schedulerRunsRuntime(t, db, "u1", mode)
			input := &read.RunRowsInput{}
			value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/run/{id}"}}, Input: input})
			must(t, err)
			rows := value.(*read.RunRowsOutput).Data
			if len(rows) == 0 {
				t.Fatal("protocol row hid all execution candidates")
			}
			for _, row := range rows {
				if row.Id == "protocol" {
					t.Fatal("AG-UI protocol run entered execution read")
				}
			}
			input.SetId("protocol")
			value, err = rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/run/{id}"}}, Input: input})
			must(t, err)
			if len(value.(*read.RunRowsOutput).Data) != 0 {
				t.Fatal("explicit protocol identity bypassed execution fence")
			}
		})
	}
}

func TestExecutionRunKindMutationIsolation(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	for _, body := range []string{
		`{"data":[{"id":"protocol","status":"succeeded"}]}`,
		`{"data":[{"id":"protocol","status":"succeeded","condition":{"leaseOwner":"owner-a"}}]}`,
		`{"data":[{"id":"protocol","shouldDelete":true}]}`,
		`{"data":[{"id":"new-protocol","runKind":"agui","status":"running"}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			db, _ := runParityFixture(t, project)
			_, err := db.Exec(`INSERT INTO run(id,run_kind,status,lease_owner,attempt,created_at) VALUES('protocol','agui','running','owner-a',1,'2026-01-01 00:00:00')`)
			must(t, err)
			rt, _ := runParityRuntime(t, db, "", true)
			request := httptest.NewRequest("PATCH", "/v1/api/agently/run", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			_, _ = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/run", scope)
			var kind, status, owner string
			var attempt int
			must(t, db.QueryRow(`SELECT run_kind,status,lease_owner,attempt FROM run WHERE id='protocol'`).Scan(&kind, &status, &owner, &attempt))
			if kind != "agui" || status != "running" || owner != "owner-a" || attempt != 1 {
				t.Fatalf("protocol row changed: %s %s %s %d", kind, status, owner, attempt)
			}
			var count int
			must(t, db.QueryRow(`SELECT COUNT(*) FROM run WHERE id='new-protocol'`).Scan(&count))
			if count != 0 {
				t.Fatal("execution writer inserted AG-UI protocol row")
			}
		})
	}
}

func TestExecutionRunKindCubeIsolation(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	db, _ := schedulerRunListFixture(t, project)
	_, err := db.Exec(`INSERT INTO run(id,run_kind,schedule_id,status,created_at) VALUES('protocol','agui','pub','running','2026-01-01 00:00:00')`)
	must(t, err)
	rt := runCubeRuntime(t, db, "u1", "scheduler", true)
	request := httptest.NewRequest("POST", "/v1/internal/agently/run/report/cube", strings.NewReader(`{"measures":{"RecordCount":true}}`))
	request.Header.Set("Content-Type", "application/json")
	scope, err := requestprovider.New(request)
	must(t, err)
	defer scope.Close()
	value, err := rt.ExecuteRoute(context.Background(), "POST", "/v1/internal/agently/run/report/cube", scope)
	must(t, err)
	out := value.(*cube.RunReportOutput)
	if len(out.Data) != 1 || out.Data[0].RecordCount != 3 {
		t.Fatalf("protocol run affected scheduler count: %+v", out.Data)
	}
}

func TestRunOwnedPayloadIsNotUnreferenced(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	db, _ := payloadFixture(t, project)
	_, err := db.Exec(`INSERT INTO run(id,run_kind,status,created_at) VALUES('protocol','agui','running','2026-01-01 00:00:00');UPDATE call_payload SET run_id='protocol' WHERE id='p1'`)
	must(t, err)
	rt, _, _ := payloadRuntime(t, db)
	store := &conversation.PayloadStore{Invoker: rt}
	must(t, store.DeleteUnreferencedTrusted(context.Background(), "p1"))
	var count int
	must(t, db.QueryRow(`SELECT COUNT(*) FROM call_payload WHERE id='p1'`).Scan(&count))
	if count != 1 {
		t.Fatal("live run-owned payload was garbage collected")
	}
	_, err = db.Exec(`DELETE FROM run WHERE id='protocol'`)
	must(t, err)
	must(t, store.DeleteUnreferencedTrusted(context.Background(), "p1"))
	must(t, db.QueryRow(`SELECT COUNT(*) FROM call_payload WHERE id='p1'`).Scan(&count))
	if count != 0 {
		t.Fatal("unowned and unreferenced payload was retained")
	}
}

func TestExecutionRunKindStepsIsolation(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	for _, internal := range []bool{false, true} {
		db, _ := runStepsFixture(t, project)
		_, err := db.Exec(`UPDATE run SET run_kind='agui' WHERE id='r1'`)
		must(t, err)
		rt, key := runStepsReaderRuntime(t, db, "u1", internal, true)
		for _, filters := range []map[string]any{nil, {"runId": "r1"}} {
			value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/run/steps"}}, Input: runStepsInput(t, filters)})
			must(t, err)
			rows := value.(*steps.RunStepsOutput).Data
			for _, row := range rows {
				if row.RunId != nil && *row.RunId == "r1" {
					t.Fatal("AG-UI run entered execution tool/model steps")
				}
			}
			if filters == nil && len(rows) == 0 {
				t.Fatal("execution step candidates disappeared")
			}
			if filters != nil && len(rows) != 0 {
				t.Fatal("explicit AG-UI run bypassed steps fence")
			}
		}
	}
}

func TestNativeExecutionWriterRejectsProtocolColumns(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	for _, body := range []string{
		`{"data":[{"id":"owned","protocolInputJson":"{}"}]}`,
		`{"data":[{"id":"owned","runKind":""}]}`,
		`{"data":[{"id":"owned","runKind":null}]}`,
		`{"data":[{"id":"owned","protocolLeaseOwner":"foreign"}]}`,
		`{"data":[{"id":"owned","protocolRevision":0}]}`,
		`{"data":[{"id":"new","status":"pending","protocolRunId":"wire"}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			db, _ := runParityFixture(t, project)
			_, err := db.Exec(`UPDATE run SET protocol_key='retained-key',protocol_revision=9 WHERE id='owned'`)
			must(t, err)
			rt, _ := runParityRuntime(t, db, "", true)
			request := httptest.NewRequest("PATCH", "/v1/api/agently/run", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			_, err = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/run", scope)
			if err == nil {
				t.Fatal("native writer accepted protocol mutation")
			}
			var key string
			var revision int
			must(t, db.QueryRow(`SELECT protocol_key,protocol_revision FROM run WHERE id='owned'`).Scan(&key, &revision))
			if key != "retained-key" || revision != 9 {
				t.Fatal("native writer changed protocol metadata")
			}
			var count int
			must(t, db.QueryRow(`SELECT COUNT(*) FROM run WHERE id='new'`).Scan(&count))
			if count != 0 {
				t.Fatal("protocol-labelled native insert persisted")
			}
		})
	}
}
