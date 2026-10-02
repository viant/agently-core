// Package host links the selected persistence components for both the standalone
// executable and the embedded application. Datly owns binding and execution.
package host

import (
	"context"
	messagewrite "github.com/viant/agently-core/internal/datly/message/write"
	"reflect"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/mattn/go-sqlite3"
	_ "github.com/viant/agently-core/internal/datly/link"
	conversationmaintenance "github.com/viant/agently-core/internal/store/conversationmaintenance"
	tree "github.com/viant/agently-core/internal/store/conversationtree"
	maintenance "github.com/viant/agently-core/internal/store/maintenancelease"
	orphanmaintenance "github.com/viant/agently-core/internal/store/orphanmaintenance"
	reorder "github.com/viant/agently-core/internal/store/queuereorder"
	adoption "github.com/viant/agently-core/internal/store/reporting/adoption"
	complete "github.com/viant/agently-core/internal/store/reporting/exportcomplete"
	submit "github.com/viant/agently-core/internal/store/reporting/exportsubmit"
	scheduledelete "github.com/viant/agently-core/internal/store/scheduledelete"
	scheduledmaintenance "github.com/viant/agently-core/internal/store/scheduledmaintenance"
	technicalmaintenance "github.com/viant/agently-core/internal/store/technicalmaintenance"
	terminalartifact "github.com/viant/agently-core/internal/store/terminalartifact"
	"github.com/viant/datly/cmd/command"
	"github.com/viant/datly/standalone"
	"github.com/viant/datly/standalone/config"
	_ "github.com/viant/sqlx/metadata/product/mysql"
	_ "github.com/viant/sqlx/metadata/product/sqlite"
	"github.com/viant/x"
)

func exports() (*x.Registry, []any, error) {
	registry := x.NewRegistry()
	registry.Register(x.NewType(reflect.TypeFor[messagewrite.CoreWriterComponent]()))
	for _, export := range []func() (*x.Registry, error){reorder.Exports, adoption.Exports, submit.Exports, complete.Exports, maintenance.Exports, tree.Exports, scheduledelete.Exports, terminalartifact.Exports, conversationmaintenance.Exports, scheduledmaintenance.Exports, technicalmaintenance.Exports, orphanmaintenance.Exports} {
		item, err := export()
		if err != nil {
			return nil, nil, err
		}
		registry.Merge(item)
		if err := registry.RegisterFunctions(item.Functions()...); err != nil {
			return nil, nil, err
		}
	}
	return registry, []any{messagewrite.CoreWriterComponent{}, reorder.ReorderComponent{}, adoption.Component{}, submit.Component{}, complete.Component{}, maintenance.PurgeComponent{}, tree.DeleteComponent{}, scheduledelete.Component{}, terminalartifact.CleanupComponent{}, conversationmaintenance.Component{}, scheduledmaintenance.Component{}, technicalmaintenance.Component{}, orphanmaintenance.ApplyComponent{}}, nil
}

// Command uses the same linked contracts and business factories as New.
func Command() (command.Service, error) {
	registry, holders, err := exports()
	return command.Service{Registry: registry, Holders: holders}, err
}

// New opens a single native runtime and its configured connector pools. It
// publishes the initial generation without opening an HTTP or MCP listener.
// The caller owns Shutdown and must keep BaseDir source available for reload.
// Providers and authentication options retain their stock Datly semantics.
func New(ctx context.Context, options standalone.Options) (*standalone.Server, error) {
	registry, holders, err := exports()
	if err != nil {
		return nil, err
	}
	if options.Registry != nil {
		registry.Merge(options.Registry)
		if err := registry.RegisterFunctions(options.Registry.Functions()...); err != nil {
			return nil, err
		}
	}
	options.Registry = registry
	options.Holders = append(holders, options.Holders...)
	options.RequireLinked = true
	if options.Config != nil && options.Config.GoBootstrap == nil {
		options.Config.GoBootstrap = &config.Packages{Packages: []string{
			"github.com/viant/agently-core/internal/datly/...",
			"github.com/viant/agently-core/internal/store/queuereorder",
			"github.com/viant/agently-core/internal/store/maintenancelease",
			"github.com/viant/agently-core/internal/store/conversationtree",
			"github.com/viant/agently-core/internal/store/scheduledelete",
			"github.com/viant/agently-core/internal/store/terminalartifact",
			"github.com/viant/agently-core/internal/store/conversationmaintenance",
			"github.com/viant/agently-core/internal/store/scheduledmaintenance",
			"github.com/viant/agently-core/internal/store/technicalmaintenance",
			"github.com/viant/agently-core/internal/store/orphanmaintenance",
			"github.com/viant/agently-core/internal/store/reporting/adoption",
			"github.com/viant/agently-core/internal/store/reporting/exportsubmit",
			"github.com/viant/agently-core/internal/store/reporting/exportcomplete",
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
