// Package native opens the linked Datly 1.0 runtime for application callers.
package native

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/datly/host"
	"github.com/viant/agently-core/internal/dbconfig"
	sqlitesvc "github.com/viant/agently-core/internal/service/sqlite"
	"github.com/viant/bindly/locator"
	"github.com/viant/datly/bootstrap/connector"
	"github.com/viant/datly/standalone"
	"github.com/viant/datly/standalone/config"
	xmodule "github.com/viant/x/module"
	xauth "github.com/viant/xdatly/auth"
)

// Options binds one application lifetime to one connector pool. SourceRoot is
// the Core source root used by Datly's linked bootstrap.
// The caller supplies trusted providers and owns the returned server shutdown.
type Options struct {
	SourceRoot           string
	WorkspaceRoot        string
	Connectors           []connector.Config
	Providers            []locator.Provider
	DefaultAuthenticator xauth.Authenticator
}

// New reads the application's database configuration and opens one stock Datly
// runtime. No application persistence SQL or component registration lives here.
func New(ctx context.Context, options Options) (*standalone.Server, error) {
	if ctx == nil {
		return nil, fmt.Errorf("native Datly context is required")
	}
	if strings.TrimSpace(options.SourceRoot) == "" {
		return nil, fmt.Errorf("native Datly source root is required")
	}
	connectors := options.Connectors
	if len(connectors) == 0 {
		config, err := connectorFromEnvironment(ctx, options.WorkspaceRoot)
		if err != nil {
			return nil, err
		}
		connectors = []connector.Config{config}
	}
	providers := AccessProviders()
	providers = append(providers, options.Providers...)
	// Discovery roots contain the linked contracts and orchestration. Dependency
	// type resolution still uses the enclosing Core module; unrelated SDK build
	// trees are outside discovery and cannot invalidate an application runtime.
	workspace, err := sourceWorkspace(ctx, options.SourceRoot)
	if err != nil {
		return nil, err
	}
	return host.New(ctx, standalone.Options{
		Workspace: workspace,
		Config: &config.Config{
			BaseDir:    options.SourceRoot,
			Connector:  "agently",
			Connectors: connectors,
		},
		Providers:            providers,
		DefaultAuthenticator: options.DefaultAuthenticator,
	})
}

func sourceWorkspace(ctx context.Context, root string) (*xmodule.Workspace, error) {
	return (xmodule.LocalWorkspace{
		BaseDir:    filepath.Join(root, "internal", "datly"),
		ModuleDirs: []string{filepath.Join(root, "internal", "store")},
	}).Resolve(ctx)
}

func connectorFromEnvironment(ctx context.Context, workspaceRoot string) (connector.Config, error) {
	driver := strings.TrimSpace(os.Getenv("AGENTLY_DB_DRIVER"))
	if driver == "" {
		driver = "sqlite"
	}
	dsn := strings.TrimSpace(os.Getenv("AGENTLY_DB_DSN"))
	if dsn == "" {
		path := strings.TrimSpace(os.Getenv("AGENTLY_DB_PATH"))
		if path == "" && strings.TrimSpace(workspaceRoot) == "" {
			return connector.Config{}, fmt.Errorf("AGENTLY_DB_DSN, AGENTLY_DB_PATH, or workspace root is required")
		}
		sqlite := sqlitesvc.New(workspaceRoot)
		if path != "" {
			sqlite = sqlite.WithPath(path)
		}
		var err error
		if dsn, err = sqlite.Ensure(ctx); err != nil {
			return connector.Config{}, err
		}
		// Keep the application's original SQLite driver and its provisioned
		// pragmas. Translate them only when the caller explicitly chose sqlite3.
		if strings.EqualFold(driver, "sqlite3") {
			dsn, err = sqlite3DSN(dsn)
			if err != nil {
				return connector.Config{}, err
			}
		}
	}
	expanded, _, err := dbconfig.ExpandDSN(ctx, dsn, os.Getenv("AGENTLY_DB_SECRETS"))
	if err != nil {
		return connector.Config{}, err
	}
	result := connector.Config{Name: "agently", Driver: driver, DSN: expanded}
	if strings.EqualFold(driver, "mysql") {
		result.ConnMaxLifetimeMs = int((55 * time.Minute) / time.Millisecond)
		result.ConnMaxIdleTimeMs = int((5 * time.Minute) / time.Millisecond)
		result.MaxIdleConns = 4
	} else if strings.EqualFold(driver, "sqlite3") || strings.EqualFold(driver, "sqlite") {
		result.MaxOpenConns = 2
		result.MaxIdleConns = 2
	}
	return result, nil
}

func sqlite3DSN(dsn string) (string, error) {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("invalid provisioned SQLite DSN: %w", err)
	}
	values := parsed.Query()
	values.Del("_pragma")
	values.Set("_foreign_keys", "on")
	values.Set("_busy_timeout", "5000")
	values.Set("_journal_mode", "WAL")
	values.Set("_synchronous", "NORMAL")
	parsed.RawQuery = values.Encode()
	return parsed.String(), nil
}
