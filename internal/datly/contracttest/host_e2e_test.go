package tests

import (
	"context"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	read "github.com/viant/agently-core/internal/datly/goal/read"
	write "github.com/viant/agently-core/internal/datly/goal/write"
	"github.com/viant/agently-core/internal/datly/host"
	"github.com/viant/datly/bootstrap/connector"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
	"github.com/viant/datly/standalone"
	"github.com/viant/datly/standalone/config"
)

// The full linked host invokes private generated components in process using
// its configured connector without binding an HTTP listener.
func TestLinkedHostGoalReaderWriter(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	_, dbPath := goalFixture(t, project)
	ctx := context.Background()
	server, err := host.New(ctx, standalone.Options{Config: &config.Config{
		BaseDir:    project,
		Connector:  "agently",
		Connectors: []connector.Config{{Name: "agently", Driver: "sqlite3", DSN: dbPath + "?_foreign_keys=on", MaxOpenConns: 2}},
	}})
	must(t, err)
	t.Cleanup(func() { must(t, server.Shutdown(context.Background())) })
	column, err := server.InspectColumn(ctx, "agently", "user_oauth_token", "provider")
	must(t, err)
	if column.Dialect != "sqlite" || column.Length != nil {
		t.Fatalf("SQLite provider metadata = %+v", column)
	}
	query := &read.GoalInput{}
	query.SetConversationID("c1")
	reader := dexec.ComponentTarget{
		Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[read.ReaderComponent]().PkgPath(), Name: "reader"},
		Route:     spec.RouteRef{Method: "GET", Path: "/v1/api/agently/goal/{conversationId}"},
	}
	value, err := server.InvokeComponent(ctx, dexec.ComponentRequest{Target: reader, Input: query})
	must(t, err)
	before := value.(*read.GoalOutput)
	if len(before.Data) != 1 || before.Data[0].Objective != "original" {
		t.Fatalf("linked host reader before=%+v", before.Data)
	}
	objective := "linked host update"
	row := &write.Goal{}
	row.SetId("g1")
	row.SetObjective(&objective)
	input := &write.Input{}
	input.SetGoals([]*write.Goal{row})
	_, err = server.InvokeComponent(ctx, dexec.ComponentRequest{
		Target: dexec.ComponentTarget{
			Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriterComponent]().PkgPath(), Name: "writer"},
			Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/goal"},
		},
		Input: input,
	})
	must(t, err)
	value, err = server.InvokeComponent(ctx, dexec.ComponentRequest{Target: reader, Input: query})
	must(t, err)
	after := value.(*read.GoalOutput)
	if len(after.Data) != 1 || after.Data[0].Objective != objective {
		t.Fatalf("linked host reader after=%+v", after.Data)
	}
}
