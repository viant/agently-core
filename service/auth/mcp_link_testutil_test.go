package auth

import (
	"context"
	"testing"

	"github.com/viant/agently-core/internal/testutil/dbtest"
	userread "github.com/viant/agently-core/pkg/agently/user"
	userwrite "github.com/viant/agently-core/pkg/agently/user/write"
	"github.com/viant/datly"
	"github.com/viant/datly/view"
)

// newMCPLinkTestDAO builds an isolated file-backed SQLite datly service with
// the full Agently schema and only the components the MCP link tests need
// (users, oauth token write, oauth link state). Registering the entire
// application component set (data.NewDatlyInMemory) is avoided deliberately:
// it shares one in-memory database across the process and its unrelated
// component registrations carry a latent datly-internal registration race.
func newMCPLinkTestDAO(t *testing.T) *datly.Service {
	t.Helper()
	ctx := context.Background()
	db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "mcp-link-auth")
	t.Cleanup(cleanup)
	dbtest.LoadSQLiteSchema(t, db)
	dao, err := datly.New(ctx)
	if err != nil {
		t.Fatalf("datly.New() error = %v", err)
	}
	connector := view.NewConnector("agently", "sqlite", dbPath)
	if err := dao.AddConnectors(ctx, connector); err != nil {
		t.Fatalf("AddConnectors() error = %v", err)
	}
	if err := userread.DefineUserComponent(ctx, dao); err != nil {
		t.Fatalf("DefineUserComponent() error = %v", err)
	}
	if _, err := userwrite.DefineComponent(ctx, dao); err != nil {
		t.Fatalf("user write DefineComponent() error = %v", err)
	}
	return dao
}
