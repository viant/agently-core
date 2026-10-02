package data

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/viant/agently-core/app/store/native"
	sqlitesvc "github.com/viant/agently-core/internal/service/sqlite"
	"github.com/viant/datly/bootstrap/connector"
	"github.com/viant/datly/standalone"
)

func sourceRoot() string {
	if configured := strings.TrimSpace(os.Getenv("AGENTLY_DATLY_SOURCE_ROOT")); configured != "" {
		return configured
	}
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}

// NewRuntime opens one application-owned stock runtime and connector pool.
// The caller shares it across stores and owns Shutdown.
func NewRuntime(ctx context.Context) (*standalone.Server, error) {
	return native.New(ctx, native.Options{SourceRoot: sourceRoot()})
}
func NewRuntimeFromWorkspace(ctx context.Context, root string) (*standalone.Server, error) {
	return native.New(ctx, native.Options{SourceRoot: sourceRoot(), WorkspaceRoot: root})
}

// NewRuntimeInMemory shares the initialized modernc memory database with the
// stock runtime through the registered sqlite driver. An sqlite3 connection
// would be a separate in-memory engine with an uninitialized schema.
func NewRuntimeInMemory(ctx context.Context) (*standalone.Server, error) {
	dsn, err := sqlitesvc.New("").EnsureInMemory(ctx)
	if err != nil {
		return nil, err
	}
	return native.New(ctx, native.Options{SourceRoot: sourceRoot(), Connectors: []connector.Config{{Name: "agently", Driver: "sqlite", DSN: dsn, MaxOpenConns: 2, MaxIdleConns: 2}}})
}
func NewThinServiceFromEnv(ctx context.Context) (Service, error) {
	server, err := NewRuntime(ctx)
	if err != nil {
		return nil, err
	}
	service := NewService(server).(*datlyService)
	service.closeOwned = server.Shutdown
	return service, nil
}
func NewThinServiceInMemory(ctx context.Context) (Service, error) {
	server, err := NewRuntimeInMemory(ctx)
	if err != nil {
		return nil, err
	}
	service := NewService(server).(*datlyService)
	service.closeOwned = server.Shutdown
	return service, nil
}
