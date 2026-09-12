package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	authctx "github.com/viant/agently-core/internal/auth"
	scratchpadsvc "github.com/viant/agently-core/protocol/tool/service/scratchpad"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
)

func TestResourceAvailabilityWithoutProvider(t *testing.T) {
	t.Setenv(scratchpadsvc.EnvScratchpadURI, "file://"+filepath.ToSlash(filepath.Join(t.TempDir(), "${userID}")))
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	d, err := scratchpadsvc.New().PublishArtifact(ctx, "", "sheet.csv", "text/csv", "", strings.NewReader("id\n1\n"))
	require.NoError(t, err)
	store := convmem.New()
	c := apiconv.NewConversation()
	c.SetId("conv")
	require.NoError(t, store.PatchConversations(ctx, c))
	svc := &Service{conversation: store}
	require.NoError(t, svc.persistResourceInputs(ctx, requestctx.TurnMeta{ConversationID: "conv", TurnID: "turn"}, []string{d.URI, d.URI}))
	conv, err := store.GetConversation(ctx, "conv")
	require.NoError(t, err)
	count := 0
	for _, turn := range conv.GetTranscript() {
		for _, msg := range turn.Message {
			if msg.Content != nil && strings.Contains(*msg.Content, d.URI) {
				count++
				require.Nil(t, msg.AttachmentPayloadId)
			}
		}
	}
	require.Equal(t, 1, count)
	require.Error(t, svc.persistResourceInputs(ctx, requestctx.TurnMeta{}, make([]string, 33)))
	other := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
	require.Error(t, svc.persistResourceInputs(other, requestctx.TurnMeta{ConversationID: "conv"}, []string{d.URI}))
}
func TestNativeIntentSurvivesHistoryPayload(t *testing.T) {
	subtype := "native_file"
	data := []byte("%PDF-data")
	id := "payload"
	svc := &Service{conversation: &stubConversationClient{payloads: map[string]*apiconv.Payload{id: {Id: id, MimeType: "application/pdf", Subtype: &subtype, InlineBody: &data}}}}
	msg := &apiconv.Message{AttachmentPayloadId: &id}
	atts, err := svc.attachmentsFromMessage(context.Background(), msg, nil, false)
	require.NoError(t, err)
	require.Len(t, atts, 1)
	require.True(t, atts[0].Native)
}

func TestResourceUploadsExposeExistingResourceTools(t *testing.T) {
	svc := &Service{}
	control, err := svc.resolveToolControl(context.Background(), &QueryInput{ResourceURIs: []string{"scratchpad://artifact/a"}})
	require.NoError(t, err)
	require.Contains(t, control.Tools, "resources-readImage")
	require.Contains(t, control.Tools, "resources-inspect")
	restricted, err := svc.resolveToolControl(context.Background(), &QueryInput{ResourceURIs: []string{"scratchpad://artifact/a"}, ToolsAllowed: []string{"custom:send"}})
	require.NoError(t, err)
	require.NotContains(t, restricted.Tools, "resources-readImage")
}
