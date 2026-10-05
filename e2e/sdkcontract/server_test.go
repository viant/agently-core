package sdkcontract_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/e2e/sdkcontract"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/sdk"
	svcauth "github.com/viant/agently-core/service/auth"
	"github.com/viant/agently-core/workspace"
)

func TestGoSDKActualApplicationJSONAuthQueueSSEAndDeletion(t *testing.T) {
	prior := workspace.Root()
	t.Cleanup(func() { workspace.SetRoot(prior) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture, err := sdkcontract.New(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, fixture.Runtime.Close(context.Background())) })
	server := httptest.NewServer(fixture.Handler)
	defer server.Close()
	owner, err := sdk.NewHTTP(server.URL, sdk.WithAuthToken(fixture.Token))
	require.NoError(t, err)
	other, err := sdk.NewHTTP(server.URL, sdk.WithAuthToken(fixture.OtherToken))
	require.NoError(t, err)
	unauth, err := sdk.NewHTTP(server.URL)
	require.NoError(t, err)
	_, err = unauth.GetConversation(ctx, sdkcontract.ConversationID)
	require.ErrorContains(t, err, "401")
	conversation, err := owner.GetConversation(ctx, sdkcontract.ConversationID)
	require.NoError(t, err)
	require.Equal(t, sdkcontract.ConversationID, conversation.Id)
	require.Equal(t, 11, *conversation.UsageInputTokens)
	require.Len(t, conversation.Transcript, 1)
	embedded, err := fixture.Backend.GetConversation(svcauth.InjectUser(ctx, sdkcontract.Owner), sdkcontract.ConversationID)
	require.NoError(t, err)
	require.Equal(t, embedded.Id, conversation.Id)
	require.Equal(t, embedded.Transcript[0].Id, conversation.Transcript[0].Id)
	state, err := owner.GetTranscript(ctx, &sdk.GetTranscriptInput{ConversationID: sdkcontract.ConversationID})
	require.NoError(t, err)
	require.Equal(t, "2", state.SchemaVersion)
	require.NotNil(t, state.Usage)
	err = other.DeleteConversation(ctx, "sdk-contract-delete")
	require.ErrorContains(t, err, "403")
	preserved, err := owner.GetConversation(ctx, "sdk-contract-delete")
	require.NoError(t, err)
	require.Equal(t, "sdk-contract-delete", preserved.Id)
	err = owner.EditQueuedTurn(ctx, &sdk.EditQueuedTurnInput{ConversationID: sdkcontract.QueueConversationID, TurnID: "sdk-contract-queued-turn", Content: "edited through actual HTTP"})
	require.NoError(t, err)
	queued, err := owner.GetConversation(ctx, sdkcontract.QueueConversationID)
	require.NoError(t, err)
	require.Equal(t, "edited through actual HTTP", *queued.Transcript[0].Message[0].Content)
	streamCtx, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	subscription, err := owner.ObserveApplicationEvents(streamCtx, &sdk.StreamEventsInput{ConversationID: sdkcontract.ConversationID})
	require.NoError(t, err)
	defer subscription.Close()
	require.NoError(t, fixture.Publish(ctx, 1))
	select {
	case event := <-subscription.C():
		require.NotNil(t, event)
		require.Equal(t, streaming.EventTypeUsage, event.Type)
		require.Equal(t, sdkcontract.ConversationID, event.StreamID)
		raw, err := json.Marshal(event.Patch)
		require.NoError(t, err)
		require.JSONEq(t, `{"inputTokens":11,"outputTokens":7}`, string(raw))
	case <-streamCtx.Done():
		t.Fatal("actual authenticated SSE event timed out")
	}
	require.NoError(t, owner.DeleteConversation(ctx, "sdk-contract-delete"))
	missing, err := owner.GetConversation(ctx, "sdk-contract-delete")
	require.NoError(t, err)
	require.Empty(t, missing.Id, "legacy missing detail contract is HTTP200 null")
}
