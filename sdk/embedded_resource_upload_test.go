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
