package data

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	read "github.com/viant/agently-core/internal/datly/conversation/read"
	conversationmodel "github.com/viant/agently-core/model/conversation"
)

func TestNativeConversationAliasNormalizesBaseFallback(t *testing.T) {
	status := "failed"
	row := &read.ConversationView{Id: "conversation", ListMode: true, Stage: "base-stage", Status: &status}
	row.OnRelation(context.Background())
	require.Equal(t, "base-stage", row.Stage, "canonical list reads retain their computed stage")
	got := normalizeNativeConversationGet(context.Background(), row)
	require.Same(t, row, got, "generated public alias must not copy through JSON")
	require.False(t, got.ListMode)
	require.Equal(t, conversationmodel.StageWaiting, got.Stage, "get retains no-transcript fallback normalization")
	require.Equal(t, "failed", *got.Status, "explicit terminal status stays authoritative")
}

func TestNativeConversationRequestPresence(t *testing.T) {
	absent, err := nativeConversationInput(&conversationmodel.ConversationInput{IncludeTranscript: true})
	require.NoError(t, err)
	require.Nil(t, absent.Has, "unmarked fields remain absent")
	request := &conversationmodel.ConversationInput{}
	request.SetIncludeTranscript(false)
	request.SetQuery("")
	request.SetIds(nil)
	input, err := nativeConversationInput(request)
	require.NoError(t, err)
	require.NotNil(t, input.Has)
	require.True(t, input.Has.IncludeTranscript)
	require.False(t, input.IncludeTranscript)
	require.True(t, input.Has.Query)
	require.Equal(t, "", input.Query)
	require.True(t, input.Has.Ids)
	require.Nil(t, input.Ids)
	require.False(t, input.Has.VisibilitySubject, "public request cannot supply trusted visibility")
	require.False(t, input.Has.GraphMode, "public request cannot supply trusted graph mode")
}
