package toolexec

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	authctx "github.com/viant/agently-core/internal/auth"
	scratchpadsvc "github.com/viant/agently-core/protocol/tool/service/scratchpad"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
)

func TestNativeResourcePersistenceAndRetry(t *testing.T) {
	t.Setenv(scratchpadsvc.EnvScratchpadURI, "file://"+filepath.ToSlash(filepath.Join(t.TempDir(), "${userID}")))
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	store := convmem.New()
	turn := requestctx.TurnMeta{ConversationID: "conv", TurnID: "turn"}
	c := apiconv.NewConversation()
	c.SetId("conv")
	require.NoError(t, store.PatchConversations(ctx, c))
	data := []byte("%PDF-example")
	d, err := scratchpadsvc.New().PublishArtifact(ctx, "", "a.pdf", "application/pdf", "", bytes.NewReader(data))
	require.NoError(t, err)
	result, _ := json.Marshal(map[string]interface{}{"native": map[string]string{"uri": d.URI, "name": d.Name, "mimeType": d.MimeType, "sha256": d.SHA256}})
	args := map[string]interface{}{"representation": "native"}
	for i := 0; i < 2; i++ {
		require.NoError(t, persistNativeResource(ctx, store, turn, "call", "resources-read", args, string(result)))
	}
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("conv\x00call\x00"+d.URI)).String()
	p, err := store.GetPayload(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, p)
	require.Equal(t, "native_file", *p.Subtype)
	require.Equal(t, data, *p.InlineBody)
	require.NoError(t, persistNativeResource(ctx, nil, turn, "call", "untrusted-read", args, string(result)))
	require.NoError(t, persistNativeResource(ctx, nil, turn, "call", "resources-read", nil, string(result)))
	require.Error(t, persistNativeResource(ctx, nil, turn, "call", "resources-read", args, string(result)))
	other := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
	require.Error(t, persistNativeResource(other, store, turn, "call", "resources-read", args, string(result)))
	require.Error(t, persistNativeResource(ctx, store, turn, "call", "resources-read", args, `{"native":{"uri":"file:///tmp/private"}}`))
}

func TestNativePresentationFailureIsToolFailure(t *testing.T) {
	t.Setenv(scratchpadsvc.EnvScratchpadURI, "file://"+filepath.ToSlash(filepath.Join(t.TempDir(), "${userID}")))
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	turn := requestctx.TurnMeta{ConversationID: "native-failure", TurnID: "turn"}
	ctx = requestctx.WithTurnMeta(ctx, turn)
	reg := &scriptedRegistry{script: []scriptedResult{{result: `{"native":{"uri":"scratchpad://artifact/missing","sha256":"missing"}}`}}}
	out, _, err := ExecuteToolStep(ctx, reg, StepInfo{ID: "native-failure-call", Name: "resources-read", Args: map[string]interface{}{"representation": "native"}}, &stubConv{})
	require.Error(t, err)
	require.NotEmpty(t, out.Error)
}
