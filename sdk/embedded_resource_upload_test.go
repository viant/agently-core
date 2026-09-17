package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	authctx "github.com/viant/agently-core/internal/auth"
	gfread "github.com/viant/agently-core/pkg/agently/generatedfile/read"
	scratchpadsvc "github.com/viant/agently-core/protocol/tool/service/scratchpad"
)

func TestUploadPublishesUserResource(t *testing.T) {
	t.Setenv(scratchpadsvc.EnvScratchpadURI, "file://"+filepath.ToSlash(filepath.Join(t.TempDir(), "${userID}")))
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	client := &backendClient{conv: convmem.New()}
	out, err := client.UploadFile(ctx, &UploadFileInput{ConversationID: "conv", Name: "customers.csv", ContentType: "text/csv", Data: []byte("id\n1\n")})
	require.NoError(t, err)
	require.NotNil(t, out.Resource)
	require.Equal(t, out.ID, out.Resource.ID)
	_, r, err := scratchpadsvc.New().OpenArtifact(ctx, out.Resource.URI)
	require.NoError(t, err)
	data, err := io.ReadAll(r)
	r.Close()
	require.NoError(t, err)
	require.Equal(t, "id\n1\n", string(data))
	other := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
	_, _, err = scratchpadsvc.New().OpenArtifact(other, out.Resource.URI)
	require.Error(t, err)
}

func TestAuthenticatedPreConversationUpload(t *testing.T) {
	t.Setenv(scratchpadsvc.EnvScratchpadURI, "file://"+filepath.ToSlash(filepath.Join(t.TempDir(), "${userID}")))
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "a.csv")
	require.NoError(t, err)
	_, err = part.Write([]byte("id\n1\n"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	req := httptest.NewRequest(http.MethodPost, "/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req = req.WithContext(authctx.WithUserInfo(req.Context(), &authctx.UserInfo{Subject: "alice"}))
	response := httptest.NewRecorder()
	handleStagedUpload()(response, req)
	require.Equal(t, http.StatusOK, response.Code)
	var out UploadFileOutput
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &out))
	require.NotNil(t, out.Resource)
	require.Equal(t, out.Resource.URI, out.URI)
	_, r, err := scratchpadsvc.New().OpenArtifact(req.Context(), out.URI)
	require.NoError(t, err)
	r.Close()
}

func TestAttachArtifactToConversationDoesNotRepublishIt(t *testing.T) {
	t.Setenv(scratchpadsvc.EnvScratchpadURI, "file://"+filepath.ToSlash(filepath.Join(t.TempDir(), "${userID}")))
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	scratchpad := scratchpadsvc.New()
	descriptor, err := scratchpad.PublishArtifact(ctx, "artifact-1", "customers.csv", "text/csv", "browser", bytes.NewReader([]byte("id\n1\n")))
	require.NoError(t, err)

	store := convmem.New()
	client := &backendClient{conv: store}
	out, err := client.UploadFile(ctx, &UploadFileInput{ConversationID: "conv", ResourceURI: descriptor.URI})
	require.NoError(t, err)
	require.NotEqual(t, descriptor.ID, out.ID)
	require.Equal(t, descriptor, out.Resource)
	require.Equal(t, descriptor.Name, out.Name)
	require.Equal(t, descriptor.MimeType, out.MimeType)

	artifacts, _, err := scratchpad.ListArtifacts(ctx, "", 100)
	require.NoError(t, err)
	require.Len(t, artifacts, 1)
	require.Equal(t, descriptor.ID, artifacts[0].ID)

	files, err := store.GetGeneratedFiles(ctx, &gfread.Input{ConversationID: "conv"})
	require.NoError(t, err)
	require.Len(t, files, 1)
	file := files[0]
	require.Equal(t, out.ID, file.ID)
	require.Equal(t, "scratchpad", file.Provider)
	require.NotNil(t, file.ProviderFileID)
	require.Equal(t, descriptor.ID, *file.ProviderFileID)
	require.NotNil(t, file.Checksum)
	require.Equal(t, descriptor.SHA256, *file.Checksum)
	require.NotNil(t, file.PayloadID)

	payload, err := store.GetPayload(ctx, *file.PayloadID)
	require.NoError(t, err)
	require.NotNil(t, payload)
	require.NotNil(t, payload.InlineBody)
	require.Equal(t, []byte("id\n1\n"), *payload.InlineBody)
}

func TestAttachArtifactRejectsOtherUsersArtifact(t *testing.T) {
	t.Setenv(scratchpadsvc.EnvScratchpadURI, "file://"+filepath.ToSlash(filepath.Join(t.TempDir(), "${userID}")))
	alice := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	descriptor, err := scratchpadsvc.New().PublishArtifact(alice, "artifact-1", "private.txt", "text/plain", "", bytes.NewReader([]byte("private")))
	require.NoError(t, err)

	bob := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
	store := convmem.New()
	_, err = (&backendClient{conv: store}).UploadFile(bob, &UploadFileInput{ConversationID: "conv", ResourceURI: descriptor.URI})
	require.ErrorContains(t, err, "artifact unavailable")
	files, listErr := store.GetGeneratedFiles(bob, &gfread.Input{ConversationID: "conv"})
	require.NoError(t, listErr)
	require.Empty(t, files)
}
