package native_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/data"
	convwrite "github.com/viant/agently-core/pkg/agently/conversation/write"
)

func TestWorkspaceRuntimeNativeMemoryFactoryBorrowedLifetime(t *testing.T) {
	ctx := context.Background()
	server, err := data.NewRuntimeInMemory(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(ctx)) })
	identity, err := server.ConnectionIdentity(ctx, "agently")
	require.NoError(t, err)
	require.NotEmpty(t, identity)
	service := data.NewService(server)
	row := convwrite.NewMutableConversationView(convwrite.WithConversationID("native-factory-lifetime"), convwrite.WithConversationStatus("active"))
	_, err = service.PatchConversations(ctx, []*convwrite.MutableConversationView{row})
	require.NoError(t, err)
	require.NoError(t, data.CloseService(ctx, service))
	found, err := service.GetConversation(ctx, row.Id, nil)
	require.NoError(t, err)
	require.NotNil(t, found)
	require.Equal(t, row.Id, found.Id)
}
