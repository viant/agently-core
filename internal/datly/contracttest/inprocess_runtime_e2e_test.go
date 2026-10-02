package tests

import (
	"context"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	read "github.com/viant/agently-core/internal/datly/goal/read"
	write "github.com/viant/agently-core/internal/datly/goal/write"
	"github.com/viant/datly/bootstrap/connector"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
	"github.com/viant/datly/standalone"
	"github.com/viant/datly/standalone/config"
)

// Root callers can use the stock linked runtime in process, preserving the
// generated component boundary without an HTTP hop or handwritten registration.
func TestInProcessGoalReaderWriter(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	_, dbPath := goalFixture(t, project)
	ctx := context.Background()
	server, err := standalone.New(ctx, standalone.Options{
		Config: &config.Config{
			BaseDir:     project,
			Connector:   "agently",
			Connectors:  []connector.Config{{Name: "agently", Driver: "sqlite3", DSN: dbPath + "?_foreign_keys=on"}},
			GoBootstrap: &config.Packages{Packages: []string{"github.com/viant/agently-core/internal/datly/goal/..."}},
		},
		RequireLinked: true,
	})
	must(t, err)
	t.Cleanup(func() { must(t, server.Shutdown(context.Background())) })
	must(t, server.Reload(ctx, 1))

	readerKey := spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[read.ReaderComponent]().PkgPath(), Name: "reader"}
	writerKey := spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriterComponent]().PkgPath(), Name: "writer"}
	query := &read.GoalInput{}
	query.SetConversationID("c1")
	readGoal := func() *read.GoalView {
		value, err := server.InvokeComponent(ctx, dexec.ComponentRequest{
			Target: dexec.ComponentTarget{Component: readerKey, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/goal/{conversationId}"}},
			Input:  query,
		})
		must(t, err)
		out := value.(*read.GoalOutput)
		if len(out.Data) != 1 {
			t.Fatalf("goal rows=%d", len(out.Data))
		}
		return out.Data[0]
	}
	if before := readGoal(); before.Objective != "original" {
		t.Fatalf("before objective=%q", before.Objective)
	}
	objective := "in-process update"
	goal := &write.Goal{}
	goal.SetId("g1")
	goal.SetObjective(&objective)
	mutation := &write.Input{}
	mutation.SetGoals([]*write.Goal{goal})
	_, err = server.InvokeComponent(ctx, dexec.ComponentRequest{
		Target: dexec.ComponentTarget{Component: writerKey, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/goal"}},
		Input:  mutation,
	})
	must(t, err)
	if after := readGoal(); after.Objective != objective {
		t.Fatalf("after objective=%q", after.Objective)
	}
}
