package native_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	"github.com/viant/datly/bootstrap/connector"
)

// Deployed Core binaries have linked shapes and embedded SQL. Neither startup
// nor the first writer/reader invocation should require a source checkout.
func TestLinkedRuntimeWithoutSourceCheckout(t *testing.T) {
	for _, key := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(key, "")
	}
	ctx := context.Background()
	server, err := native.New(ctx, native.Options{
		SourceRoot:    filepath.Join(t.TempDir(), "source-is-not-deployed"),
		WorkspaceRoot: t.TempDir(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	service := data.NewService(server)
	row := conversationmodel.NewMutableConversationView(
		conversationmodel.WithConversationID("linked-without-source"),
		conversationmodel.WithConversationStatus("active"),
	)
	_, err = service.PatchConversations(ctx, []*conversationmodel.MutableConversationView{row})
	require.NoError(t, err)
	found, err := service.GetConversation(ctx, row.Id, nil)
	require.NoError(t, err)
	require.NotNil(t, found)
	require.Equal(t, row.Id, found.Id)
}

func TestLinkedRuntimeIncludesDeclaredContracts(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	contents, err := os.ReadFile(filepath.Join(filepath.Dir(source), "../../..", "components.json"))
	require.NoError(t, err)
	var manifest struct {
		Components []struct {
			Route string `json:"route"`
		} `json:"components"`
	}
	require.NoError(t, json.Unmarshal(contents, &manifest))
	ctx := context.Background()
	server, err := native.New(ctx, native.Options{Connectors: []connector.Config{{Name: "agently", Driver: "sqlite3", DSN: ":memory:"}}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	metadata, err := server.Metadata(ctx)
	require.NoError(t, err)
	routes := map[string]bool{}
	for _, component := range metadata.Components {
		for _, route := range component.Routes {
			routes[route.Path] = true
		}
	}
	for _, component := range manifest.Components {
		// The inventory's operation describes authoring, not necessarily the
		// HTTP method: deletion can be an operation of a canonical PATCH writer.
		require.True(t, routes[component.Route], "declared contract is missing from linked runtime: %s", component.Route)
	}
}
