package sdkcontract_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	fixture "github.com/viant/agently-core/e2e/sdkcontract"
	authctx "github.com/viant/agently-core/internal/auth"
	message "github.com/viant/agently-core/model/message"
	"github.com/viant/agently-core/sdk"
)

func TestActualHTTPMessagePageGeneratedScalarAlias(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	ctx := context.Background()
	server, err := fixture.New(ctx, t.TempDir())
	require.NoError(t, err)
	defer server.Runtime.Close(ctx)
	owner := authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: fixture.Owner})
	for i, id := range []string{"page-old", "page-middle", "page-new"} {
		m := &message.Message{}
		m.SetId(id)
		m.SetConversationID(fixture.ConversationID)
		m.SetTurnID("sdk-contract-turn")
		m.SetRole("assistant")
		m.SetType("text")
		m.SetContent("ORANGE-42 café")
		m.SetCreatedAt(time.Date(2026, 10, 1, 1, i, 0, 0, time.UTC))
		_, err = server.Runtime.Data.PatchMessages(owner, []*message.Message{m})
		require.NoError(t, err)
	}
	httpServer := httptest.NewServer(server.Handler)
	defer httpServer.Close()
	client, err := sdk.NewHTTP(httpServer.URL, sdk.WithAuthToken(server.Token))
	require.NoError(t, err)
	first, err := client.GetMessages(ctx, &sdk.GetMessagesInput{ConversationID: fixture.ConversationID, Roles: []string{"assistant"}, Page: &sdk.PageInput{Limit: 2}})
	require.NoError(t, err)
	require.Len(t, first.Rows, 2)
	require.True(t, first.HasMore)
	require.Equal(t, "page-new", first.Rows[0].Id)
	require.Equal(t, "page-middle", first.Rows[1].Id)
	second, err := client.GetMessages(ctx, &sdk.GetMessagesInput{ConversationID: fixture.ConversationID, Roles: []string{"assistant"}, Page: &sdk.PageInput{Limit: 2, Cursor: first.NextCursor, Direction: sdk.DirectionBefore}})
	require.NoError(t, err)
	require.Len(t, second.Rows, 1)
	require.False(t, second.HasMore)
	require.Equal(t, "page-old", second.Rows[0].Id)
	req, err := http.NewRequest("GET", httpServer.URL+"/v1/messages?conversationId="+fixture.ConversationID+"&roles=assistant&limit=2", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+server.Token)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, 200, res.StatusCode)
	raw, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &envelope))
	rowsRaw := envelope["Rows"]
	if rowsRaw == nil {
		rowsRaw = envelope["rows"]
	}
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rowsRaw, &rows))
	require.Len(t, rows, 2)
	require.Len(t, rows[0], 29)
	require.Equal(t, `"ORANGE-42 café"`, string(rows[0]["content"]))
	require.Equal(t, "0", string(rows[0]["interim"]))
	require.Contains(t, rows[0], "rawContent")
	require.NotContains(t, rows[0], "modelCall")
	require.NotContains(t, rows[0], "elicitation")
}
