package data

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/datly/bootstrap/connector"
	"github.com/viant/datly/standalone"
)

func newNativeMySQLRuntime(t *testing.T, dsn string) *standalone.Server {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	server, err := native.New(context.Background(), native.Options{SourceRoot: filepath.Join(filepath.Dir(file), "..", "..", ".."), Connectors: []connector.Config{{Name: "agently", Driver: "mysql", DSN: dsn}}})
	if err != nil {
		t.Fatalf("native.New(mysql): %v", err)
	}
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	return server
}
