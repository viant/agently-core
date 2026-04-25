package data

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/viant/agently-core/internal/dbconfig"
	sqlitesvc "github.com/viant/agently-core/internal/service/sqlite"
	conversation "github.com/viant/agently-core/pkg/agently/conversation"
	conversationlist "github.com/viant/agently-core/pkg/agently/conversation/list"
	conversationwrite "github.com/viant/agently-core/pkg/agently/conversation/write"
	gfread "github.com/viant/agently-core/pkg/agently/generatedfile/read"
	message "github.com/viant/agently-core/pkg/agently/message"
	elicitationmsg "github.com/viant/agently-core/pkg/agently/message/elicitation"
	messagelist "github.com/viant/agently-core/pkg/agently/message/list"
	messagewrite "github.com/viant/agently-core/pkg/agently/message/write"
	modelcallwrite "github.com/viant/agently-core/pkg/agently/modelcall/write"
	payload "github.com/viant/agently-core/pkg/agently/payload"
	payloadwrite "github.com/viant/agently-core/pkg/agently/payload/write"
	run "github.com/viant/agently-core/pkg/agently/run"
	runactive "github.com/viant/agently-core/pkg/agently/run/active"
	runstale "github.com/viant/agently-core/pkg/agently/run/stale"
	runsteps "github.com/viant/agently-core/pkg/agently/run/steps"
	runwrite "github.com/viant/agently-core/pkg/agently/run/write"
	toolcall "github.com/viant/agently-core/pkg/agently/toolcall/byOp"
	toolcallwrite "github.com/viant/agently-core/pkg/agently/toolcall/write"
	turn "github.com/viant/agently-core/pkg/agently/turn/active"
	turnbyid "github.com/viant/agently-core/pkg/agently/turn/byId"
	turnlistall "github.com/viant/agently-core/pkg/agently/turn/list"
	turnnext "github.com/viant/agently-core/pkg/agently/turn/nextQueued"
	turncount "github.com/viant/agently-core/pkg/agently/turn/queuedCount"
	turnlist "github.com/viant/agently-core/pkg/agently/turn/queuedList"
	turnwrite "github.com/viant/agently-core/pkg/agently/turn/write"
	turnqueueread "github.com/viant/agently-core/pkg/agently/turnqueue/read"
	turnqueuewrite "github.com/viant/agently-core/pkg/agently/turnqueue/write"
	userread "github.com/viant/agently-core/pkg/agently/user"
	oauthread "github.com/viant/agently-core/pkg/agently/user/oauth"
	oauthwrite "github.com/viant/agently-core/pkg/agently/user/oauth/write"
	userwrite "github.com/viant/agently-core/pkg/agently/user/write"
	"github.com/viant/datly"
	"github.com/viant/datly/view"
	"github.com/viant/scy"
)

const (
	defaultConnMaxLifetime = 55 * time.Minute
	defaultConnMaxIdle     = 5 * time.Minute
	defaultMaxIdleConns    = 4
	// defaultSQLiteMaxOpenConns caps concurrent SQLite connections.
	// A file-backed SQLite DB (even with WAL) serializes writers at the
	// file level and its shared-cache internal mutexes don't scale with
	// connection count — an unbounded pool (Go's default) produces
	// prepare-storms under bursty load, surfacing as "context canceled"
	// errors when the client ctx expires before the driver can compile
	// a statement. 4 is a conservative sweet spot: enough reader
	// parallelism in WAL mode, low enough to avoid lock churn.
	defaultSQLiteMaxOpenConns = 4
	defaultSQLiteMaxIdleConns = 4
)

// applySQLitePoolDefaults configures the datly Connector with a
// bounded connection pool for SQLite. Only applied when the driver
// is SQLite — MySQL uses its own tuning block above. Caller-set
// values are preserved; this only fills zero defaults.
func applySQLitePoolDefaults(conn *view.Connector) {
	if conn == nil {
		return
	}
	if conn.MaxOpenConns == 0 {
		conn.MaxOpenConns = defaultSQLiteMaxOpenConns
	}
	if conn.MaxIdleConns == 0 {
		conn.MaxIdleConns = defaultSQLiteMaxIdleConns
	}
}

var (
	sharedDAO *datly.Service
	daoOnce   sync.Once
)

// NewDatly creates a singleton datly service with configured connector.
// It expects AGENTLY_DB_DSN and AGENTLY_DB_DRIVER to be set.
func NewDatly(ctx context.Context) (*datly.Service, error) {
	var initErr error
	daoOnce.Do(func() {
		var svc *datly.Service
		svc, initErr = datly.New(ctx)
		if initErr != nil {
			return
		}

		driver := strings.TrimSpace(os.Getenv("AGENTLY_DB_DRIVER"))
		if driver == "" {
			driver = "sqlite"
		}
		dsn := strings.TrimSpace(os.Getenv("AGENTLY_DB_DSN"))
		secrets := strings.TrimSpace(os.Getenv("AGENTLY_DB_SECRETS"))
		if dsn == "" {
			initErr = fmt.Errorf("AGENTLY_DB_DSN is required")
			return
		}
		secretResource, err := func() (*scy.Resource, error) {
			expanded, resource, err := dbconfig.ExpandDSN(ctx, dsn, secrets)
			if err != nil {
				return nil, err
			}
			dsn = expanded
			return resource, nil
		}()
		if err != nil {
			initErr = err
			return
		}

		conn := view.NewConnector("agently", driver, dsn)
		if secretResource != nil {
			conn.Secret = secretResource
		}
		if strings.EqualFold(driver, "mysql") {
			if conn.ConnMaxLifetimeMs == 0 {
				conn.ConnMaxLifetimeMs = int(defaultConnMaxLifetime / time.Millisecond)
			}
			if conn.ConnMaxIdleTimeMs == 0 {
				conn.ConnMaxIdleTimeMs = int(defaultConnMaxIdle / time.Millisecond)
			}
			if conn.MaxIdleConns == 0 {
				conn.MaxIdleConns = defaultMaxIdleConns
			}
		} else if strings.EqualFold(driver, "sqlite") {
			applySQLitePoolDefaults(conn)
		}

		if err := svc.AddConnectors(ctx, conn); err != nil {
			initErr = err
			return
		}
		if err := registerReadComponents(ctx, svc); err != nil {
			initErr = err
			return
		}
		sharedDAO = svc
	})
	if initErr != nil {
		return nil, initErr
	}
	return sharedDAO, nil
}

// NewDatlyServiceFromEnv is an alias kept for compatibility with existing patterns.
func NewDatlyServiceFromEnv(ctx context.Context) (*datly.Service, error) { return NewDatly(ctx) }

// NewDatlyFromWorkspace creates a datly service backed by file-based SQLite
// in the given workspace root directory ({root}/db/agently-core.db).
// Data persists across restarts.
func NewDatlyFromWorkspace(ctx context.Context, root string) (*datly.Service, error) {
	svc, err := datly.New(ctx)
	if err != nil {
		return nil, err
	}
	dsn, err := sqlitesvc.New(root).Ensure(ctx)
	if err != nil {
		return nil, err
	}
	conn := view.NewConnector("agently", "sqlite", dsn)
	applySQLitePoolDefaults(conn)
	if err := svc.AddConnectors(ctx, conn); err != nil {
		return nil, err
	}
	if err := registerReadComponents(ctx, svc); err != nil {
		return nil, err
	}
	return svc, nil
}

// NewDatlyInMemory creates a non-singleton datly service backed by in-memory sqlite.
func NewDatlyInMemory(ctx context.Context) (*datly.Service, error) {
	svc, err := datly.New(ctx)
	if err != nil {
		return nil, err
	}
	dsn, err := sqlitesvc.New("").EnsureInMemory(ctx)
	if err != nil {
		return nil, err
	}
	conn := view.NewConnector("agently", "sqlite", dsn)
	applySQLitePoolDefaults(conn)
	if err := svc.AddConnectors(ctx, conn); err != nil {
		return nil, err
	}
	if err := registerReadComponents(ctx, svc); err != nil {
		return nil, err
	}
	return svc, nil
}

// NewThinServiceFromEnv creates a thin data.Service backed by env-configured Datly.
func NewThinServiceFromEnv(ctx context.Context) (Service, error) {
	dao, err := NewDatly(ctx)
	if err != nil {
		return nil, err
	}
	return NewService(dao), nil
}

// NewThinServiceInMemory creates a thin data.Service backed by in-memory sqlite.
func NewThinServiceInMemory(ctx context.Context) (Service, error) {
	dao, err := NewDatlyInMemory(ctx)
	if err != nil {
		return nil, err
	}
	return NewService(dao), nil
}

func registerReadComponents(ctx context.Context, svc *datly.Service) error {
	if err := conversation.DefineConversationComponent(ctx, svc); err != nil {
		return err
	}
	if err := conversationlist.DefineConversationRowsComponent(ctx, svc); err != nil {
		return err
	}
	if err := message.DefineMessageComponent(ctx, svc); err != nil {
		return err
	}
	if err := messagelist.DefineMessageRowsComponent(ctx, svc); err != nil {
		return err
	}
	if err := elicitationmsg.DefineMessageComponent(ctx, svc); err != nil {
		return err
	}
	if err := turn.DefineActiveTurnsComponent(ctx, svc); err != nil {
		return err
	}
	if err := turnbyid.DefineTurnLookupComponent(ctx, svc); err != nil {
		return err
	}
	if err := turnlistall.DefineTurnRowsComponent(ctx, svc); err != nil {
		return err
	}
	if err := turnnext.DefineQueuedTurnComponent(ctx, svc); err != nil {
		return err
	}
	if err := turnlist.DefineQueuedTurnsComponent(ctx, svc); err != nil {
		return err
	}
	if err := turncount.DefineQueuedTotalComponent(ctx, svc); err != nil {
		return err
	}
	if err := turnqueueread.DefineQueueRowsComponent(ctx, svc); err != nil {
		return err
	}
	if err := run.DefineRunRowsComponent(ctx, svc); err != nil {
		return err
	}
	if err := runactive.DefineActiveRunsComponent(ctx, svc); err != nil {
		return err
	}
	if err := runstale.DefineStaleRunsComponent(ctx, svc); err != nil {
		return err
	}
	if err := runsteps.DefineRunStepsComponent(ctx, svc); err != nil {
		return err
	}
	if err := toolcall.DefineToolCallRowsComponent(ctx, svc); err != nil {
		return err
	}
	if err := payload.DefinePayloadRowsComponent(ctx, svc); err != nil {
		return err
	}
	if err := gfread.DefineComponent(ctx, svc); err != nil {
		return err
	}
	if _, err := conversationwrite.DefineComponent(ctx, svc); err != nil {
		return err
	}
	if _, err := messagewrite.DefineComponent(ctx, svc); err != nil {
		return err
	}
	if _, err := turnwrite.DefineComponent(ctx, svc); err != nil {
		return err
	}
	if _, err := turnqueuewrite.DefineComponent(ctx, svc); err != nil {
		return err
	}
	if _, err := modelcallwrite.DefineComponent(ctx, svc); err != nil {
		return err
	}
	if _, err := toolcallwrite.DefineComponent(ctx, svc); err != nil {
		return err
	}
	if _, err := payloadwrite.DefineComponent(ctx, svc); err != nil {
		return err
	}
	if _, err := conversationwrite.DefineDeleteComponent(ctx, svc); err != nil {
		return err
	}
	if _, err := messagewrite.DefineDeleteComponent(ctx, svc); err != nil {
		return err
	}
	if _, err := turnwrite.DefineDeleteComponent(ctx, svc); err != nil {
		return err
	}
	if _, err := modelcallwrite.DefineDeleteComponent(ctx, svc); err != nil {
		return err
	}
	if _, err := toolcallwrite.DefineDeleteComponent(ctx, svc); err != nil {
		return err
	}
	if _, err := payloadwrite.DefineDeleteComponent(ctx, svc); err != nil {
		return err
	}
	if _, err := runwrite.DefineComponent(ctx, svc); err != nil {
		return err
	}
	if _, err := runwrite.DefineDeleteComponent(ctx, svc); err != nil {
		return err
	}
	if err := userread.DefineUserComponent(ctx, svc); err != nil {
		return err
	}
	if _, err := userwrite.DefineComponent(ctx, svc); err != nil {
		return err
	}
	if err := oauthread.DefineTokenComponent(ctx, svc); err != nil {
		return err
	}
	if _, err := oauthwrite.DefineComponent(ctx, svc); err != nil {
		return err
	}
	return nil
}
