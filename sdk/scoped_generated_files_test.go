package sdk

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	conversation "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	iauth "github.com/viant/agently-core/internal/auth"
)

func TestListFilesUsesOwnedNativeIndexAndRetainsImageBytes(t *testing.T) {
	c := newDatlyObservedClient(t, 16)
	ctx := recoveryContext()
	bytes := []byte{0x89, 'P', 'N', 'G', 0, 1, 2, 3}
	uploaded, err := c.native.storeConversationFile(ctx, conversationFileInput{ConversationID: "thread", Name: "red-square.png", ContentType: "image/png", Data: bytes, Provider: "openai"})
	require.NoError(t, err)
	output, err := c.native.ListFiles(ctx, &ListFilesInput{ConversationID: "thread"})
	require.NoError(t, err)
	require.Len(t, output.Files, 1)
	file := output.Files[0]
	require.Equal(t, uploaded.ID, file.ID)
	require.Equal(t, "red-square.png", file.Name)
	require.Equal(t, "image/png", file.ContentType)
	require.EqualValues(t, len(bytes), file.Size)
	require.Contains(t, file.URI, "conversationId=thread")
	require.False(t, file.IsDir)
	require.Equal(t, output.Files, output.Rows)
	restored, err := c.native.DownloadFile(ctx, &DownloadFileInput{ConversationID: "thread", FileID: file.ID})
	require.NoError(t, err)
	require.Equal(t, bytes, restored.Data)
	filtered, err := c.native.ListFiles(ctx, &ListFilesInput{ConversationID: "thread", Prefix: "red-"})
	require.NoError(t, err)
	require.Len(t, filtered.Files, 1)
	empty, err := c.native.ListFiles(ctx, &ListFilesInput{ConversationID: "thread", Prefix: "../"})
	require.NoError(t, err)
	require.Empty(t, empty.Files)
	_, err = c.native.ListFiles(ctx, &ListFilesInput{Page: &PageInput{Limit: 1}})
	require.ErrorContains(t, err, "conversation ID")
	_, err = c.native.ListFiles(ctx, &ListFilesInput{ConversationID: "thread", Page: &PageInput{Limit: 1001}})
	require.ErrorContains(t, err, "1000")
	reader := iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "reader"})
	_, err = c.native.ListFiles(reader, &ListFilesInput{ConversationID: "thread"})
	require.Error(t, err, "foreign private conversation cannot be listed")
	public := conversation.NewConversation()
	public.SetId("thread")
	public.SetVisibility("public")
	require.NoError(t, c.conv.PatchConversations(ctx, public))
	shared, err := c.native.ListFiles(reader, &ListFilesInput{ConversationID: "thread", Page: &PageInput{Limit: 1, Direction: data.DirectionLatest}})
	require.NoError(t, err)
	require.Len(t, shared.Files, 1)
	require.Equal(t, file.ID, shared.Files[0].ID)
	require.Equal(t, file.Size, shared.Files[0].Size)
}
