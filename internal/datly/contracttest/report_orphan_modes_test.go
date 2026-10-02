package tests

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	artifactwrite "github.com/viant/agently-core/internal/datly/reporting/artifact/write"
	jobwrite "github.com/viant/agently-core/internal/datly/reporting/job/write"
	runwrite "github.com/viant/agently-core/internal/datly/reporting/run/write"
	"github.com/viant/bindly/locator"
	requestprovider "github.com/viant/bindly/provider/request"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"strings"
)

func orphanReportProvider(kind, column string) locator.Provider {
	return provider.Named(kind, func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		switch name {
		case "enabledDetach", "enabledDelete":
			return true, true, nil
		case "column":
			return column, true, nil
		}
		return nil, false, nil
	})
}

func TestReportArtifactDedicatedOrphanDelete(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	for _, private := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary rejects ownerless", true: "private deletes ownerless"}[private], func(t *testing.T) {
			db, _ := reportArtifactFixture(t, filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(source)))))
			db.SetMaxOpenConns(1)
			_, fixtureErr := db.Exec(`PRAGMA foreign_keys=OFF`)
			must(t, fixtureErr)
			_, err := db.Exec(`INSERT INTO report_export_artifact(artifact_id,job_id,artifact_ref,owner_id,format,content_type) VALUES('ownerless-artifact','','old://orphan','','pdf','application/pdf')`)
			must(t, err)
			rt, _, key := reportArtifactRuntime(t, db, "", true, nil)
			row := &artifactwrite.Artifact{}
			row.SetArtifactId("ownerless-artifact")
			row.SetShouldDelete(true)
			input := &artifactwrite.Input{}
			input.SetArtifacts([]*artifactwrite.Artifact{row})
			input.SetMode("delete")
			var bindings []locator.Provider
			if private {
				input.SetMode("orphanDelete")
				bindings = []locator.Provider{orphanReportProvider("orphandelete", "")}
			}
			_, err = rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/forge/reporting/artifact"}}, Input: input, Providers: bindings})
			var remaining int
			must(t, db.QueryRow(`SELECT COUNT(*) FROM report_export_artifact WHERE artifact_id='ownerless-artifact'`).Scan(&remaining))
			if private {
				must(t, err)
				if remaining != 0 {
					t.Fatal("private orphan was retained")
				}
			} else {
				if err == nil || remaining != 1 {
					t.Fatalf("ordinary mode weakened: remaining=%d err=%v", remaining, err)
				}
			}
		})
	}
	t.Run("HTTP cannot enable private delete", func(t *testing.T) {
		db, _ := reportArtifactFixture(t, filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(source)))))
		db.SetMaxOpenConns(1)
		_, fixtureErr := db.Exec(`PRAGMA foreign_keys=OFF`)
		must(t, fixtureErr)
		_, err := db.Exec(`INSERT INTO report_export_artifact(artifact_id,job_id,artifact_ref,owner_id,format,content_type) VALUES('ownerless-artifact','','old://orphan','','pdf','application/pdf')`)
		must(t, err)
		rt, _, _ := reportArtifactRuntime(t, db, "", true, nil)
		request := httptest.NewRequest("PATCH", "/v1/internal/forge/reporting/artifact?mode=orphanDelete&enabled=true", strings.NewReader(`{"orphanDelete":true,"data":[{"artifactId":"ownerless-artifact","shouldDelete":true}]}`))
		request.Header.Set("Content-Type", "application/json")
		scope, err := requestprovider.New(request)
		must(t, err)
		defer scope.Close()
		_, err = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/internal/forge/reporting/artifact", scope)
		if err == nil {
			t.Fatal("HTTP enabled orphan deletion")
		}
		var remaining int
		must(t, db.QueryRow(`SELECT COUNT(*) FROM report_export_artifact WHERE artifact_id='ownerless-artifact'`).Scan(&remaining))
		if remaining != 1 {
			t.Fatal("spoof deleted historical row")
		}
	})
}
func TestReportOrphanDetachPreservesMetadata(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(source))))
	t.Run("report run", func(t *testing.T) {
		db, _ := reportRunFixture(t, project)
		db.SetMaxOpenConns(1)
		_, fixtureErr := db.Exec(`PRAGMA foreign_keys=OFF`)
		must(t, fixtureErr)
		_, err := db.Exec(`UPDATE report_run SET conversation_id='missing-conversation' WHERE report_run_id='running'`)
		must(t, err)
		rt, _, key := reportRunRuntime(t, db, "u1", true, nil)
		var revision int64
		var updatedBefore any
		must(t, db.QueryRow(`SELECT revision,updated_at FROM report_run WHERE report_run_id='running'`).Scan(&revision, &updatedBefore))
		row := &runwrite.Run{}
		row.SetReportRunId("running")
		row.SetRevision(revision)
		row.SetConversationId(nil)
		input := &runwrite.Input{}
		input.SetMode("orphanDetach")
		input.SetRuns([]*runwrite.Run{row})
		_, err = rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/forge/reporting/run"}}, Input: input, Providers: []locator.Provider{orphanReportProvider("orphandetach", "conversation_id")}})
		must(t, err)
		var conversation, updatedAfter any
		var afterRevision int64
		must(t, db.QueryRow(`SELECT conversation_id,revision,updated_at FROM report_run WHERE report_run_id='running'`).Scan(&conversation, &afterRevision, &updatedAfter))
		if conversation != nil || afterRevision != revision || !reflect.DeepEqual(updatedBefore, updatedAfter) {
			t.Fatalf("orphan detach changed metadata: %v %d %v", conversation, afterRevision, updatedAfter)
		}
	})
	t.Run("export job", func(t *testing.T) {
		db, _ := reportJobFixture(t, project)
		db.SetMaxOpenConns(1)
		_, fixtureErr := db.Exec(`PRAGMA foreign_keys=OFF`)
		must(t, fixtureErr)
		_, err := db.Exec(`UPDATE report_export_job SET conversation_id='missing-conversation' WHERE job_id='running'`)
		must(t, err)
		rt, _, key := reportJobRuntime(t, db, "u1", true, nil)
		row := &jobwrite.Job{}
		row.SetJobId("running")
		row.SetConversationId(nil)
		input := &jobwrite.Input{}
		input.SetMode("orphanDetach")
		input.SetJobs([]*jobwrite.Job{row})
		_, err = rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/forge/reporting/job"}}, Input: input, Providers: []locator.Provider{orphanReportProvider("orphandetach", "conversation_id")}})
		must(t, err)
		var conversation any
		var status string
		must(t, db.QueryRow(`SELECT conversation_id,status FROM report_export_job WHERE job_id='running'`).Scan(&conversation, &status))
		if conversation != nil || status != "running" {
			t.Fatalf("job metadata changed: %v %s", conversation, status)
		}
	})
}
