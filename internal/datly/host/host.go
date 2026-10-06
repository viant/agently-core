// Package host links the selected persistence components for both the standalone
// executable and the embedded application. Datly owns binding and execution.
package host

import (
	"context"
	_ "github.com/viant/agently-core/internal/datly/message/write"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/viant/agently-core/internal/datly/link"
	_ "github.com/viant/agently-core/internal/store/conversationmaintenance"
	_ "github.com/viant/agently-core/internal/store/conversationtree"
	_ "github.com/viant/agently-core/internal/store/maintenancelease"
	_ "github.com/viant/agently-core/internal/store/orphanmaintenance"
	_ "github.com/viant/agently-core/internal/store/queuereorder"
	_ "github.com/viant/agently-core/internal/store/reporting/adoption"
	_ "github.com/viant/agently-core/internal/store/reporting/exportcomplete"
	_ "github.com/viant/agently-core/internal/store/reporting/exportsubmit"
	_ "github.com/viant/agently-core/internal/store/scheduledelete"
	_ "github.com/viant/agently-core/internal/store/scheduledmaintenance"
	_ "github.com/viant/agently-core/internal/store/technicalmaintenance"
	_ "github.com/viant/agently-core/internal/store/terminalartifact"
	"github.com/viant/datly/standalone"
	"github.com/viant/datly/standalone/config"
	_ "github.com/viant/sqlx/metadata/product/mysql"
	_ "github.com/viant/sqlx/metadata/product/sqlite"
	_ "modernc.org/sqlite"
)

// New opens a single native runtime and its configured connector pools. It
// publishes the initial generation without opening an HTTP or MCP listener.
// The caller owns Shutdown. Linked runtime reloads require no source checkout.
// Providers and authentication options retain their stock Datly semantics.
func New(ctx context.Context, options standalone.Options) (*standalone.Server, error) {
	options.RequireLinked = true
	if options.Config != nil && options.Config.GoBootstrap == nil {
		options.Config.GoBootstrap = &config.Packages{LinkedOnly: true, Packages: []string{
			"github.com/viant/agently-core/internal/datly/...",
			"github.com/viant/agently-core/internal/store/...",
		}}
	}
	server, err := standalone.New(ctx, options)
	if err != nil {
		return nil, err
	}
	if err := server.Reload(ctx, 1); err != nil {
		_ = server.Shutdown(context.Background())
		return nil, err
	}
	return server, nil
}
