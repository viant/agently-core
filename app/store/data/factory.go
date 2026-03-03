package data

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

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
	oauthread "github.com/viant/agently-core/pkg/agently/user/oauth"
	oauthwrite "github.com/viant/agently-core/pkg/agently/user/oauth/write"
	"github.com/viant/datly"
	"github.com/viant/datly/view"
)

const (
	defaultConnMaxLifetime = 55 * time.Minute
	defaultConnMaxIdle     = 5 * time.Minute
	defaultMaxIdleConns    = 4
)

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
		if dsn == "" {
			initErr = fmt.Errorf("AGENTLY_DB_DSN is required")
			return
		}

		conn := view.NewConnector("agently", driver, dsn)
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
	if err := oauthread.DefineTokenComponent(ctx, svc); err != nil {
		return err
	}
	if _, err := oauthwrite.DefineComponent(ctx, svc); err != nil {
		return err
	}
	return nil
}
