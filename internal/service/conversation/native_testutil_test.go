package conversation

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/datly/bootstrap/connector"
	"github.com/viant/datly/standalone"
)

func testNativeInvoker(t *testing.T, dbPath string) *standalone.Server {
	t.Helper()
	_, source, _, _ := runtime.Caller(0)
	options := native.Options{SourceRoot: filepath.Join(filepath.Dir(source), "../../.."), WorkspaceRoot: t.TempDir()}
	if dbPath != "" {
		options.Connectors = []connector.Config{{Name: "agently", Driver: "sqlite3", DSN: dbPath}}
	}
	server, err := native.New(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return server
}
